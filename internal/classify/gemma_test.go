package classify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// newTestGemmaModel creates a GemmaModel pointed at a test Ollama server.
func newTestGemmaModel(serverURL, modelName string) *GemmaModel {
	m := NewGemmaModel("/fake/path.gguf", modelName, "")
	m.SetOllamaURL(serverURL)
	return m
}

// --- TestGemmaModelLoad ---

func TestGemmaModelLoad(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" && r.Method == "POST" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"name": "gemma-3-4b"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "gemma-3-4b")

	if m.IsLoaded() {
		t.Fatal("expected model to not be loaded initially")
	}

	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !m.IsLoaded() {
		t.Fatal("expected model to be loaded after Load")
	}
}

// --- TestGemmaModelLoadNoFile ---

func TestGemmaModelLoadNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "nonexistent-model")
	err := m.Load(t.Context())
	if err == nil {
		t.Fatal("expected error loading nonexistent model, got nil")
	}
	if m.IsLoaded() {
		t.Fatal("expected model to remain unloaded after failed Load")
	}
}

// --- TestGemmaModelLoadEmptyPath ---

func TestGemmaModelLoadEmptyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "")
	err := m.Load(t.Context())
	if err == nil {
		t.Fatal("expected error for empty model name, got nil")
	}
	if m.IsLoaded() {
		t.Fatal("expected model to remain unloaded after failed Load")
	}
}

// --- TestGemmaModelUnload ---

func TestGemmaModelUnload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"name": "gemma-3-4b"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "gemma-3-4b")

	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	m.Unload()

	if m.IsLoaded() {
		t.Fatal("expected model to not be loaded after Unload")
	}
}

// --- TestGemmaModelClassifyNotLoaded ---

func TestGemmaModelClassifyNotLoaded(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	groups := [][]types.Trace{
		makeTimedTraces(traceItem{"read", types.TraceCategorySyscall, 0, nil}),
	}

	_, err := m.ClassifyBatch(t.Context(), groups)
	if err == nil {
		t.Fatal("expected error when classifying without Load")
	}

	var notLoaded types.ErrModelNotLoaded
	if !errorsAs(err, &notLoaded) {
		t.Errorf("expected ErrModelNotLoaded, got %T: %v", err, err)
	}
}

// --- TestGemmaModelClassifyBatchOllama ---

func TestGemmaModelClassifyBatchOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"name": "gemma-3-4b"})
			return
		}
		if r.URL.Path == "/api/generate" {
			var req ollamaGenerateRequest
			json.NewDecoder(r.Body).Decode(&req)
			// Return realistic classification output
			resp := ollamaGenerateResponse{
				Response: `[
					{"intent": "read_file", "phase": "observation", "outcome": "success", "description": "Read source file", "confidence": 0.92},
					{"intent": "api_call", "phase": "action", "outcome": "success", "description": "HTTP API request", "confidence": 0.88}
				]`,
				Done: true,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "gemma-3-4b")
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	groups := [][]types.Trace{
		makeTimedTraces(
			traceItem{"read", types.TraceCategorySyscall, 0, nil},
			traceItem{"read", types.TraceCategorySyscall, 1, nil},
		),
		makeTimedTraces(
			traceItem{"connect", types.TraceCategoryNetwork, 10, nil},
		),
	}

	flows, err := m.ClassifyBatch(t.Context(), groups)
	if err != nil {
		t.Fatalf("ClassifyBatch failed: %v", err)
	}

	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}

	if flows[0].Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", flows[0].Intent)
	}
	if flows[0].Confidence != 0.92 {
		t.Errorf("expected confidence 0.92, got %.2f", flows[0].Confidence)
	}
	if flows[1].Intent != "api_call" {
		t.Errorf("expected intent api_call, got %s", flows[1].Intent)
	}
}

// --- TestGemmaModelClassifyBatchDegradation ---

