package collector

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/cilium/ebpf"
)

// stalePrivilegeClaim is the message DF-RABBIT-HOLE-6 exists to remove: it was
// printed for EVERY load failure, including map-create EINVAL on hosts that
// demonstrably held CAP_SYS_RESOURCE and ran kernel 7.0. No classified error
// may contain it.
const stalePrivilegeClaim = "requires CAP_SYS_RESOURCE and kernel 5.11+"

// assertNoStaleClaim fails when the message repeats the debunked claim.
func assertNoStaleClaim(t *testing.T, msg string) {
	t.Helper()
	if strings.Contains(msg, stalePrivilegeClaim) {
		t.Errorf("message still carries the stale claim %q: %s", stalePrivilegeClaim, msg)
	}
	if strings.Contains(msg, "kernel 5.11") {
		t.Errorf("message still claims a kernel-version floor: %s", msg)
	}
}

// TestClassifyEBPFError_ReportsKernelCause covers the errno classifier the
// acceptance criteria call for: each errno must map to a message that states
// what the kernel actually returned.
func TestClassifyEBPFError_ReportsKernelCause(t *testing.T) {
	loadErr := func(errno syscall.Errno) error {
		// Mirrors the shape cilium/ebpf produces for a failed map create:
		// the map name is embedded, followed by "map create: <errno>".
		return fmt.Errorf("load bpf objects: map stack_map: map create: %w", errno)
	}

	tests := []struct {
		name    string
		err     error
		want    []string
		notWant []string
	}{
		{
			name:    "map create EINVAL",
			err:     loadErr(syscall.EINVAL),
			want:    []string{"map \"stack_map\"", "EINVAL", "invalid argument", "not a privilege error"},
			notWant: []string{"grant the missing capabilities", "unprivileged"},
		},
		{
			name:    "EPERM (missing capabilities)",
			err:     loadErr(syscall.EPERM),
			want:    []string{"map \"stack_map\"", "operation not permitted"},
			notWant: []string{"kernel-version"},
		},
		{
			name: "EACCES without a map name",
			err:  fmt.Errorf("load bpf objects: failed to load program: %w", syscall.EACCES),
			want: []string{"the eBPF collection", "permission denied"},
		},
		{
			name: "E2BIG",
			err:  loadErr(syscall.E2BIG),
			want: []string{"E2BIG", "argument list too long"},
		},
		{
			name: "ENOMEM names the memory limit, not a privilege requirement",
			err:  loadErr(syscall.ENOMEM),
			want: []string{"ENOMEM", "RLIMIT_MEMLOCK"},
		},
		{
			name: "EOPNOTSUPP",
			err:  loadErr(syscall.EOPNOTSUPP),
			want: []string{"EOPNOTSUPP", "does not support"},
		},
		{
			name: "EBADF",
			err:  loadErr(syscall.EBADF),
			want: []string{"EBADF", "bad file descriptor"},
		},
		{
			name: "EBUSY",
			err:  loadErr(syscall.EBUSY),
			want: []string{"EBUSY"},
		},
		{
			name: "unrecognised failure does not invent a cause",
			err:  errors.New("load bpf objects: something odd happened"),
			want: []string{"no recognisable errno"},
		},
		{
			name: "stringified errno (no wrapped errno value)",
			err:  errors.New("map events: map create: invalid argument"),
			want: []string{"map \"events\"", "EINVAL"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyEBPFError(tc.err)
			if err == nil {
				t.Fatal("classifyEBPFError() = nil, want an error")
			}
			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q missing %q", msg, want)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(msg, notWant) {
					t.Errorf("message %q must not contain %q", msg, notWant)
				}
			}
			assertNoStaleClaim(t, msg)

			// The original error must stay reachable for callers that
			// match on their own errno.
			var unavailable *EBPFUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("error %T is not *EBPFUnavailableError", err)
			}
			if !errors.Is(err, tc.err) && unavailable.Err != tc.err {
				t.Errorf("underlying error not preserved: %v", unavailable.Err)
			}
		})
	}
}

// TestClassifyEBPFError_StackMapEINVALIsTargeted pins the remediation the
// board asked for: a stack-trace map EINVAL must explain the value_size floor
// instead of blaming privileges.
func TestClassifyEBPFError_StackMapEINVALIsTargeted(t *testing.T) {
	err := classifyEBPFError(fmt.Errorf("load bpf objects: map stack_map: map create: %w", syscall.EINVAL))
	msg := err.Error()

	for _, want := range []string{"BPF_MAP_TYPE_STACK_TRACE", "value_size", "8"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stack-map EINVAL message %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "grant the missing capabilities") {
		t.Errorf("stack-map EINVAL must not ask for capabilities: %s", msg)
	}
	assertNoStaleClaim(t, msg)
}

