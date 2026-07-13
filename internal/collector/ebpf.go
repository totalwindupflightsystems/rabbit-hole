// Package collector implements the eBPF-based trace collection layer.
//
// eBPF programs are compiled from collector.bpf.c via bpf2go code generation.
// Run `go generate ./internal/collector/` after installing clang and kernel headers.
package collector

//go:generate bpf2go -cc clang -cflags "-O2 -g -Wall -Werror" -target amd64 bpf collector.bpf.c -- -I/usr/include/x86_64-linux-gnu

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/perf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/google/uuid"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// bpfObjects mirrors the eBPF maps and programs defined in collector.bpf.c.
// The struct tags (ebpf:"...") must match the SECTION names in the C source.
// bpf2go uses this struct to validate loaded programs against their expected names.
type bpfObjects struct {
	TraceEnterSyscall *ebpf.Program `ebpf:"trace_enter_syscall"`
	TraceExitSyscall  *ebpf.Program `ebpf:"trace_exit_syscall"`

	TraceConnect *ebpf.Program `ebpf:"trace_connect"`
	TraceSendMsg *ebpf.Program `ebpf:"trace_sendmsg"`
	TraceRecvMsg *ebpf.Program `ebpf:"trace_recvmsg"`

	TraceOpenAt *ebpf.Program `ebpf:"trace_openat"`
	TraceRead   *ebpf.Program `ebpf:"trace_read"`
	TraceWrite  *ebpf.Program `ebpf:"trace_write"`
	TraceClose  *ebpf.Program `ebpf:"trace_close"`

	TraceExec *ebpf.Program `ebpf:"trace_exec"`
	TraceExit *ebpf.Program `ebpf:"trace_exit"`
	TraceFork *ebpf.Program `ebpf:"trace_fork"`

	Events    *ebpf.Map `ebpf:"events"`
	FilterMap *ebpf.Map `ebpf:"pid_filter"`

	UprobeSSLRead     *ebpf.Program `ebpf:"uprobe_ssl_read"`
	UprobeSSLWrite    *ebpf.Program `ebpf:"uprobe_ssl_write"`
	UretprobeSSLRead  *ebpf.Program `ebpf:"uretprobe_ssl_read"`
	UretprobeSSLWrite *ebpf.Program `ebpf:"uretprobe_ssl_write"`
}

// Close releases all eBPF resources held by this object collection.
func (o *bpfObjects) Close() error {
	var errs []error
	closeIf := func(c io.Closer) {
		if c != nil {
			if err := c.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	closeIf(o.TraceEnterSyscall)
	closeIf(o.TraceExitSyscall)
	closeIf(o.TraceConnect)
	closeIf(o.TraceSendMsg)
	closeIf(o.TraceRecvMsg)
	closeIf(o.TraceOpenAt)
	closeIf(o.TraceRead)
	closeIf(o.TraceWrite)
	closeIf(o.TraceClose)
	closeIf(o.TraceExec)
	closeIf(o.TraceExit)
	closeIf(o.TraceFork)
	closeIf(o.UprobeSSLRead)
	closeIf(o.UprobeSSLWrite)
	closeIf(o.UretprobeSSLRead)
	closeIf(o.UretprobeSSLWrite)
	closeIf(o.Events)
	closeIf(o.FilterMap)
	if len(errs) > 0 {
		return fmt.Errorf("close bpfObjects: %v", errs)
	}
	return nil
}

// eBPFCollector attaches eBPF programs to a target agent process and captures
// kernel-level telemetry (syscalls, file ops, network calls) via the perf
// event ring buffer.
type eBPFCollector struct {
	objs     bpfObjects
	links    []link.Link
	sessions map[string]*collectionSession
	mu       sync.RWMutex

	ringBuffer  *RingBuffer
	perfReader  *perf.Reader
	maxSessions int
	logger      *slog.Logger
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
// If eBPF loading fails (bpf2go not run, no clang, missing kernel support),
// the collector operates in degraded mode — ring buffer and session tracking
// work, but no kernel events arrive.
//
// Parameters:
//   - bufSize: ring buffer capacity (0 = default 100000)
//   - maxSessions: maximum concurrent sessions (0 = default 50)
//   - logger: structured logger (uses slog.Default if nil)
func NewEBPFCollector(bufSize, maxSessions int, logger *slog.Logger) (*eBPFCollector, error) {
	if logger == nil {
		logger = slog.Default()
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
	}

	// Best-effort eBPF loading. If bpf2go hasn't been run or the kernel
	// lacks BPF support, operate without kernel probes.
	if err := c.loadEBPF(); err != nil {
		logger.Warn("ebpf: kernel probes unavailable, running without eBPF",
			"err", err,
			"hint", "run 'go generate ./internal/collector/' with clang and kernel headers installed",
		)
	}

	return c, nil
}

// loadEBPF attempts to load eBPF programs and start the perf event reader.
func (c *eBPFCollector) loadEBPF() error {
	if err := rlimit.RemoveMemlock(); err != nil {
		c.logger.Warn("ebpf: remove memlock rlimit failed, continuing without eBPF",
			"err", err,
			"hint", "may need CAP_SYS_RESOURCE or kernel 5.11+",
		)
		return fmt.Errorf("remove memlock: %w", err)
	}

	var objs bpfObjects
	opts := &ebpf.CollectionOptions{
		Programs: ebpf.ProgramOptions{
			LogLevel: ebpf.LogLevelBranch,
		},
	}
	if err := loadBpfObjects(&objs, opts); err != nil {
		return fmt.Errorf("load bpf objects: %w", err)
	}

	if objs.Events == nil {
		objs.Close()
		return fmt.Errorf("events map not found in bpf objects")
	}
	if objs.FilterMap == nil {
		objs.Close()
		return fmt.Errorf("pid_filter map not found in bpf objects")
	}

	rd, err := perf.NewReader(objs.Events, c.bufSize())
	if err != nil {
		objs.Close()
		return fmt.Errorf("create perf reader: %w", err)
	}

	c.objs = objs
	c.perfReader = rd

	go c.readEvents()
	return nil
}

func (c *eBPFCollector) bufSize() int {
	return c.ringBuffer.Stats().Size
}

// readEvents runs in a background goroutine, reading raw events from the
// perf reader, parsing them into types.Trace values, and pushing them
// into the ring buffer.
func (c *eBPFCollector) readEvents() {
	for {
		record, err := c.perfReader.Read()
		if err != nil {
			if err == perf.ErrClosed {
				return
			}
			c.logger.Error("ebpf: perf reader error", "err", err)
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

	if c.perfReader != nil {
		c.perfReader.Close()
	}

	for _, l := range c.links {
		l.Close()
	}

	return c.objs.Close()
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

// loadBpfObjects is a stub replaced by bpf2go code generation.
var loadBpfObjects = func(objs *bpfObjects, opts *ebpf.CollectionOptions) error {
	return fmt.Errorf("ebpf: bpf2go code generation not run — run 'go generate ./internal/collector/' with clang and kernel headers installed")
}

var _ = unsafe.Sizeof(0)
