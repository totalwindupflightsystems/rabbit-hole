package classify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// --- helper: make traces with explicit timestamps ---

func makeTimedTraces(items ...struct {
	Syscall  string
	Category types.TraceCategory
	OffsetMs int64
	Args     []string
}) []types.Trace {
	base := time.Now()
	traces := make([]types.Trace, len(items))
	for i, item := range items {
		traces[i] = types.Trace{
			ID:        traceID(i),
			Timestamp: base.Add(time.Duration(item.OffsetMs) * time.Millisecond),
			Category:  item.Category,
			Syscall:   item.Syscall,
			Args:      item.Args,
		}
	}
	return traces
}

type traceItem struct {
	Syscall  string
	Category types.TraceCategory
	OffsetMs int64
	Args     []string
}

func traceID(i int) string {
	return "trace-" + string(rune('a'+i))
}

// --- TestGroupTracesByTime: gap detection ---

func TestGroupTracesByTime(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)

	t.Run("50ms gap stays in same group", func(t *testing.T) {
		traces := makeTimedTraces(
			traceItem{"read", types.TraceCategorySyscall, 0, nil},
			traceItem{"read", types.TraceCategorySyscall, 50, nil},
			traceItem{"close", types.TraceCategorySyscall, 100, nil},
		)
		groups := engine.groupTracesByTime(traces)
		if len(groups) != 1 {
			t.Fatalf("expected 1 group for 50ms gaps, got %d", len(groups))
		}
		if len(groups[0]) != 3 {
			t.Errorf("expected 3 traces in group, got %d", len(groups[0]))
		}
	})

	t.Run("150ms gap creates new group", func(t *testing.T) {
		traces := makeTimedTraces(
			traceItem{"read", types.TraceCategorySyscall, 0, nil},
			traceItem{"read", types.TraceCategorySyscall, 150, nil},
		)
		groups := engine.groupTracesByTime(traces)
		if len(groups) != 2 {
			t.Fatalf("expected 2 groups for 150ms gap, got %d", len(groups))
		}
	})

	t.Run("category change creates new group", func(t *testing.T) {
		traces := makeTimedTraces(
			traceItem{"read", types.TraceCategorySyscall, 0, nil},
			traceItem{"connect", types.TraceCategoryNetwork, 10, nil},
		)
		groups := engine.groupTracesByTime(traces)
		if len(groups) != 2 {
			t.Fatalf("expected 2 groups for category change, got %d", len(groups))
		}
	})

	t.Run("boundary syscall creates new group", func(t *testing.T) {
		traces := makeTimedTraces(
			traceItem{"read", types.TraceCategorySyscall, 0, nil},
			traceItem{"execve", types.TraceCategorySyscall, 10, nil},
			traceItem{"read", types.TraceCategorySyscall, 20, nil},
		)
		groups := engine.groupTracesByTime(traces)
		if len(groups) < 2 {
			t.Fatalf("expected >=2 groups for boundary syscall, got %d", len(groups))
		}
	})
}

// --- TestGroupTracesByTimeEmpty ---

func TestGroupTracesByTimeEmpty(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)

	groups := engine.groupTracesByTime(nil)
	if groups != nil {
		t.Errorf("expected nil groups for nil input, got %v", groups)
	}

	groups = engine.groupTracesByTime([]types.Trace{})
	if groups != nil {
		t.Errorf("expected nil groups for empty input, got %v", groups)
	}
}

// --- TestClassificationEngineClassify ---

func TestClassificationEngineClassify(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)

	traces := makeTimedTraces(
		traceItem{"openat", types.TraceCategorySyscall, 0, []string{"openat", "file.go"}},
		traceItem{"read", types.TraceCategorySyscall, 1, []string{"read"}},
		traceItem{"read", types.TraceCategorySyscall, 2, []string{"read"}},
		traceItem{"close", types.TraceCategorySyscall, 3, []string{"close"}},
	)

	flows, err := engine.Classify(t.Context(), "session-1", traces)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(flows) == 0 {
		t.Fatal("expected at least one flow")
	}

	flow := flows[0]
	if flow.Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", flow.Intent)
	}
	if flow.SessionID != "session-1" {
		t.Errorf("expected session-1, got %s", flow.SessionID)
	}
	if flow.Confidence < 0.90 {
		t.Errorf("expected high confidence pattern match, got %.2f", flow.Confidence)
	}
}