// TestPrivilegeRemediation_GatedOnEffectiveCaps proves the privilege text is
// only produced when a capability is provably missing — the gate criterion in
// the fix scope.
func TestPrivilegeRemediation_GatedOnEffectiveCaps(t *testing.T) {
	allCaps := uint64(1)<<capBPF | uint64(1)<<capPerfmon | uint64(1)<<capSysAdmin
	noCaps := uint64(0)

	original := capEffReader
	t.Cleanup(func() { capEffReader = original })

	t.Run("all capabilities held", func(t *testing.T) {
		capEffReader = func() (uint64, bool) { return allCaps, true }

		if missing := missingEBPFCapabilities(); len(missing) != 0 {
			t.Fatalf("missingEBPFCapabilities() = %v, want none", missing)
		}
		got := privilegeRemediation()
		if strings.Contains(got, "grant the missing capabilities") {
			t.Errorf("remediation %q asks for capabilities the process already holds", got)
		}
		for _, want := range []string{"already effective", "CAP_BPF", "CAP_PERFMON", "CAP_SYS_ADMIN", "sysctl"} {
			if !strings.Contains(got, want) {
				t.Errorf("remediation %q missing %q", got, want)
			}
		}
	})

	t.Run("no capabilities held", func(t *testing.T) {
		capEffReader = func() (uint64, bool) { return noCaps, true }

		got := privilegeRemediation()
		for _, want := range []string{"CAP_BPF", "CAP_PERFMON", "CAP_SYS_ADMIN", "run as root"} {
			if !strings.Contains(got, want) {
				t.Errorf("remediation %q missing %q", got, want)
			}
		}
	})

	t.Run("only CAP_PERFMON held", func(t *testing.T) {
		capEffReader = func() (uint64, bool) { return uint64(1) << capPerfmon, true }

		missing := missingEBPFCapabilities()
		if strings.Join(missing, ",") != "CAP_BPF,CAP_SYS_ADMIN" {
			t.Fatalf("missing = %v, want [CAP_BPF CAP_SYS_ADMIN]", missing)
		}
		got := privilegeRemediation()
		if !strings.Contains(got, "CAP_BPF, CAP_SYS_ADMIN") || strings.Contains(got, "CAP_PERFMON,") {
			t.Errorf("remediation %q must name only the missing capabilities", got)
		}
	})

	t.Run("unreadable mask claims nothing", func(t *testing.T) {
		capEffReader = func() (uint64, bool) { return 0, false }

		if missing := missingEBPFCapabilities(); missing != nil {
			t.Fatalf("missingEBPFCapabilities() = %v, want nil when the mask is unreadable", missing)
		}
		got := privilegeRemediation()
		if strings.Contains(got, "grant the missing capabilities") {
			t.Errorf("remediation %q claims missing capabilities from an unreadable mask", got)
		}
		if !strings.Contains(got, "capsh") {
			t.Errorf("remediation %q should point at a way to check privileges", got)
		}
	})
}

// TestClassifyEBPFError_EPERMNamesCapsHonestly checks both capability branches
// end to end through the classifier.
func TestClassifyEBPFError_EPERMNamesCapsHonestly(t *testing.T) {
	original := capEffReader
	t.Cleanup(func() { capEffReader = original })

	loadErr := fmt.Errorf("load bpf objects: map events: map create: %w", syscall.EPERM)

	capEffReader = func() (uint64, bool) {
		return uint64(1)<<capBPF | uint64(1)<<capPerfmon | uint64(1)<<capSysAdmin, true
	}
	heldMsg := classifyEBPFError(loadErr).Error()
	if strings.Contains(heldMsg, "grant the missing capabilities") {
		t.Errorf("all caps held but message asks for them: %s", heldMsg)
	}
	if !strings.Contains(heldMsg, "already effective") {
		t.Errorf("message %q should say the capabilities are already effective", heldMsg)
	}
	assertNoStaleClaim(t, heldMsg)

	capEffReader = func() (uint64, bool) { return 0, true }
	missingMsg := classifyEBPFError(loadErr).Error()
	if !strings.Contains(missingMsg, "grant the missing capabilities") {
		t.Errorf("no caps held but message %q does not ask for them", missingMsg)
	}
	assertNoStaleClaim(t, missingMsg)
}

