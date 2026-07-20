package classify

import (
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func makeTraces(syscalls ...string) []types.Trace {
	traces := make([]types.Trace, len(syscalls))
	for i, s := range syscalls {
		traces[i] = types.Trace{
			ID:          "trace-" + string(rune('0'+i)),
			Timestamp:   time.Now().Add(time.Duration(i) * time.Millisecond),
			Category:    types.TraceCategorySyscall,
			Syscall:     s,
			Args:        []string{s, "arg1"},
			ReturnValue: 0,
			Duration:    time.Microsecond,
		}
	}
	return traces
}

func makeTracesWithRC(syscallsAndRC ...any) []types.Trace {
	traces := make([]types.Trace, 0, len(syscallsAndRC)/2)
	for i := 0; i < len(syscallsAndRC); i += 2 {
		syscall := syscallsAndRC[i].(string)
		rc := syscallsAndRC[i+1].(int64)
		traces = append(traces, types.Trace{
			ID:          "trace-" + string(rune('0'+i/2)),
			Timestamp:   time.Now().Add(time.Duration(i/2) * time.Millisecond),
			Category:    types.TraceCategorySyscall,
			Syscall:     syscall,
			Args:        []string{syscall},
			ReturnValue: rc,
			Duration:    time.Microsecond,
		})
	}
	return traces
}

// --- file_read pattern ---

func TestPatternFileReadMatch(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTraces("openat", "read", "read", "close")
	// openat succeeded (ReturnValue >= 0 is default), all reads, ends in close
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected file_read pattern to match")
	}
	if flow.Intent != "read_file" {
		t.Errorf("expected intent read_file, got %s", flow.Intent)
	}
	if flow.Phase != types.FlowPhaseObservation {
		t.Errorf("expected phase observation, got %s", flow.Phase)
	}
	if flow.Outcome != types.FlowOutcomeSuccess {
		t.Errorf("expected outcome success, got %s", flow.Outcome)
	}
	if flow.Confidence < 0.90 {
		t.Errorf("expected high confidence, got %.2f", flow.Confidence)
	}
}

func TestPatternFileReadRejectSingleTrace(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTraces("openat")
	_, ok := pc.Match(traces)
	if ok {
		t.Fatal("expected single-trace group to be rejected")
	}
}

func TestPatternFileReadRejectNoReads(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTraces("openat", "write", "write", "close")
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected write-heavy group to match file_write, not file_read")
	}
	if flow.Intent == "read_file" {
		t.Fatal("expected file_read to reject write-only group")
	}
	if flow.Intent != "write_file" {
		t.Errorf("expected write_file match, got %s", flow.Intent)
	}
}

func TestPatternFileReadRejectFailedOpen(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTracesWithRC("openat", int64(-1), "read", int64(0), "close", int64(0))
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected failed-syscall pattern to match")
	}
	if flow.Intent == "read_file" {
		t.Fatal("expected file_read to reject failed openat")
	}
	if flow.Outcome != types.FlowOutcomeFailure {
		t.Errorf("expected failure outcome from failed_syscall, got %s", flow.Outcome)
	}
}

// --- file_write pattern ---

func TestPatternFileWriteMatch(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTraces("openat", "write", "write", "close")
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected file_write pattern to match")
	}
	if flow.Intent != "write_file" {
		t.Errorf("expected intent write_file, got %s", flow.Intent)
	}
	if flow.Phase != types.FlowPhaseAction {
		t.Errorf("expected phase action, got %s", flow.Phase)
	}
}

func TestPatternFileWriteWithRename(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTraces("openat", "write", "rename")
	_, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected file_write to match with rename close")
	}
}

func TestPatternFileWriteRejectNoWrite(t *testing.T) {
	pc := NewPatternCatalog()
	// openat + close but no write and too few traces for file_read (needs ≥3)
	traces := makeTraces("openat", "close")
	_, ok := pc.Match(traces)
	if ok {
		t.Fatal("expected no pattern to match openat+close alone (too few traces)")
	}
}

// --- file_edit pattern ---

