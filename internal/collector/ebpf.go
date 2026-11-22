// Package collector implements the eBPF-based trace collection layer.
//
// eBPF programs are compiled from collector.bpf.c via bpf2go code generation.
// Run `go generate ./internal/collector/` after installing clang and kernel headers.
package collector

//go:generate sh -c "bpftool btf dump file /sys/kernel/btf/vmlinux format c > bpf/vmlinux.h"
//go:generate bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -Wno-missing-declarations" -target amd64 bpf bpf/collector.bpf.c -- -I/usr/include/x86_64-linux-gnu
//go:generate bpf2go -cc clang -cflags "-O2 -g -Wall -Wno-missing-declarations" -target arm64 bpf bpf/collector.bpf.c

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/google/uuid"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// eBPFCollector attaches eBPF programs to a target agent process and captures
// kernel-level telemetry (syscalls, file ops, network calls) via the perf
// event ring buffer.
type eBPFCollector struct {
	objs     bpfObjects
	links    []link.Link
	sessions map[string]*collectionSession
	mu       sync.RWMutex

	ringBuffer  *RingBuffer
	ringReader  *ringbuf.Reader
	maxSessions int
	logger      *slog.Logger

	// ebpfErr is non-nil when the collector is running degraded — kernel
	// probes failed to load and no kernel telemetry will arrive. It carries
	// the cause the kernel actually reported (see classifyEBPFError); it is
	// never a guess about privileges or kernel version. Ring buffer and
	// session bookkeeping still work.
	ebpfErr error

	// quietInit suppresses only the best-effort eBPF init diagnostics. It is
	// set when the caller has ALREADY declared degraded mode to the user
	// (--no-ebpf / --demo-stream), where repeating "memlock failed" and
	// "kernel probes unavailable" reads like an unexpected failure instead
	// of the mode the user asked for. It never changes what is attempted:
	// the load still runs and ebpfErr still records the real cause.
	quietInit bool

	// bpfDebug prints the kernel verifier log when the BPF collection fails
	// to load (RABBITHOLE_BPF_DEBUG). Off by default: the log is large and
	// only useful while debugging a load failure.
	bpfDebug bool
}

// Option configures construction-time policy for NewEBPFCollector. Options
// never change what the collector can do once it is built.
type Option func(*collectorOptions)

type collectorOptions struct {
	quietInit bool
	bpfDebug  bool
}

// WithQuietEBPFInit suppresses the init-time eBPF diagnostics emitted when
// kernel probes fail to load. Use it only after the caller has already told
// the user telemetry is unavailable or not requested (attach --no-ebpf,
// serve --no-ebpf / --demo-stream): the caller owns the single honest
// degraded-mode message, so the best-effort init must not add a second and
// third WARN that read like errors (DF-RABBIT-HOLE-2).
//
// The default (no option) keeps every diagnostic: on the non-degraded path a
// real kernel capability failure must still say what the kernel reported.
func WithQuietEBPFInit() Option {
	return func(o *collectorOptions) { o.quietInit = true }
}

// WithBPFDebug turns on verifier-log reporting for a failed BPF load
// (RABBITHOLE_BPF_DEBUG). The kernel only produces the verifier log when a
// program fails to load, so this is a startup diagnostic: when the load
// succeeds nothing extra is emitted. The load already requests
// ebpf.LogLevelBranch so the log exists to be printed.
func WithBPFDebug() Option {
	return func(o *collectorOptions) { o.bpfDebug = true }
}

// collectionSession tracks the state of a single attached agent process.
type collectionSession struct {
	pid       int32
	filterFD  int
	attached  bool
	createdAt time.Time
}

