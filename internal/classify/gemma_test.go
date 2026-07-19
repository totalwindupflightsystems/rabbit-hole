package classify

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// --- TestGemmaModelLoad ---

func TestGemmaModelLoad(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

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

// --- TestGemmaModelUnload ---

func TestGemmaModelUnload(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")

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

	// Verify the prompt has the key structural elements from S03 spec §2.3
	requiredSubstrings := []string{
		"<start_of_turn>user",
		"<end_of_turn>",
		"<start_of_turn>model",
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

// --- TestGemmaModelClassifyBatchStub ---

func TestGemmaModelClassifyBatchStub(t *testing.T) {
	m := NewGemmaModel("/fake/path.gguf", "gemma-3-4b", "")
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
		t.Fatalf("ClassifyBatch failed: %v", err)
	}

	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}

	// Stub mode: confidence should be 0
	if flows[0].Confidence != 0 {
		t.Errorf("expected confidence 0 in stub mode, got %.2f", flows[0].Confidence)
	}
	if flows[0].Intent != "unknown" {
		t.Errorf("expected intent unknown in stub mode, got %s", flows[0].Intent)
	}
	if flows[0].Description == "" {
		t.Error("expected non-empty description in stub mode")
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
				_ = m.Load(t.Context())
			} else {
				m.Unload()
			}
			_ = m.IsLoaded()
			_ = m.ModelInfo()
		}(i)
	}

	wg.Wait()
	// No race detector failure = pass. Set final state.
	_ = m.Load(t.Context())
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

	_ = m.Load(t.Context())
	info = m.ModelInfo()
	if !m.IsLoaded() {
		t.Fatal("expected model to be loaded")
	}
	if !info.Ready {
		t.Error("expected Ready=true after load")
	}
	if info.LoadedAt.IsZero() {
		t.Error("expected non-zero LoadedAt after load")
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
	// Verify we can round-trip the JSON shape the model is expected to produce
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

// errorsAs is a tiny helper to avoid importing errors directly in tests
// (keeps the test file clean and consistent with the existing patterns_test.go).
func errorsAs(err error, target any) bool {
	if err == nil {
		return false
	}
	// Use standard errors.As semantics via type assertion on the concrete types.
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