// --- TestClassificationEngineClassifyEmpty ---

func TestClassificationEngineClassifyEmpty(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)

	flows, err := engine.Classify(t.Context(), "session-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flows != nil {
		t.Errorf("expected nil flows for empty input, got %v", flows)
	}

	flows, err = engine.Classify(t.Context(), "session-1", []types.Trace{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flows != nil {
		t.Errorf("expected nil flows for empty slice, got %v", flows)
	}
}

// --- TestClassificationEngineClassifyUnknown ---

func TestClassificationEngineClassifyUnknown(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)

	// Traces that don't match any pattern — just read/write without
	// openat or close framing.
	traces := makeTimedTraces(
		traceItem{"read", types.TraceCategorySyscall, 0, nil},
		traceItem{"read", types.TraceCategorySyscall, 1, nil},
	)

	flows, err := engine.Classify(t.Context(), "session-1", traces)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(flows) == 0 {
		t.Fatal("expected at least one flow for unmatched traces")
	}

	flow := flows[0]
	if flow.Confidence != 0 {
		t.Errorf("expected confidence 0 for unknown flow, got %.2f", flow.Confidence)
	}
	if flow.Intent != "unknown" {
		t.Errorf("expected intent unknown, got %s", flow.Intent)
	}
	if flow.Outcome != types.FlowOutcomeUnknown {
		t.Errorf("expected outcome unknown, got %s", flow.Outcome)
	}
}

// --- TestClassifierHealth ---

// TestClassifierHealth verifies Health reflects the EFFECTIVE classifier
// state (DF-022): pattern-only (no backend or unloaded model) is NOT
// healthy; only a loaded model backend is.
func TestClassifierHealth(t *testing.T) {
	// No backend: pattern-only mode must not report as operational.
	engine := NewClassificationEngine(nil, nil, nil)
	c := NewClassifier(engine)

	if err := c.Health(t.Context()); err == nil {
		t.Error("expected error from Health with no backend (pattern-only), got nil")
	}

	// Local backend with an unloaded model — the zero-config default
	// where Load() failed because no Ollama is reachable.
	model := NewGemmaModel("/fake/path", "gemma-3-4b", "")
	engine2 := NewClassificationEngine(NewLocalBackend(model), nil, nil)
	c2 := NewClassifier(engine2)

	if err := c2.Health(t.Context()); err == nil {
		t.Error("expected error from Health with unloaded model, got nil")
	}

	// Local backend with a loaded model is operational.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"name": "gemma-3-4b"})
	}))
	defer srv.Close()

	m := newTestGemmaModel(srv.URL, "gemma-3-4b")
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("test model Load failed: %v", err)
	}
	engine3 := NewClassificationEngine(NewLocalBackend(m), nil, nil)
	c3 := NewClassifier(engine3)

	if err := c3.Health(t.Context()); err != nil {
		t.Errorf("expected nil error from Health with loaded model, got %v", err)
	}
}

// --- TestClassifierStatus ---