// NewEBPFCollector creates and initializes an eBPF-based trace collector.
//
// It attempts to load compiled BPF programs (requires `go generate` + clang).
// If eBPF loading fails (bpf2go not run, no clang, missing kernel support,
// insufficient privileges), the collector operates in degraded mode — ring
// buffer and session tracking work, but no kernel events arrive. Callers can
// distinguish the states via EBPFEnabled() and PreflightEBPF().
//
// Parameters:
//   - bufSize: ring buffer capacity (0 = default 100000)
//   - maxSessions: maximum concurrent sessions (0 = default 50)
//   - logger: structured logger (uses slog.Default if nil)
func NewEBPFCollector(bufSize, maxSessions int, logger *slog.Logger, options ...Option) (*eBPFCollector, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var opts collectorOptions
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	if bufSize <= 0 {
		bufSize = 100000
	}
	if maxSessions <= 0 {
		maxSessions = 50
	}

	rb := NewRingBuffer(bufSize)

	c := &eBPFCollector{
		sessions:    make(map[string]*collectionSession),
		ringBuffer:  rb,
		maxSessions: maxSessions,
		logger:      logger,
		quietInit:   opts.quietInit,
		bpfDebug:    opts.bpfDebug,
	}

	// Best-effort eBPF loading. If bpf2go hasn't been run or the kernel
	// lacks BPF support, operate without kernel probes. The failure is
	// recorded so callers can hard-fail via PreflightEBPF() when they
	// need real telemetry (see attach/serve).
	if err := c.loadEBPF(); err != nil {
		c.ebpfErr = err
		if !c.quietInit {
			logger.Warn("ebpf: kernel probes unavailable, running without eBPF",
				"err", err,
				"hint", ebpfHint(err),
			)
		}
	}

	return c, nil
}

// EBPFEnabled reports whether kernel probes are loaded and telemetry is
// flowing. When false, the collector runs degraded: session bookkeeping and
// the ring buffer work, but no kernel events arrive.
func (c *eBPFCollector) EBPFEnabled() bool {
	return c.ebpfErr == nil
}

// PreflightEBPF returns nil when eBPF probes are loaded and functional.
// When the collector is running degraded it returns an explicit error stating
// that telemetry is DISABLED, the cause the kernel actually reported, and the
// opt-in escape hatch. It never asserts a privilege or kernel-version
// requirement: those are only named when the load error was an actual
// EPERM/EACCES refusal and the required capability is provably missing.
// Commands that promise kernel telemetry (attach, serve) must check this
// before starting; session-management commands (status, list, detach) must not.
func (c *eBPFCollector) PreflightEBPF() error {
	if c.ebpfErr == nil {
		return nil
	}
	return fmt.Errorf("eBPF unavailable — telemetry DISABLED (use --no-ebpf to run in degraded mode): %w", c.ebpfErr)
}

// removeMemlock is a package-level hook so tests can simulate an
// unprivileged host (no CAP_SYS_RESOURCE) without dropping privileges.
var removeMemlock = rlimit.RemoveMemlock

// stackMapValueSize is the minimum legal value_size for a
// BPF_MAP_TYPE_STACK_TRACE map. The kernel's stack_map_alloc() has always
// required `value_size >= 8 && value_size % 8 == 0`, so a spec that leaves
// value_size at the clang-emitted default of 0 is rejected with EINVAL on
// every kernel — not only on new ones. The generated object declares no
// __type(value, ...) for stack_map, so the spec is normalized before load
// (DF-RABBIT-HOLE-6). 8 keeps the default stack layout (no
// BPF_F_STACK_BUILD_ID) and is accepted by every kernel that has stack maps.
const stackMapValueSize = 8

// stackTraceMapNames returns the names of the collection's stack-trace maps.
func stackTraceMapNames(spec *ebpf.CollectionSpec) []string {
	var names []string
	for name, ms := range spec.Maps {
		if ms.Type == ebpf.StackTrace {
			names = append(names, name)
		}
	}
	return names
}

// normalizeStackMapSpecs gives every stack-trace map a legal value_size.
// A single unloadable map aborts the whole collection, so leaving the
// clang-emitted 0 here costs all telemetry for an unrelated map's sake.
func normalizeStackMapSpecs(spec *ebpf.CollectionSpec) {
	for _, ms := range spec.Maps {
		if ms.Type != ebpf.StackTrace || ms.ValueSize != 0 {
			continue
		}
		ms.ValueSize = stackMapValueSize
	}
}

