package collector

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// This file turns a low-level eBPF load failure into an error that names the
// cause the kernel actually reported. Before DF-RABBIT-HOLE-6 the collector
// printed a static "requires CAP_SYS_RESOURCE and kernel 5.11+" for EVERY
// load failure, including map-create EINVAL on hosts where those requirements
// were demonstrably satisfied (root, all capabilities effective, kernel 7.0).
// A misattributed failure sends the operator to the wrong fix, so the
// privilege/kernel text is now derived from the real errno and gated on an
// actual capability check.

// Capability bit numbers from <linux/capability.h> that govern BPF loading.
// CAP_SYS_RESOURCE (bit 24) is deliberately absent: it controls RLIMIT_MEMLOCK,
// not the right to load programs or create maps.
const (
	capSysAdmin = 21
	capPerfmon  = 38
	capBPF      = 39
)

// ebpfPrivileges are the capabilities an eBPF load needs. CAP_BPF (kernel
// 5.8+) covers program and map loading, CAP_PERFMON covers perf/ring-buffer
// telemetry, and CAP_SYS_ADMIN is the pre-5.8 equivalent.
var ebpfPrivileges = []struct {
	bit  int
	name string
}{
	{capBPF, "CAP_BPF"},
	{capPerfmon, "CAP_PERFMON"},
	{capSysAdmin, "CAP_SYS_ADMIN"},
}

// EBPFUnavailableError describes a failed eBPF load with the cause the kernel
// reported and, only when it applies, a targeted remediation. Cause never
// claims a privilege problem: privilege text lives in Remediation and is only
// produced when a required capability is genuinely missing.
type EBPFUnavailableError struct {
	Cause       string // what the kernel actually rejected
	Remediation string // targeted next step; empty when none is known
	Err         error  // underlying load error, kept for errors.Is/As
}

func (e *EBPFUnavailableError) Error() string {
	msg := e.Cause
	if e.Remediation != "" {
		msg += "; " + e.Remediation
	}
	if e.Err != nil {
		msg += " [" + e.Err.Error() + "]"
	}
	return msg
}

// Unwrap exposes the underlying error so callers can still match an errno.
func (e *EBPFUnavailableError) Unwrap() error { return e.Err }

// kernelRelease reports the running kernel release (e.g. "7.0.0-38-generic")
// or "unknown". Error messages quote it so the operator can check the claim.
func kernelRelease() string {
	for _, path := range []string{"/proc/sys/kernel/osrelease", "/proc/version"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) == 0 {
			continue
		}
		return fields[0]
	}
	return "unknown"
}

// effectiveCapabilities returns the process's effective capability mask from
// /proc/self/status. ok is false when the mask cannot be read or parsed; in
// that case callers must not claim a capability is missing, because an
// unreadable mask proves nothing.
func effectiveCapabilities() (mask uint64, ok bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, found := strings.CutPrefix(line, "CapEff:")
		if !found {
			continue
		}
		var parsed uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%x", &parsed); err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// capEffReader reads the process's effective capability mask. Package-level
// hook so tests can exercise both the missing- and held-capability branches
// without changing the test process's privileges.
var capEffReader = effectiveCapabilities

// missingEBPFCapabilities returns the eBPF capabilities absent from the
// effective set. It returns nil when the effective set is unreadable (unknown
// is not missing) or when every capability is held.
func missingEBPFCapabilities() []string {
	mask, ok := capEffReader()
	if !ok {
		return nil
	}
	var missing []string
	for _, c := range ebpfPrivileges {
		if mask&(1<<uint(c.bit)) == 0 {
			missing = append(missing, c.name)
		}
	}
	return missing
}

// heldEBPFCapabilities returns the eBPF capabilities the process actually
// holds. Used to state, truthfully, why a privilege remediation does not apply.
func heldEBPFCapabilities() []string {
	mask, ok := capEffReader()
	if !ok {
		return nil
	}
	var held []string
	for _, c := range ebpfPrivileges {
		if mask&(1<<uint(c.bit)) != 0 {
			held = append(held, c.name)
		}
	}
	return held
}

// privilegeRemediation returns the privilege paragraph for a refusal the
// kernel attributed to insufficient privileges. It names the missing
// capabilities when there are any; when every capability is already held it
// says so instead of repeating a fix that cannot help.
func privilegeRemediation() string {
	if missing := missingEBPFCapabilities(); len(missing) > 0 {
		return fmt.Sprintf("grant the missing capabilities (%s) or run as root", strings.Join(missing, ", "))
	}
	if held := heldEBPFCapabilities(); len(held) > 0 {
		return fmt.Sprintf("all required capabilities are already effective (%s), so the refusal comes from a sysctl such as kernel.unprivileged_bpf_disabled or an LSM policy — not from missing privileges",
			strings.Join(held, ", "))
	}
	return "the effective capability set could not be read; check privileges with 'capsh --print'"
}

// errnos this classifier knows how to describe, in match order. ENOTSUP and
// EOPNOTSUPP are the same value on Linux, so they are listed once.
var knownErrnos = []syscall.Errno{
	syscall.EINVAL,
	syscall.EPERM,
	syscall.EACCES,
	syscall.E2BIG,
	syscall.ENOMEM,
	syscall.EOPNOTSUPP,
	syscall.EBADF,
	syscall.EBUSY,
}

// errnoOf extracts the errno a load failure carries. errors.Is is tried first;
// the message text is the fallback because some errors reach us stringified
// (e.g. from a test hook or a re-wrapped error).
func errnoOf(err error) (syscall.Errno, bool) {
	if err == nil {
		return 0, false
	}
	for _, errno := range knownErrnos {
		if errors.Is(err, errno) {
			return errno, true
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "invalid argument"):
		return syscall.EINVAL, true
	case strings.Contains(msg, "operation not permitted"):
		return syscall.EPERM, true
	case strings.Contains(msg, "permission denied"):
		return syscall.EACCES, true
	case strings.Contains(msg, "cannot allocate memory"):
		return syscall.ENOMEM, true
	case strings.Contains(msg, "argument list too long"):
		return syscall.E2BIG, true
	case strings.Contains(msg, "operation not supported"):
		return syscall.EOPNOTSUPP, true
	case strings.Contains(msg, "bad file descriptor"):
		return syscall.EBADF, true
	}
	return 0, false
}

// nonMapWords are tokens that follow "map " in eBPF error text without naming
// a map (e.g. "map create: invalid argument").
var nonMapWords = map[string]bool{
	"create": true, "lookup": true, "update": true, "delete": true,
	"load": true, "not": true, "closed": true, "with": true,
}

// mapNameFromError extracts the map name a cilium/ebpf load error names, e.g.
// "...: map stack_map: map create: invalid argument" → "stack_map". Returns
// "" when the error does not name one.
func mapNameFromError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for idx := 0; ; {
		pos := strings.Index(msg[idx:], "map ")
		if pos < 0 {
			return ""
		}
		start := idx + pos + len("map ")
		end := start
		for end < len(msg) && (msg[end] == '_' || msg[end] >= 'a' && msg[end] <= 'z' ||
			msg[end] >= 'A' && msg[end] <= 'Z' || msg[end] >= '0' && msg[end] <= '9') {
			end++
		}
		token := msg[start:end]
		// cilium/ebpf formats a named map as "map <name>: ...", so the
		// token must be terminated by a colon; that keeps prose like
		// "no map here" from yielding a bogus name.
		if token != "" && end < len(msg) && msg[end] == ':' && !nonMapWords[token] {
			return token
		}
		idx = start
		if idx >= len(msg) {
			return ""
		}
	}
}

