package classify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// GemmaModel wraps the on-device Gemma LLM used for classifying trace
// groups that don't match any deterministic pattern. In Phase 4 the
// model is a stub — Load() succeeds, but inference returns confidence-0
// flows with descriptions derived from prompt metadata. Real llama.cpp
// integration arrives in Phase 2.
//
// Graceful degradation is the pattern: if the model is not available,
// the engine falls back to unknown flows. All public methods are safe
// for concurrent use.
type GemmaModel struct {
	modelPath  string
	modelName  string
	version    string
	loadedAt   time.Time
	loaded     atomic.Bool
	mu         sync.RWMutex
	metrics    gemmaMetrics
}

type gemmaMetrics struct {
	totalInferences atomic.Int64
	totalTokens     atomic.Int64
	avgLatency      atomic.Int64 // nanoseconds
}

// NewGemmaModel creates a new model wrapper. The model is not loaded
// until Load() is called. version is stored for ModelInfo reporting.
func NewGemmaModel(modelPath, modelName, version string) *GemmaModel {
	if version == "" {
		version = "1.0.0-dev"
	}
	return &GemmaModel{
		modelPath: modelPath,
		modelName: modelName,
		version:   version,
	}
}

// Load loads the model into memory. In the stub implementation this
// simply marks the model as loaded and returns nil. Real llama.cpp
// integration (mmap the GGUF, initialise context, warm up) is Phase 2.
func (g *GemmaModel) Load(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadedAt = time.Now()
	g.loaded.Store(true)
	return nil
}

// Unload releases the model from memory.
func (g *GemmaModel) Unload() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loaded.Store(false)
}

// IsLoaded reports whether the model is currently loaded.
func (g *GemmaModel) IsLoaded() bool {
	return g.loaded.Load()
}

// ClassifyBatch classifies a batch of trace groups. When the model is
// not loaded it returns ErrModelNotLoaded. When loaded in stub mode
// (no real llama.cpp binding) it builds the classification prompt for
// each group and returns flows with confidence=0, using basic trace
// metadata for descriptions. Real model inference is Phase 2.
func (g *GemmaModel) ClassifyBatch(ctx context.Context, groups [][]types.Trace) ([]types.Flow, error) {
	if !g.IsLoaded() {
		return nil, types.ErrModelNotLoaded{}
	}

	select {
	case <-ctx.Done():
		return nil, types.ErrContextCancelled{}
	default:
	}

	start := time.Now()

	// Build the prompt — in production this would be sent to the model.
	// In stub mode we use it to derive basic descriptions.
	_ = g.buildClassificationPrompt(groups)

	flows := make([]types.Flow, 0, len(groups))
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		flow := makeUnknownFlow("", group)
		flow.ID = uuid.Must(uuid.NewV7()).String()
		flow.Intent = "unknown"
		flow.Phase = types.FlowPhaseAction
		flow.Outcome = types.FlowOutcomeUnknown
		flow.Confidence = 0
		flow.Description = buildGroupDescription(group)
		flows = append(flows, flow)
	}

	g.updateAvgLatency(time.Since(start))

	return flows, nil
}

// buildClassificationPrompt constructs the Gemma prompt for a batch of
// trace groups, following the S03 spec §2.3 prompt template.
func (g *GemmaModel) buildClassificationPrompt(groups [][]types.Trace) string {
	var b strings.Builder

	b.WriteString("<start_of_turn>user\n")
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

	b.WriteString("<end_of_turn>\n")
	b.WriteString("<start_of_turn>model\n")

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
		info.MemoryMB = 0 // stub uses no resident memory
	}
	return info
}

// Health reports whether the model is operational for classification.
// It returns nil when the model is loaded, or an error otherwise.
func (g *GemmaModel) Health(_ context.Context) error {
	if !g.IsLoaded() {
		return types.ErrModelNotLoaded{}
	}
	return nil
}

// Close unloads the model and releases resources.
func (g *GemmaModel) Close() error {
	g.Unload()
	return nil
}

// updateAvgLatency updates the running average latency using the model
// mutex for consistency between the count increment and the average
// computation.
func (g *GemmaModel) updateAvgLatency(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := g.metrics.totalInferences.Add(1)
	oldAvg := g.metrics.avgLatency.Load()
	newAvg := (oldAvg*(count-1) + int64(d)) / count
	g.metrics.avgLatency.Store(newAvg)
}

// buildGroupDescription creates a human-readable summary of a trace
// group for use in stub-mode classification where no model output is
// available.
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