// logVerifierError prints the kernel BPF verifier log carried by a failed
// load. RABBITHOLE_BPF_DEBUG turns this on; a load that succeeds carries no
// log, so nothing is printed in the normal case.
func logVerifierError(logger *slog.Logger, err error) {
	var ve *ebpf.VerifierError
	if !errors.As(err, &ve) || len(ve.Log) == 0 {
		return
	}
	logger.Error("ebpf: verifier log", "log", strings.Join(ve.Log, "\n"))
}

// loadCollection loads the embedded BPF collection with the stack-trace maps
// normalized for the kernel's documented value_size floor.
func loadCollection(opts *ebpf.CollectionOptions) (*bpfObjects, error) {
	spec, err := loadBpf()
	if err != nil {
		return nil, fmt.Errorf("load bpf spec: %w", err)
	}
	normalizeStackMapSpecs(spec)

	objs := &bpfObjects{}
	if err := spec.LoadAndAssign(objs, opts); err != nil {
		return nil, err
	}
	return objs, nil
}

// loadEBPF attempts to load eBPF programs and start the ring-buffer reader.
func (c *eBPFCollector) loadEBPF() error {
	if err := removeMemlock(); err != nil {
		err = fmt.Errorf("remove memlock rlimit: %w", err)
		if !c.quietInit {
			c.logger.Warn("ebpf: remove memlock rlimit failed, continuing without eBPF",
				"err", err,
				"hint", ebpfHint(classifyEBPFError(err)),
			)
		}
		return classifyEBPFError(err)
	}

	var objs *bpfObjects
	opts := &ebpf.CollectionOptions{
		Programs: ebpf.ProgramOptions{
			LogLevel: ebpf.LogLevelBranch,
		},
	}
	objs, err := loadCollection(opts)
	if err != nil {
		if c.bpfDebug {
			logVerifierError(c.logger, err)
		}
		return classifyEBPFError(fmt.Errorf("load bpf objects: %w", err))
	}

	if objs.Events == nil {
		objs.Close()
		return fmt.Errorf("events map not found in bpf objects")
	}
	if objs.PidFilter == nil {
		objs.Close()
		return fmt.Errorf("pid_filter map not found in bpf objects")
	}

	// events is a BPF_MAP_TYPE_RINGBUF (see collector.bpf.c): it must be read
	// with ringbuf.Reader. A perf.Reader cannot attach to a ring-buffer map,
	// so the previous perf path always failed once the maps loaded.
	rd, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		objs.Close()
		return fmt.Errorf("create ring buffer reader: %w", err)
	}

	c.objs = *objs
	c.ringReader = rd

	go c.readEvents()
	return nil
}

func (c *eBPFCollector) bufSize() int {
	return c.ringBuffer.Stats().Size
}

// readEvents runs in a background goroutine, reading raw events from the
// ring buffer, parsing them into types.Trace values, and pushing them
// into the in-memory ring buffer.
func (c *eBPFCollector) readEvents() {
	for {
		record, err := c.ringReader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			c.logger.Error("ebpf: ring buffer read error", "err", err)
			return
		}

		trace, err := parseTraceEvent(record.RawSample)
		if err != nil {
			c.logger.Error("ebpf: parse trace event", "err", err)
			continue
		}

		c.ringBuffer.Push(trace)
	}
}

// Close releases all eBPF resources.
func (c *eBPFCollector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	if c.ringReader != nil {
		if err := c.ringReader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ring buffer reader: %w", err))
		}
	}

	for _, l := range c.links {
		if err := l.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close bpf link: %w", err))
		}
	}

	if err := c.objs.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close bpf objects: %w", err))
	}
	return errors.Join(errs...)
}

