package classify

import (
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

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
