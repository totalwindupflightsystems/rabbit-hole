// Package attach provides session attachment and lifecycle management for eBPF-based agent monitoring.

package attach

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// ---------- Process-exit observation ----------
//
// In degraded mode (--no-ebpf) the collector never emits process-exit
// events, so the daemon has no signal that an attached process died —
// its session stayed 'running' forever (DF-028). The monitor below
// observes tracked PIDs directly and transitions the session to an
// honest terminal state: exit code 0 -> completed, nonzero exit or
// signal death -> crashed.
//
// The exit STATUS of a non-child process is normally only readable by
// its real parent (wait* syscalls, ECHILD for everyone else). We use
// pidfd_open(2) + waitid(2) with P_PIDFD (kernel >= 5.4) to observe the
// process like a supervisor would: the pidfd pins the exact process (no
// PID-reuse races) and P_PIDFD allows waiting on non-children with
// sufficient ptrace access. WNOWAIT reads the status WITHOUT reaping, so
// the process's real parent still sees a valid wait result — we never
// steal its zombie. When pidfd is unavailable (old kernel, seccomp,
// EPERM), the monitor falls back to /proc polling: the exit is detected
// but the status is unknown, and the session is labelled completed (the
// false-'crashed' failure mode is strictly worse for a legibility
// product).

const (
	// pidfdOpenSyscall is pidfd_open(2), not exposed in the stdlib
	// syscall package; 434 on both x86_64 and arm64 (generic table).
	pidfdOpenSyscall = 434

	// waitid(2) idtype/options for P_PIDFD waiting.
	waitidPidfd = 3          // P_PIDFD
	waitExited  = 0x0004     // WEXITED
	waitNoHang  = 0x0001     // WNOHANG
	waitNoWait  = 0x01000000 // WNOWAIT — read status, leave the zombie re-able

	// siginfo si_code values reported for an exited process.
	clExited = 1 // CLD_EXITED — normal exit, status is the exit code
	clKilled = 9 // CLD_KILLED — killed by signal
	clDumped = 2 // CLD_DUMPED — killed by signal and dumped core
)

// siginfoWait mirrors the kernel's siginfo_t _wait member as returned by
// waitid(P_PIDFD, ...). Offsets match the generic (x86_64/arm64) uapi
// layout; the struct is exactly 128 bytes, the size the kernel writes.
type siginfoWait struct {
	signo  int32 //  0
	errno  int32 //  4
	code   int32 //  8 — CLD_* above
	_      [4]byte
	pid    int32  // 16
	_uid   uint32 // 20
	status int32  // 24 — exit code (CLD_EXITED) or signal number
	_      [100]byte
}

// pidwatchStatus is a captured exit observation.
type pidwatchStatus struct {
	pid    int32
	code   int32 // CLD_*
	status int32
}

// signalDeath reports whether the capture describes death-by-signal
// rather than a normal exit(2).
func (s pidwatchStatus) signalDeath() bool { return s.code != clExited }

// waitPidfd asks the kernel for the watched process's exit status using
// waitid(P_PIDFD, WEXITED|WNOWAIT|WNOHANG). The call never blocks and
// never reaps. Returns:
//   - status.pid != 0: the process exited and its status was captured.
//   - errno == 0: the process is still running (or in a tiny exit
//     window) — no status yet.
//   - errno == ECHILD: the process exited and was already reaped by its
//     real parent (or the kernel predates P_PIDFD on non-children).
//   - other errno: P_PIDFD unsupported for this process (EPERM, EINVAL,
//     ENOSYS) — the caller should fall back to /proc observation.
func waitPidfd(fd, pid int) (pidwatchStatus, syscall.Errno) {
	var info siginfoWait
	_, _, errno := syscall.Syscall6(
		syscall.SYS_WAITID,
		uintptr(waitidPidfd),
		uintptr(fd),
		uintptr(unsafe.Pointer(&info)),
		uintptr(waitExited|waitNoHang|waitNoWait),
		0, 0,
	)
	if errno != 0 {
		return pidwatchStatus{}, errno
	}
	st := pidwatchStatus{pid: info.pid, code: info.code, status: info.status}
	if st.pid != 0 && st.pid != int32(pid) {
		// Defensive: never trust a status that belongs to a different
		// process (this would mean the pidfd refers to something else).
		return pidwatchStatus{}, syscall.EAGAIN
	}
	return st, 0
}

