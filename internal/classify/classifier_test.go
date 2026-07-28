package classify

import (
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
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

func TestClassifierHealth(t *testing.T) {
	engine := NewClassificationEngine(nil, nil, nil)
	c := NewClassifier(engine)

	if err := c.Health(t.Context()); err != nil {
		t.Errorf("expected nil error from Health, got %v", err)
	}
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
