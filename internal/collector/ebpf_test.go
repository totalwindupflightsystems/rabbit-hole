package collector

import (
	"encoding/binary"
	"testing"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// ---- cstring ----

func TestCstring_NullTerminated(t *testing.T) {
	b := []byte{'h', 'e', 'l', 'l', 'o', 0, 'w', 'o', 'r', 'l', 'd'}
	if got := cstring(b); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestCstring_NoNull(t *testing.T) {
	b := []byte{'h', 'e', 'l', 'l', 'o'}
	if got := cstring(b); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestCstring_Empty(t *testing.T) {
	if got := cstring([]byte{}); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCstring_FirstByteNull(t *testing.T) {
	b := []byte{0, 'a', 'b', 'c'}
	if got := cstring(b); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCstring_AllNulls(t *testing.T) {
	b := make([]byte, 10)
	if got := cstring(b); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ---- syscallName ----

func TestSyscallName_Known(t *testing.T) {
	tests := []struct {
		nr   uint64
		name string
	}{
		{0, "read"},
		{1, "write"},
		{2, "open"},
		{3, "close"},
		{9, "mmap"},
		{39, "getpid"},
		{41, "socket"},
		{42, "connect"},
		{43, "accept"},
		{56, "clone"},
		{57, "fork"},
		{59, "execve"},
		{60, "exit"},
		{62, "kill"},
		{202, "futex"},
		{217, "getdents"},
		{228, "clock_gettime"},
		{231, "exit_group"},
		{257, "openat"},
		{318, "getrandom"},
		{332, "statx"},
		{334, "rseq"},
	}
	for _, tt := range tests {
		if got := syscallName(tt.nr); got != tt.name {
			t.Errorf("syscallName(%d) = %q, want %q", tt.nr, got, tt.name)
		}
	}
}

func TestSyscallName_All(t *testing.T) {
	// Cover every branch in syscallName — all known syscall numbers
	all := map[uint64]string{
		0: "read", 1: "write", 2: "open", 3: "close", 4: "stat", 5: "fstat",
		9: "mmap", 10: "mprotect", 11: "munmap", 13: "rt_sigaction",
		14: "rt_sigprocmask", 21: "access", 22: "pipe", 25: "mremap",
		39: "getpid", 41: "socket", 42: "connect", 43: "accept",
		44: "sendto", 45: "recvfrom", 46: "sendmsg", 47: "recvmsg",
		49: "bind", 50: "listen", 56: "clone", 57: "fork",
		59: "execve", 60: "exit", 61: "wait4", 62: "kill",
		78: "getdents64", 79: "getcwd", 80: "chdir", 82: "rename",
		85: "creat", 89: "readlink", 96: "gettimeofday",
		202: "futex", 217: "getdents", 228: "clock_gettime",
		230: "clock_nanosleep", 231: "exit_group", 232: "epoll_wait",
		233: "epoll_ctl", 234: "tgkill", 257: "openat",
		262: "newfstatat", 281: "socketpair", 291: "epoll_create1",
		318: "getrandom", 332: "statx", 334: "rseq",
	}
	for nr, expected := range all {
		if got := syscallName(nr); got != expected {
			t.Errorf("syscallName(%d) = %q, want %q", nr, got, expected)
		}
	}
}

func TestSyscallName_Unknown(t *testing.T) {
	if got := syscallName(999); got != "syscall_999" {
		t.Errorf("expected 'syscall_999', got %q", got)
	}
	// Test a few more unknown values
	for _, nr := range []uint64{6, 7, 8, 100, 500, 1000} {
		if got := syscallName(nr); got == "" {
			t.Errorf("syscallName(%d) returned empty string", nr)
		}
	}
}

func TestRingBuffer_String(t *testing.T) {
	rb := NewRingBuffer(100)
	rb.Push(types.Trace{ID: "a"})
	rb.Push(types.Trace{ID: "b"})

	s := rb.String()
	if s == "" {
		t.Error("String() returned empty")
	}
	// Should contain size and used info
	if len(s) < 10 {
		t.Errorf("String() too short: %q", s)
	}
}

// ---- parseTraceEvent ----

// buildTraceEvent constructs a raw binary trace_event buffer matching the
// struct layout in collector.bpf.c.
func buildTraceEvent(opts struct {
	TimestampNs uint64
	PID         int32
	SyscallNr   uint64
	Args        [6]uint64
	Ret         int64
	Errno       int32
	DurationNs  uint64
	Category    uint8
}) []byte {
	buf := make([]byte, 637)
	le := binary.LittleEndian

	le.PutUint64(buf[0:8], opts.TimestampNs)
	le.PutUint32(buf[8:12], uint32(opts.PID))
	// bytes 12-16: tid (reserved, zero)
	le.PutUint64(buf[16:24], opts.SyscallNr)
	for i := 0; i < 6; i++ {
		off := 24 + i*8
		le.PutUint64(buf[off:off+8], opts.Args[i])
	}
	le.PutUint64(buf[72:80], uint64(opts.Ret))
	le.PutUint32(buf[80:84], uint32(opts.Errno))
	le.PutUint64(buf[84:92], opts.DurationNs)
	buf[352] = opts.Category
	// comm and filename fields: leave zero (null-terminated empty strings)
	// network fields: leave zero
	return buf
}

func TestParseTraceEvent_Basic(t *testing.T) {
	raw := buildTraceEvent(struct {
		TimestampNs uint64
		PID         int32
		SyscallNr   uint64
		Args        [6]uint64
		Ret         int64
		Errno       int32
		DurationNs  uint64
		Category    uint8
	}{
		TimestampNs: 1700000000000000000,
		PID:         12345,
		SyscallNr:   257, // openat
		Args:        [6]uint64{0x7f, 0x0, 0x0, 0x0, 0x0, 0x0},
		Ret:         3,
		Errno:       0,
		DurationNs:  15000,
		Category:    2, // TraceCategoryFile
	})

	trace, err := parseTraceEvent(raw)
	if err != nil {
		t.Fatalf("parseTraceEvent: %v", err)
	}

	if trace.PID != 12345 {
		t.Errorf("PID = %d, want 12345", trace.PID)
	}
	if trace.Syscall != "openat" {
		t.Errorf("Syscall = %q, want openat", trace.Syscall)
	}
	if trace.ReturnValue != 3 {
		t.Errorf("ReturnValue = %d, want 3", trace.ReturnValue)
	}
	if trace.Errno != 0 {
		t.Errorf("Errno = %d, want 0", trace.Errno)
	}
	if trace.Duration != 15000 {
		t.Errorf("Duration = %dns, want 15000ns", trace.Duration)
	}
	if len(trace.Args) != 1 {
		t.Errorf("expected 1 arg, got %d: %v", len(trace.Args), trace.Args)
	}
	if trace.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestParseTraceEvent_AllCategories(t *testing.T) {
	categories := map[uint8]string{
		0: "syscall",
		1: "network",
		2: "file",
		3: "llm_call",
		4: "context_window",
		5: "process",
		6: "resource",
		7: "syscall", // unknown defaults to syscall
	}

	for cat, expected := range categories {
		raw := buildTraceEvent(struct {
			TimestampNs uint64
			PID         int32
			SyscallNr   uint64
			Args        [6]uint64
			Ret         int64
			Errno       int32
			DurationNs  uint64
			Category    uint8
		}{
			TimestampNs: 1000,
			PID:         1,
			SyscallNr:   0,
			Category:    cat,
		})

		trace, err := parseTraceEvent(raw)
		if err != nil {
			t.Fatalf("category %d: %v", cat, err)
		}
		got := string(trace.Category)
		if got != expected {
			t.Errorf("category byte %d: Category = %q, want %q", cat, got, expected)
		}
	}
}

func TestParseTraceEvent_AllArgsPopulated(t *testing.T) {
	raw := buildTraceEvent(struct {
		TimestampNs uint64
		PID         int32
		SyscallNr   uint64
		Args        [6]uint64
		Ret         int64
		Errno       int32
		DurationNs  uint64
		Category    uint8
	}{
		TimestampNs: 1000,
		PID:         42,
		SyscallNr:   1, // write
		Args:        [6]uint64{0x1, 0x2, 0x3, 0x4, 0x5, 0x6},
		Ret:         -1,
		Errno:       9, // EBADF
		DurationNs:  5000,
		Category:    0,
	})

	trace, err := parseTraceEvent(raw)
	if err != nil {
		t.Fatalf("parseTraceEvent: %v", err)
	}

	if len(trace.Args) != 6 {
		t.Errorf("expected 6 args, got %d", len(trace.Args))
	}
	if trace.ReturnValue != -1 {
		t.Errorf("ReturnValue = %d, want -1", trace.ReturnValue)
	}
	if trace.Errno != 9 {
		t.Errorf("Errno = %d, want 9", trace.Errno)
	}
}

func TestParseTraceEvent_ZeroArgs(t *testing.T) {
	raw := buildTraceEvent(struct {
		TimestampNs uint64
		PID         int32
		SyscallNr   uint64
		Args        [6]uint64
		Ret         int64
		Errno       int32
		DurationNs  uint64
		Category    uint8
	}{
		TimestampNs: 1000,
		PID:         1,
		SyscallNr:   60, // exit
		Args:        [6]uint64{},
		Ret:         0,
		Category:    0,
	})

	trace, err := parseTraceEvent(raw)
	if err != nil {
		t.Fatalf("parseTraceEvent: %v", err)
	}

	if len(trace.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(trace.Args))
	}
}

func TestParseTraceEvent_TooShort(t *testing.T) {
	raw := make([]byte, 636)
	_, err := parseTraceEvent(raw)
	if err == nil {
		t.Error("expected error for too-short buffer")
	}
}

func TestParseTraceEvent_TimestampParsing(t *testing.T) {
	raw := buildTraceEvent(struct {
		TimestampNs uint64
		PID         int32
		SyscallNr   uint64
		Args        [6]uint64
		Ret         int64
		Errno       int32
		DurationNs  uint64
		Category    uint8
	}{
		TimestampNs: 999888777666555,
		PID:         1,
		SyscallNr:   0,
		Category:    0,
	})

	trace, err := parseTraceEvent(raw)
	if err != nil {
		t.Fatalf("parseTraceEvent: %v", err)
	}

	if trace.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
	expectedNs := int64(999888777666555)
	if trace.Timestamp.UnixNano() != expectedNs {
		t.Errorf("timestamp ns = %d, want %d", trace.Timestamp.UnixNano(), expectedNs)
	}
}

// ---- bufSize ----

func TestBufSize(t *testing.T) {
	c, err := NewEBPFCollector(500, 10, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	defer c.Close()

	if got := c.bufSize(); got != 500 {
		t.Errorf("bufSize() = %d, want 500", got)
	}
}

func TestBufSize_Default(t *testing.T) {
	c, err := NewEBPFCollector(0, 10, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	defer c.Close()

	if got := c.bufSize(); got != 100000 {
		t.Errorf("bufSize() = %d, want 100000 (default)", got)
	}
}

// ---- Benchmarks ----

func BenchmarkParseTraceEvent(b *testing.B) {
	raw := buildTraceEvent(struct {
		TimestampNs uint64
		PID         int32
		SyscallNr   uint64
		Args        [6]uint64
		Ret         int64
		Errno       int32
		DurationNs  uint64
		Category    uint8
	}{
		TimestampNs: 1700000000000000000,
		PID:         12345,
		SyscallNr:   257, // openat
		Args:        [6]uint64{0x7f, 0x2, 0x1b6, 0x0, 0x0, 0x0},
		Ret:         3,
		Errno:       0,
		DurationNs:  15000,
		Category:    2, // TraceCategoryFile
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parseTraceEvent(raw)
		if err != nil {
			b.Fatalf("parseTraceEvent: %v", err)
		}
	}
}