func TestGemmaModelClassifyBatchDegradation(t *testing.T) {
	// When Ollama returns malformed JSON, all groups become unknown flows.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"name": "gemma-3-4b"})
			return
		}
		if r.URL.Path == "/api/generate" {
			resp := ollamaGenerateResponse{
				Response: "this is not valid JSON",
				Done:     true,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "gemma-3-4b")
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	groups := [][]types.Trace{
		makeTimedTraces(
			traceItem{"read", types.TraceCategorySyscall, 0, nil},
			traceItem{"read", types.TraceCategorySyscall, 1, nil},
		),
	}

	flows, err := m.ClassifyBatch(t.Context(), groups)
	if err != nil {
		t.Fatalf("ClassifyBatch should not error on parse failure: %v", err)
	}

	if len(flows) != 1 {
		t.Fatalf("expected 1 degradation flow, got %d", len(flows))
	}

	if flows[0].Confidence != 0 {
		t.Errorf("expected confidence 0 in degradation, got %.2f", flows[0].Confidence)
	}
	if flows[0].Intent != "unknown" {
		t.Errorf("expected intent unknown in degradation, got %s", flows[0].Intent)
	}
}

// --- TestGemmaModelBuildPrompt ---

func TestGemmaModelBuildPrompt(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	groups := [][]types.Trace{
		makeTimedTraces(
			traceItem{"openat", types.TraceCategorySyscall, 0, []string{"openat", "main.go"}},
			traceItem{"read", types.TraceCategorySyscall, 1, nil},
			traceItem{"close", types.TraceCategorySyscall, 2, nil},
		),
	}

	prompt := m.buildClassificationPrompt(groups)

	// Verify the prompt has the key structural elements
	requiredSubstrings := []string{
		"classification system",
		"Group 0",
		"openat",
		"main.go",
		"intent",
		"phase",
		"outcome",
		"confidence",
	}

	for _, s := range requiredSubstrings {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing expected substring %q\nPrompt:\n%s", s, prompt)
		}
	}
}

// --- TestGemmaModelParseOutput ---

func TestGemmaModelParseOutput(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	groups := [][]types.Trace{
		makeTimedTraces(
			traceItem{"openat", types.TraceCategorySyscall, 0, []string{"openat", "f.go"}},
			traceItem{"read", types.TraceCategorySyscall, 1, nil},
		),
		makeTimedTraces(
			traceItem{"connect", types.TraceCategoryNetwork, 10, nil},
		),
	}

	output := `[
		{"intent": "read_file", "phase": "observation", "outcome": "success", "description": "Read f.go", "confidence": 0.92},
		{"intent": "api_call", "phase": "action", "outcome": "success", "description": "API call", "confidence": 0.88}
	]`

	flows, err := m.parseClassificationOutput(groups, output)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}

	if flows[0].Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", flows[0].Intent)
	}
	if flows[0].Confidence != 0.92 {
		t.Errorf("expected confidence 0.92, got %.2f", flows[0].Confidence)
	}
	if flows[0].Phase != types.FlowPhaseObservation {
		t.Errorf("expected phase observation, got %s", flows[0].Phase)
	}

	if flows[1].Intent != "api_call" {
		t.Errorf("expected intent api_call, got %s", flows[1].Intent)
	}
}

// --- TestGemmaModelParseInvalidJSON ---

func TestGemmaModelParseInvalidJSON(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	groups := [][]types.Trace{
		makeTimedTraces(traceItem{"read", types.TraceCategorySyscall, 0, nil}),
	}

	_, err := m.parseClassificationOutput(groups, "this is not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	var parseErr types.ErrParseFailure
	if !errorsAs(err, &parseErr) {
		t.Errorf("expected ErrParseFailure, got %T: %v", err, err)
	}
}

// --- TestGemmaModelParseCountMismatch ---

func TestGemmaModelParseCountMismatch(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	groups := [][]types.Trace{
		makeTimedTraces(traceItem{"read", types.TraceCategorySyscall, 0, nil}),
	}

	// Valid JSON but wrong number of results
	output := `[{"intent": "a", "phase": "action", "outcome": "success", "description": "x", "confidence": 0.5},
	            {"intent": "b", "phase": "action", "outcome": "success", "description": "y", "confidence": 0.5}]`

	_, err := m.parseClassificationOutput(groups, output)
	if err == nil {
		t.Fatal("expected error for count mismatch")
	}
}

// --- TestGemmaModelConcurrent ---

func TestGemmaModelConcurrent(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	var wg sync.WaitGroup
	const goroutines = 50

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				m.Unload()
			} else {
				_ = m.IsLoaded()
				_ = m.ModelInfo()
			}
		}(i)
	}

	wg.Wait()
	// No race detector failure = pass.
}

