package classify

import (
	"context"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

const throughputTraceCount = 1000

// mockClassificationBackend simulates a model backend with fixed inference
// latency and one classified flow per unmatched trace group.
type mockClassificationBackend struct{}

var _ ClassificationBackend = (*mockClassificationBackend)(nil)

func (*mockClassificationBackend) Health(context.Context) error {
	return nil
}

func (*mockClassificationBackend) Classify(_ context.Context, groups [][]types.Trace) ([]types.Flow, error) {
	time.Sleep(10 * time.Millisecond)

	flows := make([]types.Flow, len(groups))
	for i, group := range groups {
		flow := types.Flow{
			ID:          "mock-flow",
			TraceIDs:    extractTraceIDs(group),
			Intent:      "mock_classification",
			Phase:       types.FlowPhaseAction,
			Outcome:     types.FlowOutcomeSuccess,
			Confidence:  0.7 + float64(i%3)*0.1,
			Description: "Classified by benchmark mock backend",
		}
		if len(group) > 0 {
			flow.StartTime = group[0].Timestamp
			flow.EndTime = group[len(group)-1].Timestamp
			flow.Duration = flow.EndTime.Sub(flow.StartTime)
		}
		flows[i] = flow
	}
	return flows, nil
}

func (*mockClassificationBackend) Info(context.Context) (ModelInfo, error) {
	// ModelInfo calls the backend/provider discriminator Kind in this revision.
	return ModelInfo{Name: "mock", Kind: "bench", Version: "1.0", Ready: true}, nil
}

func (*mockClassificationBackend) Status(context.Context) (string, string) {
	return "ok — mock backend", ""
}

func (*mockClassificationBackend) Close() error {
	return nil
}

func makeThroughputTraces(syscalls []string, gap time.Duration) []types.Trace {
	traces := make([]types.Trace, throughputTraceCount)
	base := time.Unix(0, 0)
	for i := range traces {
		syscall := syscalls[i%len(syscalls)]
		trace := types.Trace{
			Timestamp:   base.Add(time.Duration(i) * gap),
			PID:         12345,
			Syscall:     syscall,
			Category:    types.TraceCategorySyscall,
			ReturnValue: 0,
		}
		switch syscall {
		case "execve":
			trace.Args = []string{"/usr/bin/true"}
		case "connect":
			trace.Args = []string{"127.0.0.1:443"}
		}
		traces[i] = trace
	}
	return traces
}

func BenchmarkClassificationEngine_Classify(b *testing.B) {
	eng := NewClassificationEngine(nil, nil, nil)

	// Generate 100 synthetic traces — varied syscalls, 1ms apart.
	traces := make([]types.Trace, 100)
	base := time.Now()
	for i := range traces {
		syscall := "read"
		if i%3 == 0 {
			syscall = "write"
		} else if i%5 == 0 {
			syscall = "open"
		}
		traces[i] = types.Trace{
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			PID:       12345,
			Syscall:   syscall,
			Category:  types.TraceCategorySyscall,
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.Classify(b.Context(), "bench-session", traces)
	}
}

func BenchmarkClassificationEngine_Throughput(b *testing.B) {
	eng := NewClassificationEngine(nil, nil, nil)
	traces := makeThroughputTraces(
		[]string{"read", "write", "open", "execve", "socket", "connect"},
		time.Millisecond,
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.Classify(b.Context(), "bench-session", traces)
	}
	b.ReportMetric(float64(b.N*1000)/b.Elapsed().Seconds(), "traces/sec")
}

func BenchmarkClassificationEngine_Throughput_WithModel(b *testing.B) {
	eng := NewClassificationEngine(&mockClassificationBackend{}, nil, nil)
	// Keep every trace in a separate, non-pattern-matching group so the full
	// batch is sent through the backend on every benchmark iteration.
	traces := makeThroughputTraces(
		[]string{"futex", "mmap", "munmap", "ioctl", "poll"},
		200*time.Millisecond,
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.Classify(b.Context(), "bench-session", traces)
	}
	b.ReportMetric(float64(b.N*1000)/b.Elapsed().Seconds(), "traces/sec")
}

func BenchmarkClassificationEngine_Throughput_PatternAndModel(b *testing.B) {
	eng := NewClassificationEngine(&mockClassificationBackend{}, nil, nil)
	// Isolated execve groups hit the pattern fast path while isolated futex
	// groups miss the catalog and fall back to the mock model backend.
	traces := makeThroughputTraces(
		[]string{"execve", "futex"},
		200*time.Millisecond,
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.Classify(b.Context(), "bench-session", traces)
	}
	b.ReportMetric(float64(b.N*1000)/b.Elapsed().Seconds(), "traces/sec")
}

func BenchmarkGroupTracesByTime(b *testing.B) {
	eng := NewClassificationEngine(nil, nil, nil)

	// Generate 1000 traces with varied gaps and boundary syscalls.
	traces := make([]types.Trace, 1000)
	base := time.Now()
	for i := range traces {
		syscall := "read"
		gap := 1
		switch {
		case i%100 == 0:
			syscall = "execve" // boundary
			gap = 200
		case i%50 == 0:
			syscall = "exit" // boundary
			gap = 150
		case i%7 == 0:
			syscall = "write"
			gap = 5
		}
		traces[i] = types.Trace{
			Timestamp: base.Add(time.Duration(gap*i) * time.Millisecond),
			PID:       12345,
			Syscall:   syscall,
			Category:  types.TraceCategorySyscall,
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.groupTracesByTime(traces)
	}
}