// TestClassifierStatus verifies the classifier's effective backend mode
// is reported truthfully for /health (DF-022): never "ok / local model
// backend" while no model is actually loaded.
func TestClassifierStatus(t *testing.T) {
	t.Run("no backend — pattern only", func(t *testing.T) {
		c := NewClassifier(NewClassificationEngine(nil, nil, nil))
		status, detail := c.Status(t.Context())
		if status != "degraded — pattern-only (no model loaded)" {
			t.Errorf("status: got %q, want %q", status, "degraded — pattern-only (no model loaded)")
		}
		if detail != "" {
			t.Errorf("detail: got %q, want empty", detail)
		}
	})

	t.Run("unloaded local model — pattern only", func(t *testing.T) {
		model := NewGemmaModel("/fake/path", "gemma-3-4b", "")
		c := NewClassifier(NewClassificationEngine(NewLocalBackend(model), nil, nil))
		status, detail := c.Status(t.Context())
		if status != "degraded — pattern-only (model not loaded)" {
			t.Errorf("status: got %q, want %q", status, "degraded — pattern-only (model not loaded)")
		}
		if detail != "" {
			t.Errorf("detail: got %q, want empty", detail)
		}
	})

	t.Run("load failure — pattern only with failure detail", func(t *testing.T) {
		// Ollama returns 404 for the model: Load fails, LoadError is set.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		model := newTestGemmaModel(srv.URL, "nonexistent-model")
		if err := model.Load(t.Context()); err == nil {
			t.Fatal("expected Load to fail")
		}
		c := NewClassifier(NewClassificationEngine(NewLocalBackend(model), nil, nil))
		status, detail := c.Status(t.Context())
		if status != "degraded — pattern-only (model not loaded)" {
			t.Errorf("status: got %q, want %q", status, "degraded — pattern-only (model not loaded)")
		}
		if !strings.Contains(detail, "gemma load failed") {
			t.Errorf("detail: got %q, want it to contain the load failure", detail)
		}
	})

	t.Run("loaded local model — gemma via ollama", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"name": "gemma-3-4b"})
		}))
		defer srv.Close()

		model := newTestGemmaModel(srv.URL, "gemma-3-4b")
		if err := model.Load(t.Context()); err != nil {
			t.Fatalf("test model Load failed: %v", err)
		}
		c := NewClassifier(NewClassificationEngine(NewLocalBackend(model), nil, nil))
		status, detail := c.Status(t.Context())
		if want := "ok — gemma via ollama " + srv.URL; status != want {
			t.Errorf("status: got %q, want %q", status, want)
		}
		if !strings.Contains(detail, "gemma-3-4b loaded") {
			t.Errorf("detail: got %q, want it to mention the loaded model", detail)
		}
	})
}

// --- TestClassifierModelInfo ---

func TestClassifierModelInfo(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)
	c := NewClassifier(engine)

	info, err := c.ModelInfo(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "none" {
		t.Errorf("expected model name 'none' when no model configured, got %s", info.Name)
	}

	// With a model configured (not loaded).
	model := NewGemmaModel("/fake/path", "gemma-3-4b", "")
	engine2 := NewClassificationEngine(NewLocalBackend(model), nil, nil)
	c2 := NewClassifier(engine2)

	info2, err := c2.ModelInfo(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info2.Name != "gemma-3-4b" {
		t.Errorf("expected model name gemma-3-4b, got %s", info2.Name)
	}
}

// --- TestClassifierClassifyStream ---

func TestClassifierClassifyStream(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)
	c := NewClassifier(engine)

	in := make(chan types.Trace, 10)
	base := time.Now()

	// Send a pattern-matched group
	in <- types.Trace{ID: "t0", Timestamp: base, Category: types.TraceCategorySyscall, Syscall: "openat", Args: []string{"openat", "f.go"}}
	in <- types.Trace{ID: "t1", Timestamp: base.Add(time.Millisecond), Category: types.TraceCategorySyscall, Syscall: "read"}
	in <- types.Trace{ID: "t2", Timestamp: base.Add(2 * time.Millisecond), Category: types.TraceCategorySyscall, Syscall: "close"}
	close(in)

	out, err := c.ClassifyStream(t.Context(), "session-stream", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var flows []types.Flow
	for f := range out {
		flows = append(flows, f)
	}

	if len(flows) == 0 {
		t.Fatal("expected at least one flow from stream")
	}
	if flows[0].Intent != "read_file" {
		t.Errorf("expected read_file, got %s", flows[0].Intent)
	}
}

// --- TestIsBoundarySyscall ---

func TestIsBoundarySyscall(t *testing.T) {
	boundaries := []string{"execve", "exit", "exit_group", "fork", "vfork", "clone"}
	for _, s := range boundaries {
		if !isBoundarySyscall(s) {
			t.Errorf("expected %s to be a boundary syscall", s)
		}
	}

	nonBoundaries := []string{"read", "write", "openat", "close", "connect"}
	for _, s := range nonBoundaries {
		if isBoundarySyscall(s) {
			t.Errorf("expected %s to NOT be a boundary syscall", s)
		}
	}
}