// --- TestGemmaModelModelInfo ---

func TestGemmaModelModelInfo(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

	info := m.ModelInfo()
	if info.Name != "gemma-3-4b" {
		t.Errorf("expected name gemma-3-4b, got %s", info.Name)
	}
	if info.DeviceType != "cpu" {
		t.Errorf("expected device cpu, got %s", info.DeviceType)
	}
	if info.Version != "1.0.0-dev" {
		t.Errorf("expected version 1.0.0-dev, got %s", info.Version)
	}
	if info.Kind != "local" {
		t.Errorf("expected kind local, got %s", info.Kind)
	}
	if info.Ready {
		t.Error("expected Ready=false when not loaded")
	}
	if !info.LoadedAt.IsZero() {
		t.Error("expected zero LoadedAt when not loaded")
	}
}

// --- TestGemmaModelDefaultVersion ---

func TestGemmaModelDefaultVersion(t *testing.T) {
	m := NewGemmaModel("/anywhere.gguf", "m", "")
	if m.version != "1.0.0-dev" {
		t.Errorf("expected default version 1.0.0-dev, got %q", m.version)
	}

	m2 := NewGemmaModel("/anywhere.gguf", "m", "2.0.0")
	if m2.version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %q", m2.version)
	}
}

// --- TestMakeUnknownFlow ---

func TestMakeUnknownFlow(t *testing.T) {
	traces := makeTimedTraces(
		traceItem{"read", types.TraceCategorySyscall, 0, nil},
		traceItem{"write", types.TraceCategorySyscall, 5, nil},
	)

	flow := makeUnknownFlow("sess-1", traces)

	if flow.SessionID != "sess-1" {
		t.Errorf("expected session sess-1, got %s", flow.SessionID)
	}
	if flow.Intent != "unknown" {
		t.Errorf("expected intent unknown, got %s", flow.Intent)
	}
	if flow.Confidence != 0 {
		t.Errorf("expected confidence 0, got %.2f", flow.Confidence)
	}
	if flow.Outcome != types.FlowOutcomeUnknown {
		t.Errorf("expected outcome unknown, got %s", flow.Outcome)
	}
	if flow.ID == "" {
		t.Error("expected non-empty flow ID")
	}
	if len(flow.TraceIDs) != 2 {
		t.Errorf("expected 2 trace IDs, got %d", len(flow.TraceIDs))
	}
}

// --- TestGemmaModelParseValidJSONFromModel ---

func TestGemmaModelParseValidJSONFromModel(t *testing.T) {
	result := []classificationResult{
		{Intent: "test", Phase: "verification", Outcome: "success", Description: "ran tests", Confidence: 0.9},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")
	groups := [][]types.Trace{
		makeTimedTraces(traceItem{"execve", types.TraceCategorySyscall, 0, nil}),
	}

	flows, err := m.parseClassificationOutput(groups, string(data))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	if flows[0].Description != "ran tests" {
		t.Errorf("expected description 'ran tests', got %s", flows[0].Description)
	}
}

// --- TestGemmaModelSetOllamaURL ---

func TestGemmaModelSetOllamaURL(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")
	if m.ollamaURL != defaultOllamaURL {
		t.Errorf("expected default URL %q, got %q", defaultOllamaURL, m.ollamaURL)
	}
	m.SetOllamaURL("http://custom:9999")
	if m.ollamaURL != "http://custom:9999" {
		t.Errorf("expected custom URL, got %q", m.ollamaURL)
	}
}

// errorsAs is a tiny helper to avoid importing errors directly in tests
func errorsAs(err error, target any) bool {
	if err == nil {
		return false
	}
	switch t := target.(type) {
	case *types.ErrModelNotLoaded:
		_, ok := err.(types.ErrModelNotLoaded)
		return ok
	case *types.ErrParseFailure:
		_, ok := err.(types.ErrParseFailure)
		return ok
	default:
		_ = t
		return false
	}
}

// Ensure time import is used even if tests evolve.
var _ = time.Millisecond