// TestErrnoOf covers errno extraction, including the stringified fallback.
func TestErrnoOf(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   syscall.Errno
		wantOK bool
	}{
		{name: "wrapped EINVAL", err: fmt.Errorf("map x: %w", syscall.EINVAL), want: syscall.EINVAL, wantOK: true},
		{name: "text invalid argument", err: errors.New("map create: invalid argument"), want: syscall.EINVAL, wantOK: true},
		{name: "text operation not permitted", err: errors.New("operation not permitted"), want: syscall.EPERM, wantOK: true},
		{name: "text permission denied", err: errors.New("permission denied"), want: syscall.EACCES, wantOK: true},
		{name: "text cannot allocate memory", err: errors.New("cannot allocate memory"), want: syscall.ENOMEM, wantOK: true},
		{name: "text operation not supported", err: errors.New("operation not supported"), want: syscall.EOPNOTSUPP, wantOK: true},
		{name: "nil", err: nil, wantOK: false},
		{name: "unknown", err: errors.New("boom"), wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := errnoOf(tc.err)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("errnoOf(%v) = %v, %v; want %v, %v", tc.err, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestMapNameFromError covers the name extraction the messages depend on.
func TestMapNameFromError(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"load bpf objects: map stack_map: map create: invalid argument", "stack_map"},
		{"map events: map create: invalid argument", "events"},
		{"map pid_filter: map create: operation not permitted", "pid_filter"},
		{"field TraceEnterSyscall: map stack_map: map create: invalid argument", "stack_map"},
		{"load bpf objects: field TraceExitSyscall: program trace_exit_syscall: map stack_map: map create: invalid argument", "stack_map"},
		{"map create: invalid argument", ""},
		{"map not found", ""},
		{"no map here", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := mapNameFromError(errors.New(tc.msg)); got != tc.want {
			t.Errorf("mapNameFromError(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
	if got := mapNameFromError(nil); got != "" {
		t.Errorf("mapNameFromError(nil) = %q, want \"\"", got)
	}
}

// TestNormalizeStackMapSpecs proves the stack-trace map gets a legal value_size
// while every other map keeps the value_size the object declared.
func TestNormalizeStackMapSpecs(t *testing.T) {
	spec := &ebpf.CollectionSpec{Maps: map[string]*ebpf.MapSpec{
		"stack_map":  {Type: ebpf.StackTrace, KeySize: 4, ValueSize: 0, MaxEntries: 10240},
		"events":     {Type: ebpf.RingBuf, ValueSize: 0, MaxEntries: 262144},
		"pid_filter": {Type: ebpf.Hash, KeySize: 4, ValueSize: 1, MaxEntries: 1024},
		"presized":   {Type: ebpf.StackTrace, KeySize: 4, ValueSize: 32, MaxEntries: 128},
	}}

	normalizeStackMapSpecs(spec)

	if got := spec.Maps["stack_map"].ValueSize; got != stackMapValueSize {
		t.Errorf("stack_map value_size = %d, want %d", got, stackMapValueSize)
	}
	if got := spec.Maps["events"].ValueSize; got != 0 {
		t.Errorf("events value_size = %d, want 0 (non-stack-trace maps are untouched)", got)
	}
	if got := spec.Maps["pid_filter"].ValueSize; got != 1 {
		t.Errorf("pid_filter value_size = %d, want 1", got)
	}
	if got := spec.Maps["presized"].ValueSize; got != 32 {
		t.Errorf("presized value_size = %d, want 32 (explicit value preserved)", got)
	}
}

// TestEmbeddedObjectStackMapsNormalized runs the normalization over the
// object actually embedded in the binary: after it, every stack-trace map must
// satisfy the kernel's `value_size >= 8 && value_size % 8 == 0` rule, which is
// what made `map create` return EINVAL before DF-RABBIT-HOLE-6.
func TestEmbeddedObjectStackMapsNormalized(t *testing.T) {
	spec, err := loadBpf()
	if err != nil {
		t.Fatalf("loadBpf: %v", err)
	}

	names := stackTraceMapNames(spec)
	if len(names) == 0 {
		t.Fatal("embedded object declares no stack-trace map; the DF-6 probe expected stack_map")
	}

	normalizeStackMapSpecs(spec)

	for _, name := range names {
		ms := spec.Maps[name]
		if ms.ValueSize < 8 || ms.ValueSize%8 != 0 {
			t.Errorf("map %q value_size = %d, want >= 8 and a multiple of 8", name, ms.ValueSize)
		}
	}
}