// classifyEBPFError maps a load failure onto a truthful,
// EBPFUnavailableError. Every branch states the errno the kernel returned;
// only an actual EPERM/EACCES refusal with provably missing capabilities may
// mention privileges.
func classifyEBPFError(err error) error {
	if err == nil {
		return nil
	}

	release := kernelRelease()
	target := "the eBPF collection"
	if name := mapNameFromError(err); name != "" {
		target = fmt.Sprintf("map %q", name)
	}

	errno, known := errnoOf(err)
	if !known {
		return &EBPFUnavailableError{
			Cause: fmt.Sprintf("kernel %s rejected %s, and the failure carries no recognisable errno", release, target),
			Err:   err,
		}
	}

	if errno == syscall.EINVAL {
		remediation := "a parameter of the rejected spec is invalid for this kernel; privileges cannot turn an EINVAL into a valid spec — compare the map's key_size/value_size/max_entries with what the kernel requires for that map type"
		if name := mapNameFromError(err); strings.Contains(strings.ToLower(name), "stack") {
			remediation = fmt.Sprintf("a BPF_MAP_TYPE_STACK_TRACE map needs value_size >= 8 and a multiple of 8 (the kernel's stack_map_alloc rejects 0); this build normalizes value_size to %d before load, so a surviving EINVAL means another parameter of map %q is wrong", stackMapValueSize, name)
		}
		return &EBPFUnavailableError{
			Cause: fmt.Sprintf("kernel %s rejected %s with EINVAL (invalid argument) — the kernel does not accept this map type or its map/program parameters, which is a kernel-side objection and not a privilege error",
				release, target),
			Remediation: remediation,
			Err:         err,
		}
	}

	if errno == syscall.EPERM || errno == syscall.EACCES {
		return &EBPFUnavailableError{
			Cause:       fmt.Sprintf("kernel %s refused %s as unprivileged (%v)", release, target, errno),
			Remediation: privilegeRemediation(),
			Err:         err,
		}
	}

	var cause, remediation string
	switch errno {
	case syscall.E2BIG:
		cause = fmt.Sprintf("kernel %s rejected %s with E2BIG (argument list too long) — the map or program is larger than this kernel supports, or the map type is unsupported",
			release, target)
	case syscall.ENOMEM:
		cause = fmt.Sprintf("kernel %s could not allocate memory for %s (ENOMEM)", release, target)
		remediation = "raise the RLIMIT_MEMLOCK limit (needs CAP_SYS_RESOURCE) or free memory"
	case syscall.EOPNOTSUPP:
		cause = fmt.Sprintf("kernel %s does not support %s (EOPNOTSUPP: operation not supported)", release, target)
		remediation = "this kernel does not implement the requested eBPF feature"
	case syscall.EBADF:
		cause = fmt.Sprintf("kernel %s reported an invalid handle for %s (EBADF: bad file descriptor)", release, target)
	case syscall.EBUSY:
		cause = fmt.Sprintf("kernel %s reported %s as busy (EBUSY)", release, target)
	default:
		cause = fmt.Sprintf("kernel %s rejected %s (%v)", release, target, errno)
	}

	return &EBPFUnavailableError{Cause: cause, Remediation: remediation, Err: err}
}

// ebpfHint returns the next step to log alongside a failed load: the
// classified remediation when there is one, otherwise a truthful pointer at
// the failure itself. It deliberately does not suggest regenerating the BPF
// objects, which cannot help when the kernel refuses a valid map.
func ebpfHint(err error) string {
	var unavailable *EBPFUnavailableError
	if errors.As(err, &unavailable) && unavailable.Remediation != "" {
		return unavailable.Remediation
	}
	return "see the reported errno; run with --no-ebpf to operate in degraded mode"
}