func TestPatternFileEditMatch(t *testing.T) {
	pc := NewPatternCatalog()
	traces := []types.Trace{
		{ID: "0", Timestamp: time.Now(), Syscall: "openat", Args: []string{"openat", "file.go"}, Category: types.TraceCategorySyscall, ReturnValue: 0},
		{ID: "1", Timestamp: time.Now(), Syscall: "read", Category: types.TraceCategorySyscall, ReturnValue: 100},
		{ID: "2", Timestamp: time.Now(), Syscall: "write", Category: types.TraceCategorySyscall, ReturnValue: 50},
	}
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected file_edit pattern to match")
	}
	if flow.Intent != "edit_file" {
		t.Errorf("expected intent edit_file, got %s", flow.Intent)
	}
}

// --- network_connect pattern ---

func TestPatternNetworkConnectMatch(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTracesWithRC("socket", int64(3), "connect", int64(0))
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected network_connect pattern to match")
	}
	if flow.Intent != "api_call" {
		t.Errorf("expected intent api_call, got %s", flow.Intent)
	}
}

func TestPatternNetworkConnectRejectFailedConnect(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTracesWithRC("connect", int64(-1))
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected failed_syscall pattern to match failed connect")
	}
	if flow.Intent == "api_call" {
		t.Fatal("expected network_connect to reject failed connect")
	}
	if flow.Outcome != types.FlowOutcomeFailure {
		t.Errorf("expected failure outcome, got %s", flow.Outcome)
	}
}

// --- process_exec pattern ---

func TestPatternProcessExecMatch(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTraces("execve")
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected process_exec pattern to match")
	}
	if flow.Intent != "exec_command" {
		t.Errorf("expected intent exec_command, got %s", flow.Intent)
	}
}

// --- failed_syscall pattern ---

func TestPatternFailedSyscall(t *testing.T) {
	pc := NewPatternCatalog()
	traces := makeTracesWithRC("openat", int64(-2)) // ENOENT
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected failed_syscall pattern to match")
	}
	if flow.Intent != "unknown" {
		t.Errorf("expected intent unknown, got %s", flow.Intent)
	}
	if flow.Outcome != types.FlowOutcomeFailure {
		t.Errorf("expected outcome failure, got %s", flow.Outcome)
	}
}

// --- git_operation pattern ---

func TestPatternGitOperation(t *testing.T) {
	pc := NewPatternCatalog()
	traces := []types.Trace{
		{ID: "0", Timestamp: time.Now(), Syscall: "execve", Args: []string{"execve", "git", "commit", "-m", "msg"}, Category: types.TraceCategorySyscall},
	}
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected git_operation pattern to match")
	}
	if flow.Intent != "git_operation" {
		t.Errorf("expected intent git_operation, got %s", flow.Intent)
	}
}

// --- test_run pattern ---

func TestPatternTestRun(t *testing.T) {
	pc := NewPatternCatalog()
	traces := []types.Trace{
		{ID: "0", Timestamp: time.Now(), Syscall: "execve", Args: []string{"execve", "go", "test", "./..."}, Category: types.TraceCategorySyscall},
	}
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected test_run pattern to match go test")
	}
	if flow.Intent != "test_run" {
		t.Errorf("expected intent test_run, got %s", flow.Intent)
	}
}

// --- build_command pattern ---

func TestPatternBuildCommand(t *testing.T) {
	pc := NewPatternCatalog()
	traces := []types.Trace{
		{ID: "0", Timestamp: time.Now(), Syscall: "execve", Args: []string{"execve", "go", "build"}, Category: types.TraceCategorySyscall},
	}
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected build_command pattern to match go build")
	}
	if flow.Intent != "build_command" {
		t.Errorf("expected intent build_command, got %s", flow.Intent)
	}
}

// --- search_code pattern ---

func TestPatternSearchCode(t *testing.T) {
	pc := NewPatternCatalog()
	traces := []types.Trace{
		{ID: "0", Timestamp: time.Now(), Syscall: "execve", Args: []string{"execve", "rg", "function"}, Category: types.TraceCategorySyscall},
	}
	flow, ok := pc.Match(traces)
	if !ok {
		t.Fatal("expected search_code pattern to match")
	}
	if flow.Intent != "search_code" {
		t.Errorf("expected intent search_code, got %s", flow.Intent)
	}
}