// procStateAlive reports whether /proc/<pid> names a live (non-zombie)
// process. A zombie has already terminated — only its reap awaits, and a
// missing directory means the process is fully gone. Field 2 (comm) may
// contain spaces and parentheses, so find the LAST ')' before reading
// the state character.
func procStateAlive(pid int32) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 || end+2 >= len(data) {
		return false
	}
	return data[end+2] != 'Z'
}

// pidWatch observes one tracked process.
type pidWatch interface {
	// exitInfo reports whether the watched process has terminated and,
	// when the kernel could tell us, how. exited=true is authoritative
	// (the process is gone: zombie, reaped, or missing from /proc).
	// ok=true means the exit status was captured: code holds the exit
	// code (signalDeath=false) or the signal number (signalDeath=true).
	// When ok=false the status is unavailable (the real parent reaped
	// first, or pidfd is unsupported) — label conservatively.
	exitInfo() (exited bool, code int32, signalDeath, ok bool)
	// close releases the kernel handle (pidfd).
	close()
}

// pidfdWatch is the real pidWatch: a pidfd pinned at track time plus a
// /proc fallback. The pidfd pins the exact process, so a recycled PID
// can never be mistaken for the monitored one.
type pidfdWatch struct {
	pid           int32
	fd            int // pidfd; -1 when unavailable -> /proc-only
	logger        *slog.Logger
	pidfdDisabled bool // latched once P_PIDFD is rejected for this process
}

// openPidfdWatch opens a pidfd for pid, degrading to /proc-only
// observation when the kernel refuses (seccomp, old kernel, EPERM).
func openPidfdWatch(pid int32, logger *slog.Logger) pidWatch {
	w := &pidfdWatch{pid: pid, fd: -1, logger: logger}
	fd, _, errno := syscall.Syscall(pidfdOpenSyscall, uintptr(pid), 0, 0)
	if errno != 0 {
		logger.Debug("session monitor: pidfd_open unavailable, observing via /proc", "pid", pid, "err", errno.Error())
		return w
	}
	w.fd = int(fd)
	return w
}

func (w *pidfdWatch) close() {
	if w.fd >= 0 {
		_ = syscall.Close(w.fd)
		w.fd = -1
	}
}

func (w *pidfdWatch) exitInfo() (exited bool, code int32, signalDeath, ok bool) {
	if w.fd >= 0 && !w.pidfdDisabled {
		st, errno := waitPidfd(w.fd, int(w.pid))
		switch {
		case st.pid != 0:
			// The pinned process exited and we read how.
			return true, st.status, st.signalDeath(), true
		case errno == 0 || errno == syscall.EINTR:
			// The kernel reports no exit for the pinned process yet: it
			// is still running, or the exit is in flight (wait scans
			// lag the actual transition by microseconds). Do NOT trust
			// /proc here — the next tick's waitid either captures the
			// status or reports ECHILD once the process is reaped.
			return false, 0, false, false
		case errno == syscall.ECHILD:
			// Pinned process exited and was reaped before we read the
			// status — unless the kernel predates P_PIDFD on non-children
			// and the process is actually alive. /proc is authoritative.
			if procStateAlive(w.pid) {
				return false, 0, false, false
			}
			return true, 0, false, false
		default:
			// EPERM (no ptrace access) / EINVAL / ENOSYS: P_PIDFD is not
			// usable for this process — latch /proc-only mode.
			w.pidfdDisabled = true
			w.logger.Warn("session monitor: waitid(P_PIDFD) failed, observing via /proc only",
				"pid", w.pid, "err", errno.Error())
			return !procStateAlive(w.pid), 0, false, false
		}
	}
	return !procStateAlive(w.pid), 0, false, false
}

// ---------- The monitor loop ----------

// defaultMonitorInterval bounds the process-exit poll cadence. A 1s poll
// transitions a session within ~1s of the attached process exiting —
// well inside the 60s acceptance window. Tests lower it.
const defaultMonitorInterval = time.Second