// parseTraceEvent manually parses a raw trace_event from the eBPF perf buffer.
// Binary layout matches collector.bpf.c struct trace_event.
func parseTraceEvent(raw []byte) (types.Trace, error) {
	if len(raw) < 637 {
		return types.Trace{}, fmt.Errorf("trace event too short: %d bytes", len(raw))
	}

	le := binary.LittleEndian

	timestampNs := le.Uint64(raw[0:8])
	pid := int32(le.Uint32(raw[8:12]))
	_ = le.Uint32(raw[12:16]) // tid — reserved
	syscallNr := le.Uint64(raw[16:24])

	args := []string{}
	for i := 0; i < 6; i++ {
		off := 24 + i*8
		val := le.Uint64(raw[off : off+8])
		if val != 0 {
			args = append(args, fmt.Sprintf("0x%x", val))
		}
	}

	ret := int64(le.Uint64(raw[72:80]))
	errnoVal := int32(le.Uint32(raw[80:84]))
	durationNs := le.Uint64(raw[84:92])

	category := raw[352]
	var tc types.TraceCategory
	switch category {
	case 0:
		tc = types.TraceCategorySyscall
	case 1:
		tc = types.TraceCategoryNetwork
	case 2:
		tc = types.TraceCategoryFile
	case 3:
		tc = types.TraceCategoryLLMCall
	case 4:
		tc = types.TraceCategoryContextWindow
	case 5:
		tc = types.TraceCategoryProcess
	case 6:
		tc = types.TraceCategoryResource
	default:
		tc = types.TraceCategorySyscall
	}

	// reserved: comm, filename, network fields
	_ = cstring(raw[353:369])
	_ = cstring(raw[369:625])
	_ = le.Uint32(raw[625:629])
	_ = le.Uint32(raw[629:633])
	_ = le.Uint16(raw[633:635])
	_ = le.Uint16(raw[635:637])

	return types.Trace{
		ID:          uuid.Must(uuid.NewV7()).String(),
		PID:         pid,
		Timestamp:   time.Unix(0, int64(timestampNs)),
		Category:    tc,
		Syscall:     syscallName(syscallNr),
		Args:        args,
		ReturnValue: ret,
		Duration:    time.Duration(durationNs) * time.Nanosecond,
		Errno:       errnoVal,
	}, nil
}

// cstring extracts a null-terminated C string from a byte slice.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// syscallName returns the Linux syscall name for the given number.
func syscallName(nr uint64) string {
	switch nr {
	case 0:
		return "read"
	case 1:
		return "write"
	case 2:
		return "open"
	case 3:
		return "close"
	case 4:
		return "stat"
	case 5:
		return "fstat"
	case 9:
		return "mmap"
	case 10:
		return "mprotect"
	case 11:
		return "munmap"
	case 13:
		return "rt_sigaction"
	case 14:
		return "rt_sigprocmask"
	case 21:
		return "access"
	case 22:
		return "pipe"
	case 25:
		return "mremap"
	case 39:
		return "getpid"
	case 41:
		return "socket"
	case 42:
		return "connect"
	case 43:
		return "accept"
	case 44:
		return "sendto"
	case 45:
		return "recvfrom"
	case 46:
		return "sendmsg"
	case 47:
		return "recvmsg"
	case 49:
		return "bind"
	case 50:
		return "listen"
	case 56:
		return "clone"
	case 57:
		return "fork"
	case 59:
		return "execve"
	case 60:
		return "exit"
	case 61:
		return "wait4"
	case 62:
		return "kill"
	case 78:
		return "getdents64"
	case 79:
		return "getcwd"
	case 80:
		return "chdir"
	case 82:
		return "rename"
	case 85:
		return "creat"
	case 89:
		return "readlink"
	case 96:
		return "gettimeofday"
	case 202:
		return "futex"
	case 217:
		return "getdents"
	case 228:
		return "clock_gettime"
	case 230:
		return "clock_nanosleep"
	case 231:
		return "exit_group"
	case 232:
		return "epoll_wait"
	case 233:
		return "epoll_ctl"
	case 234:
		return "tgkill"
	case 257:
		return "openat"
	case 262:
		return "newfstatat"
	case 281:
		return "socketpair"
	case 291:
		return "epoll_create1"
	case 318:
		return "getrandom"
	case 332:
		return "statx"
	case 334:
		return "rseq"
	default:
		return fmt.Sprintf("syscall_%d", nr)
	}
}
