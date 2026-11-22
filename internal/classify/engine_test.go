package classify

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// countingBackend records how many groups each Classify call received, so a
// test can observe the engine's flush boundaries (DF-RABBIT-HOLE-011).
type countingBackend struct {
	mu     sync.Mutex
	groups []int
}

func (b *countingBackend) Classify(ctx context.Context, groups [][]types.Trace) ([]types.Flow, error) {
	b.mu.Lock()
	b.groups = append(b.groups, len(groups))
	b.mu.Unlock()

	flows := make([]types.Flow, 0, len(groups))
	for _, g := range groups {
		flows = append(flows, makeUnknownFlow("s", g))
	}
	return flows, nil
}

func (b *countingBackend) Health(ctx context.Context) error { return nil }

func (b *countingBackend) Info(ctx context.Context) (ModelInfo, error) {
	return ModelInfo{Name: "counting", Kind: "local"}, nil
}

func (b *countingBackend) Status(ctx context.Context) (string, string) { return "ok", "counting" }

func (b *countingBackend) Close() error { return nil }

func (b *countingBackend) calls() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.groups...)
}

// TestNewClassificationEngine_Defaults pins today's values so an unconfigured
// daemon keeps flushing at 100 traces / 500ms.
func TestNewClassificationEngine_Defaults(t *testing.T) {
	e := NewClassificationEngine(nil, nil, nil)
	if e.batchSize != 100 {
		t.Errorf("batchSize = %d, want 100", e.batchSize)
	}
	if e.batchTimeout != 500*time.Millisecond {
		t.Errorf("batchTimeout = %v, want 500ms", e.batchTimeout)
	}
}

// TestNewClassificationEngine_Options proves RABBITHOLE_BATCH_SIZE /
// RABBITHOLE_BATCH_TIMEOUT reach the engine, and that a non-positive value
// cannot wedge the batching loop back to zero.
func TestNewClassificationEngine_Options(t *testing.T) {
	e := NewClassificationEngine(nil, nil, nil,
		WithBatchSize(7),
		WithBatchTimeout(2*time.Second),
	)
	if e.batchSize != 7 {
		t.Errorf("batchSize = %d, want 7", e.batchSize)
	}
	if e.batchTimeout != 2*time.Second {
		t.Errorf("batchTimeout = %v, want 2s", e.batchTimeout)
	}

	e2 := NewClassificationEngine(nil, nil, nil, WithBatchSize(0), WithBatchTimeout(-time.Second))
	if e2.batchSize != 100 || e2.batchTimeout != 500*time.Millisecond {
		t.Errorf("non-positive options = %d/%v, want the 100/500ms defaults", e2.batchSize, e2.batchTimeout)
	}
}

// TestClassifyStream_BatchSizeDrivesFlushes is the behavioural half: with
// batch size 2 the stream flushes every two traces, with the default it sends
// one batch. Traces are spaced past the grouping gap and use an unpatterned
// syscall so every trace becomes its own unmatched group (and therefore its
// own entry in a backend call).
func TestClassifyStream_BatchSizeDrivesFlushes(t *testing.T) {
	traces := func(n int) []types.Trace {
		base := time.Now()
		out := make([]types.Trace, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, types.Trace{
				ID:        fmt.Sprintf("t%d", i),
				Timestamp: base.Add(time.Duration(i) * 500 * time.Millisecond),
				Category:  types.TraceCategorySyscall,
				Syscall:   "unpatterned_syscall",
			})
		}
		return out
	}

	run := func(t *testing.T, options ...EngineOption) []int {
		t.Helper()
		backend := &countingBackend{}
		engine := NewClassificationEngine(backend, nil, nil, options...)
		in := make(chan types.Trace, 16)
		for _, tr := range traces(4) {
			in <- tr
		}
		close(in)

		out, err := NewClassifier(engine).ClassifyStream(context.Background(), "s", in)
		if err != nil {
			t.Fatalf("ClassifyStream: %v", err)
		}
		for range out {
		}
		return backend.calls()
	}

	defaultCalls := run(t)
	if len(defaultCalls) != 1 {
		t.Errorf("default batch size: backend calls = %v, want 1 flush for 4 traces", defaultCalls)
	}

	smallCalls := run(t, WithBatchSize(2))
	if len(smallCalls) != 2 {
		t.Fatalf("batch size 2: backend calls = %v, want 2 flushes for 4 traces", smallCalls)
	}
	for _, n := range smallCalls {
		if n != 2 {
			t.Errorf("batch size 2: flush carried %d groups, want 2", n)
		}
	}
}