// processMonitor polls tracked sessions and transitions any whose
// attached process has exited to a terminal state in storage. It also
// detaches the dead session from the collector so the two views agree.
//
// Each session gets one poll entry in its own lifetime: registered at
// StartSession, forgotten on StopSession (user detach wins) or on
// terminal transition. The loop must NOT open a new watch per poll — the
// watch (and its pidfd) is opened once at track time.
type processMonitor struct {
	coll     collector.Collector
	store    SessionStore
	logger   *slog.Logger
	interval time.Duration

	mu      sync.Mutex
	tracked map[string]pidWatch
	// openWatches is injectable for tests that need deterministic
	// exit states without real processes.
	openWatch func(pid int32, logger *slog.Logger) pidWatch
}

func newProcessMonitor(coll collector.Collector, store SessionStore, logger *slog.Logger) *processMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &processMonitor{
		coll:      coll,
		store:     store,
		logger:    logger,
		interval:  defaultMonitorInterval,
		tracked:   make(map[string]pidWatch),
		openWatch: openPidfdWatch,
	}
}

// track starts observing session's pid. Idempotent per session.
func (m *processMonitor) track(sessionID string, pid int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tracked[sessionID]; exists {
		return
	}
	m.tracked[sessionID] = m.openWatch(pid, m.logger)
}

// untrack stops observing session. Called when the session is completed
// by the user (detach) so the monitor cannot overwrite that outcome.
func (m *processMonitor) untrack(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.tracked[sessionID]; ok {
		delete(m.tracked, sessionID)
		w.close()
	}
}

// run polls tracked sessions until ctx is cancelled, at which point all
// watches are released. It does not finalize anything on shutdown —
// daemon restart reconciliation owns those sessions.
func (m *processMonitor) run(ctx context.Context) {
	interval := m.interval
	if interval <= 0 {
		interval = defaultMonitorInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer m.closeAll()

	m.logger.Info("session monitor: watching attached processes", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("session monitor: shutting down")
			return
		case <-ticker.C:
			m.poll()
		}
	}
}

func (m *processMonitor) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, w := range m.tracked {
		w.close()
		delete(m.tracked, id)
	}
}

func (m *processMonitor) poll() {
	m.mu.Lock()
	snapshot := make(map[string]pidWatch, len(m.tracked))
	for id, w := range m.tracked {
		snapshot[id] = w
	}
	m.mu.Unlock()

	for id, w := range snapshot {
		exited, code, signalDeath, statusKnown := w.exitInfo()
		if !exited {
			continue
		}

		// Forget the session atomically. If a concurrent StopSession
		// already completed it, skip — the user's detach wins.
		m.mu.Lock()
		if _, still := m.tracked[id]; !still {
			m.mu.Unlock()
			continue
		}
		delete(m.tracked, id)
		m.mu.Unlock()
		w.close()

		m.finalize(id, code, signalDeath, statusKnown)
	}
}

// finalize transitions the session to a terminal state with an honest
// label and detaches it from the collector so the collector and storage
// views agree.
func (m *processMonitor) finalize(sessionID string, code int32, signalDeath, statusKnown bool) {
	status := types.SessionStatusCompleted
	switch {
	case statusKnown && (signalDeath || code != 0):
		status = types.SessionStatusCrashed
	case !statusKnown:
		m.logger.Warn("session monitor: exit status not readable (parent reaped first), marking completed",
			"session", sessionID)
	}

	now := time.Now()
	// Detach first: frees the PID in the collector so a concurrent
	// re-attach of the same process is not rejected as a duplicate,
	// mirroring StopSession's detach-then-update order.
	if err := m.coll.Detach(context.Background(), sessionID); err != nil {
		var nf types.ErrSessionNotFound
		if !errors.As(err, &nf) {
			m.logger.Warn("session monitor: detach dead session", "session", sessionID, "err", err)
		}
	}

	if err := m.store.UpdateSession(context.Background(), &types.Session{
		ID:      sessionID,
		Status:  status,
		EndTime: &now,
	}); err != nil {
		m.logger.Warn("session monitor: persist terminal status", "session", sessionID, "err", err)
		return
	}

	attrs := []any{"session", sessionID, "status", status}
	if statusKnown {
		attrs = append(attrs, "exit_code", code, "signal", signalDeath)
	}
	m.logger.Info("session monitor: process exited, session terminal", attrs...)
}
