// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// defaultOllamaURL is the default Ollama server address.
const defaultOllamaURL = "http://localhost:11434"

// GemmaModel wraps a local LLM (via Ollama) used for classifying trace
// groups that don't match any deterministic pattern. It talks to the
// Ollama REST API rather than linking llama.cpp directly — this avoids
// CGo dependencies while keeping the model on-device.
//
// Graceful degradation is the pattern: if the model is not available,
// the engine falls back to unknown flows. All public methods are safe
// for concurrent use.
type GemmaModel struct {
	modelPath  string
	modelName  string
	version    string
	ollamaURL  string
	loadedAt   time.Time
	loaded     atomic.Bool
	loadErr    error // last Load() failure; nil when loaded or never attempted
	mu         sync.RWMutex
	httpClient *http.Client
	metrics    gemmaMetrics
}

type gemmaMetrics struct {
	totalInferences atomic.Int64
	totalTokens     atomic.Int64
	avgLatency      atomic.Int64 // nanoseconds
}

// NewGemmaModel creates a new model wrapper. The model is not loaded
// until Load() is called. modelPath is the Ollama model tag (e.g.
// "gemma3:4b"). version is stored for ModelInfo reporting.
// Set ollamaURL to "" to use the default http://localhost:11434.
func NewGemmaModel(modelPath, modelName, version string) *GemmaModel {
	if version == "" {
		version = "1.0.0-dev"
	}
	return &GemmaModel{
		modelPath:  modelPath,
		modelName:  modelName,
		version:    version,
		ollamaURL:  defaultOllamaURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetOllamaURL overrides the default Ollama server address.
// Call before Load().
func (g *GemmaModel) SetOllamaURL(url string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ollamaURL = url
}

// Load verifies the model is available in Ollama. It sends a request to
// /api/show to confirm the model exists and is ready. Returns
// ErrModelNotLoaded if the model is not found.
func (g *GemmaModel) Load(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.loaded.Load() {
		return nil // already loaded
	}

	// Reset any prior load failure; the retry below decides the new state.
	g.loadErr = nil

	body, err := json.Marshal(map[string]string{"name": g.modelName})
	if err != nil {
		g.loadErr = fmt.Errorf("load: marshal request: %w", err)
		return g.loadErr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		g.ollamaURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		g.loadErr = fmt.Errorf("load: create request: %w", err)
		return g.loadErr
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		g.loadErr = fmt.Errorf("load: ollama unreachable at %s: %w", g.ollamaURL, err)
		return g.loadErr
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		g.loadErr = types.ErrModelNotLoaded{}
		return g.loadErr
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		g.loadErr = fmt.Errorf("load: ollama returned %d: %s", resp.StatusCode, string(b))
		return g.loadErr
	}

	g.loadedAt = time.Now()
	g.loaded.Store(true)
	g.loadErr = nil
	return nil
}

// LoadError returns the last Load() failure, or nil if the model is
// loaded or Load was never attempted. Used for honest health/status
// reporting: a model that failed to load means classification is
// running in pattern-only mode, and the operator should see why.
func (g *GemmaModel) LoadError() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.loadErr
}

// OllamaURL returns the configured Ollama server URL.
func (g *GemmaModel) OllamaURL() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ollamaURL
}

// Unload marks the model as not loaded. Ollama manages model memory
// independently, so this is a logical operation — the model process
// in Ollama is unaffected.
func (g *GemmaModel) Unload() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loaded.Store(false)
}

// IsLoaded reports whether the model is currently marked as loaded.
func (g *GemmaModel) IsLoaded() bool {
	return g.loaded.Load()
}

// ollamaGenerateRequest is the JSON body sent to Ollama /api/generate.
type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// ollamaGenerateResponse is the JSON response from Ollama /api/generate.
type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// ClassifyBatch classifies a batch of trace groups using the Ollama
// model. When the model is not loaded it returns ErrModelNotLoaded.
// The prompt is built from the trace groups, sent to Ollama, and the
// JSON response is parsed into flows. On parse failure, all groups
// are returned as unknown (graceful degradation).
func (g *GemmaModel) ClassifyBatch(ctx context.Context, groups [][]types.Trace) ([]types.Flow, error) {
	if !g.IsLoaded() {
		return nil, types.ErrModelNotLoaded{}
	}

	select {
	case <-ctx.Done():
		return nil, types.ErrContextCancelled{}
	default:
	}

	// Snapshot fields under RLock, release before inference to avoid
	// RLock→Lock deadlock when updateAvgLatency acquires write lock.
	g.mu.RLock()
	modelName := g.modelName
	ollamaURL := g.ollamaURL
	httpClient := g.httpClient
	g.mu.RUnlock()

	start := time.Now()
	defer func() {
		g.metrics.totalInferences.Add(1)
		g.updateAvgLatency(time.Since(start))
	}()

	prompt := g.buildClassificationPrompt(groups)
	output, err := infer(ctx, prompt, modelName, ollamaURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("classify: inference: %w", err)
	}

	flows, parseErr := g.parseClassificationOutput(groups, output)
	if parseErr != nil {
		// Graceful degradation: return unknown flows on parse failure.
		flows = make([]types.Flow, 0, len(groups))
		for _, group := range groups {
			if len(group) == 0 {
				continue
			}
			flow := makeUnknownFlow("", group)
			flow.ID = uuid.Must(uuid.NewV7()).String()
			flow.Description = buildGroupDescription(group)
			flows = append(flows, flow)
		}
		return flows, nil
	}

	return flows, nil
}