// --- multiple patterns in sequence ---

func TestPatternCatalogMultiplePatterns(t *testing.T) {
	pc := NewPatternCatalog()

	// File read sequence
	traces1 := makeTraces("openat", "read", "read", "read", "close")
	flow1, ok := pc.Match(traces1)
	if !ok || flow1.Intent != "read_file" {
		t.Errorf("expected read_file, got %s (ok=%v)", flow1.Intent, ok)
	}

	// Network connect sequence
	traces2 := makeTracesWithRC("connect", int64(0))
	flow2, ok := pc.Match(traces2)
	if !ok || flow2.Intent != "api_call" {
		t.Errorf("expected api_call, got %s (ok=%v)", flow2.Intent, ok)
	}

	// Failed sequence
	traces3 := makeTracesWithRC("openat", int64(-1))
	flow3, ok := pc.Match(traces3)
	if !ok || flow3.Outcome != types.FlowOutcomeFailure {
		t.Errorf("expected failure, got %s (ok=%v)", flow3.Outcome, ok)
	}
}

// --- empty group ---

func TestPatternEmptyGroup(t *testing.T) {
	pc := NewPatternCatalog()
	_, ok := pc.Match(nil)
	if ok {
		t.Fatal("expected nil traces to not match any pattern")
	}
	_, ok = pc.Match([]types.Trace{})
	if ok {
		t.Fatal("expected empty traces to not match any pattern")
	}
}

// --- description generation ---

func TestPatternDescriptions(t *testing.T) {
	pc := NewPatternCatalog()

	tests := []struct {
		name     string
		traces   []types.Trace
		wantContains string
	}{
		{
			name: "read_file",
			traces: []types.Trace{
				{ID: "0", Timestamp: time.Now(), Syscall: "openat", Args: []string{"openat", "/etc/hosts"}, ReturnValue: 3, Category: types.TraceCategorySyscall},
				{ID: "1", Timestamp: time.Now().Add(time.Millisecond), Syscall: "read", ReturnValue: 100, Category: types.TraceCategorySyscall},
				{ID: "2", Timestamp: time.Now().Add(2 * time.Millisecond), Syscall: "close", ReturnValue: 0, Category: types.TraceCategorySyscall},
			},
			wantContains: "/etc/hosts",
		},
		{
			name: "write_file",
			traces: []types.Trace{
				{ID: "0", Timestamp: time.Now(), Syscall: "openat", Args: []string{"openat", "/tmp/out"}, ReturnValue: 3, Category: types.TraceCategorySyscall},
				{ID: "1", Timestamp: time.Now().Add(time.Millisecond), Syscall: "write", ReturnValue: 42, Category: types.TraceCategorySyscall},
				{ID: "2", Timestamp: time.Now().Add(2 * time.Millisecond), Syscall: "close", ReturnValue: 0, Category: types.TraceCategorySyscall},
			},
			wantContains: "/tmp/out",
		},
		{
			name: "api_call",
			traces: []types.Trace{
				{ID: "0", Timestamp: time.Now(), Syscall: "connect", Args: []string{"connect", "api.example.com:443"}, ReturnValue: 0, Category: types.TraceCategoryNetwork},
			},
			wantContains: "API call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow, ok := pc.Match(tt.traces)
			if !ok {
				t.Fatal("expected pattern to match")
			}
			if flow.Description == "" {
				t.Error("expected non-empty description")
			}
		})
	}
}

// ---- Benchmarks ----

func BenchmarkPatternCatalog_Match(b *testing.B) {
	pc := NewPatternCatalog()
	traces := makeTraces("openat", "read", "read", "close")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pc.Match(traces)
	}
}

func BenchmarkPatternCatalog_MatchNetwork(b *testing.B) {
	pc := NewPatternCatalog()
	traces := makeTracesWithRC("socket", int64(3), "connect", int64(0))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pc.Match(traces)
	}
}