// infer sends the prompt to Ollama and returns the model's response text.
func infer(ctx context.Context, prompt, modelName, ollamaURL string, httpClient *http.Client) (string, error) {
	reqBody := ollamaGenerateRequest{
		Model:  modelName,
		Prompt: prompt,
		Stream: false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("infer: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("infer: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("infer: ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("infer: ollama returned %d: %s", resp.StatusCode, string(b))
	}

	var genResp ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return "", fmt.Errorf("infer: decode response: %w", err)
	}

	return strings.TrimSpace(genResp.Response), nil
}

// buildClassificationPrompt constructs the model prompt from trace
// groups, following the S03 spec prompt template.
func (g *GemmaModel) buildClassificationPrompt(groups [][]types.Trace) string {
	var b strings.Builder

	b.WriteString("You are a classification system for agent behavior traces.\n")
	b.WriteString("Classify each trace group into a semantic flow.\n\n")
	b.WriteString("For each group provide: intent, phase, outcome, description, confidence.\n")
	b.WriteString("Valid phases: observation, deliberation, action, verification.\n")
	b.WriteString("Valid outcomes: success, failure, timeout, unknown.\n")
	b.WriteString("Respond as a JSON array, one object per group.\n\n")

	b.WriteString("Trace Groups:\n")
	for i, group := range groups {
		fmt.Fprintf(&b, "Group %d (%d syscalls):\n", i, len(group))
		for _, t := range group {
			args := strings.Join(t.Args, " ")
			if args == "" {
				args = t.Syscall
			}
			fmt.Fprintf(&b, "  %s %s\n", t.Syscall, args)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// classificationResult is the JSON shape expected from the model output.
type classificationResult struct {
	Intent      string  `json:"intent"`
	Phase       string  `json:"phase"`
	Outcome     string  `json:"outcome"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
}

// parseClassificationOutput parses the model's JSON response and maps
// each result to a flow built from the corresponding trace group.
func (g *GemmaModel) parseClassificationOutput(groups [][]types.Trace, output string) ([]types.Flow, error) {
	output = strings.TrimSpace(output)
	output = stripCodeFences(output)

	var results []classificationResult
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		return nil, types.ErrParseFailure{Raw: output}
	}

	if len(results) != len(groups) {
		return nil, types.ErrParseFailure{Raw: output}
	}

	flows := make([]types.Flow, 0, len(groups))
	for i, res := range results {
		group := groups[i]
		if len(group) == 0 {
			continue
		}
		flow := types.Flow{
			ID:          uuid.Must(uuid.NewV7()).String(),
			TraceIDs:    extractTraceIDs(group),
			Intent:      res.Intent,
			Phase:       types.FlowPhase(res.Phase),
			Outcome:     types.FlowOutcome(res.Outcome),
			Confidence:  res.Confidence,
			StartTime:   group[0].Timestamp,
			EndTime:     group[len(group)-1].Timestamp,
			Duration:    group[len(group)-1].Timestamp.Sub(group[0].Timestamp),
			Description: res.Description,
		}
		if flow.Description == "" {
			flow.Description = buildGroupDescription(group)
		}
		flows = append(flows, flow)
	}

	return flows, nil
}

// ModelInfo returns metadata about the loaded model.
func (g *GemmaModel) ModelInfo() ModelInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	loaded := g.IsLoaded()
	info := ModelInfo{
		Name:       g.modelName,
		Version:    g.version,
		DeviceType: "cpu",
		Kind:       "local",
		Ready:      loaded,
	}
	if loaded {
		info.LoadedAt = g.loadedAt
	}
	return info
}

// Health reports whether the model is operational for classification.
func (g *GemmaModel) Health(_ context.Context) error {
	if !g.IsLoaded() {
		return types.ErrModelNotLoaded{}
	}
	return nil
}

// Close unloads the model logically. Ollama process is unaffected.
func (g *GemmaModel) Close() error {
	g.Unload()
	return nil
}

// updateAvgLatency updates the running average latency.
func (g *GemmaModel) updateAvgLatency(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := g.metrics.totalInferences.Add(1)
	oldAvg := g.metrics.avgLatency.Load()
	newAvg := (oldAvg*(count-1) + int64(d)) / count
	g.metrics.avgLatency.Store(newAvg)
}

// stripCodeFences removes markdown code fence wrappers from model output.
// Gemma (and many LLMs) frequently wrap JSON responses in ```json / ```
// blocks. This strips those fences so the raw JSON can be parsed.
func stripCodeFences(s string) string {
	// Pattern: ```json\n...\n``` or ```\n...\n```
	const fence = "```"
	if strings.HasPrefix(s, fence) {
		s = s[len(fence):] // strip opening ```
		if idx := strings.IndexByte(s, '\n'); idx >= 0 {
			s = s[idx+1:] // strip language tag (json) and newline
		}
		if last := strings.LastIndex(s, fence); last >= 0 {
			s = s[:last] // strip closing ```
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// buildGroupDescription creates a human-readable summary of a trace
// group for fallback use when model output is unavailable.
func buildGroupDescription(group []types.Trace) string {
	if len(group) == 0 {
		return "empty group"
	}
	parts := make([]string, 0, len(group))
	for _, t := range group {
		if len(t.Args) > 1 {
			parts = append(parts, fmt.Sprintf("%s %s", t.Syscall, t.Args[1]))
		} else {
			parts = append(parts, t.Syscall)
		}
	}
	dur := group[len(group)-1].Timestamp.Sub(group[0].Timestamp)
	return fmt.Sprintf("unknown (%d syscalls: %s, %s)",
		len(group), strings.Join(parts, " → "), dur.Truncate(time.Millisecond))
}
