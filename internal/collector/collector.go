// Package collector provides eBPF-based kernel telemetry collection for agent process monitoring.

package collector

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cilium/ebpf/link"
	"github.com/google/uuid"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// Collector interface from S02 §1.
type Collector interface {
	Attach(ctx context.Context, pid int32, opts CollectOptions) (*types.Session, error)
	Detach(ctx context.Context, sessionID string) error
	List(ctx context.Context) ([]types.Session, error)
	Stream(ctx context.Context, sessionID string) (<-chan types.Trace, error)
	Health(ctx context.Context) error
	// PreflightEBPF reports whether kernel telemetry is available.
	// Commands that promise kernel telemetry (attach, serve) must call
	// this and hard-fail when it errors; session-management commands
	// (status, list, detach) must not.
	PreflightEBPF() error
}

// CollectOptions configures collection behavior.
type CollectOptions struct {
	ContextWindows  bool
	TraceCategories []types.TraceCategory
	BufferSize      int
	TLSInterception bool
}

// DefaultCollectOptions returns sensible defaults.
func DefaultCollectOptions() CollectOptions {
	return CollectOptions{
		BufferSize:      100000,
		TLSInterception: true,
	}
}

// BufferStats returns ring buffer statistics.
func (c *eBPFCollector) BufferStats() BufferStats {
	return c.ringBuffer.Stats()
}

var _ Collector = (*eBPFCollector)(nil)

// Attach starts tracing a process by PID.
func (c *eBPFCollector) Attach(ctx context.Context, pid int32, opts CollectOptions) (*types.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.sessions) >= c.maxSessions {
		return nil, types.ErrMaxSessionsReached{Max: c.maxSessions}
	}

	for _, cs := range c.sessions {
		if cs.pid == pid && cs.attached {
			return nil, types.ErrAlreadyAttached{PID: pid}
		}
	}

	if !processExists(pid) {
		return nil, types.ErrProcessNotFound{PID: pid}
	}

	sessionID := uuid.Must(uuid.NewV7()).String()
	session := &types.Session{
		ID:        sessionID,
		AgentPID:  pid,
		AgentName: detectAgentName(pid),
		StartTime: time.Now(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: readCmdline(pid),
			WorkDir:     readCwd(pid),
			BinaryPath:  readExe(pid),
		},
	}

	// Add PID to eBPF filter map (best-effort — skipped if eBPF not loaded)
	if c.objs.PidFilter != nil {
		key := uint32(0)
		if err := c.objs.PidFilter.Put(&key, &pid); err != nil {
			return nil, fmt.Errorf("ebpf: add PID to filter: %w", err)
		}

		tpLinks, err := c.attachSyscallProbes()
		if err != nil {
			c.objs.PidFilter.Delete(&key)
			return nil, fmt.Errorf("ebpf: attach syscall probes: %w", err)
		}
		c.links = append(c.links, tpLinks...)

		if opts.TLSInterception {
			tlsLinks, tlsErr := c.attachTLSProbes(pid)
			if tlsErr != nil {
				c.logger.Warn("ebpf: TLS probes unavailable, continuing without TLS", "err", tlsErr)
			} else {
				c.links = append(c.links, tlsLinks...)
			}
		}
	}

	cs := &collectionSession{
		pid:       pid,
		attached:  true,
		createdAt: time.Now(),
	}
	c.sessions[sessionID] = cs

	return session, nil
}

// Detach stops tracing a session.
func (c *eBPFCollector) Detach(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cs, ok := c.sessions[sessionID]
	if !ok {
		return types.ErrSessionNotFound{SessionID: sessionID}
	}

	if c.objs.PidFilter != nil {
		key := uint32(0)
		if err := c.objs.PidFilter.Delete(&key); err != nil {
			c.logger.Warn("ebpf: failed to clear PID filter", "err", err)
		}
	}

	for _, l := range c.links {
		l.Close()
	}
	c.links = nil

	cs.attached = false
	delete(c.sessions, sessionID)
	return nil
}

// List returns all active sessions.
func (c *eBPFCollector) List(ctx context.Context) ([]types.Session, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]types.Session, 0, len(c.sessions))
	for id, cs := range c.sessions {
		out = append(out, types.Session{
			ID:        id,
			AgentPID:  cs.pid,
			AgentName: detectAgentName(cs.pid),
			StartTime: cs.createdAt,
			Status:    sessionStatus(cs),
			Metadata: types.SessionMetadata{
				CommandLine: readCmdline(cs.pid),
				WorkDir:     readCwd(cs.pid),
				BinaryPath:  readExe(cs.pid),
			},
		})
	}
	return out, nil
}

// Stream returns a channel of trace events for a session.
func (c *eBPFCollector) Stream(ctx context.Context, sessionID string) (<-chan types.Trace, error) {
	c.mu.RLock()
	_, ok := c.sessions[sessionID]
	c.mu.RUnlock()
	if !ok {
		return nil, types.ErrSessionNotFound{SessionID: sessionID}
	}

	ch := make(chan types.Trace, 128)
	go func() {
		defer close(ch)
		for {
			trace, err := c.ringBuffer.Pop(ctx)
			if err != nil {
				return
			}
			select {
			case ch <- trace:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// Health checks if the ring buffer is healthy.
func (c *eBPFCollector) Health(ctx context.Context) error {
	stats := c.ringBuffer.Stats()
	if stats.Dropped > 100000 {
		return fmt.Errorf("ring buffer overflow: %d traces dropped", stats.Dropped)
	}
	return nil
}

// ---------- Probe attachment ----------

func (c *eBPFCollector) attachSyscallProbes() ([]link.Link, error) {
	var links []link.Link

	enterLink, err := link.Tracepoint("raw_syscalls", "sys_enter", c.objs.TraceEnterSyscall, nil)
	if err != nil {
		for _, l := range links {
			l.Close()
		}
		return nil, fmt.Errorf("attach sys_enter: %w", err)
	}
	links = append(links, enterLink)

	exitLink, err := link.Tracepoint("raw_syscalls", "sys_exit", c.objs.TraceExitSyscall, nil)
	if err != nil {
		for _, l := range links {
			l.Close()
		}
		return nil, fmt.Errorf("attach sys_exit: %w", err)
	}
	links = append(links, exitLink)

	return links, nil
}

func (c *eBPFCollector) attachTLSProbes(pid int32) ([]link.Link, error) {
	libSSL, err := findLibSSL(pid)
	if err != nil {
		return nil, fmt.Errorf("find libssl: %w", err)
	}

	sslExe, err := link.OpenExecutable(libSSL)
	if err != nil {
		return nil, fmt.Errorf("open libssl: %w", err)
	}

	var links []link.Link
	opts := &link.UprobeOptions{PID: int(pid)}

	readU, err := sslExe.Uprobe("SSL_read", c.objs.UprobeSslRead, opts)
	if err != nil {
		return nil, fmt.Errorf("uprobe SSL_read: %w", err)
	}
	links = append(links, readU)

	writeU, err := sslExe.Uprobe("SSL_write", c.objs.UprobeSslWrite, opts)
	if err != nil {
		readU.Close()
		return nil, fmt.Errorf("uprobe SSL_write: %w", err)
	}
	links = append(links, writeU)

	readUR, err := sslExe.Uretprobe("SSL_read", c.objs.UretprobeSslRead, opts)
	if err != nil {
		for _, l := range links {
			l.Close()
		}
		return nil, fmt.Errorf("uretprobe SSL_read: %w", err)
	}
	links = append(links, readUR)

	writeUR, err := sslExe.Uretprobe("SSL_write", c.objs.UretprobeSslWrite, opts)
	if err != nil {
		for _, l := range links {
			l.Close()
		}
		return nil, fmt.Errorf("uretprobe SSL_write: %w", err)
	}
	links = append(links, writeUR)

	return links, nil
}

// ---------- /proc helpers ----------

func processExists(pid int32) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

func detectAgentName(pid int32) string {
	exe := readExe(pid)
	if exe == "" {
		cmdline := readCmdline(pid)
		if cmdline != "" {
			parts := strings.Fields(cmdline)
			if len(parts) > 0 {
				return parts[0]
			}
		}
		return fmt.Sprintf("pid-%d", pid)
	}
	return exe
}

func readCmdline(pid int32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(data), "\x00", " ")
}

func readCwd(pid int32) string {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return link
}

func readExe(pid int32) string {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return link
}

func findLibSSL(pid int32) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return "", fmt.Errorf("read /proc/%d/maps: %w", pid, err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, "/libssl.so") && !strings.Contains(line, "libssl.so.") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.Contains(f, "/libssl.so") && !strings.Contains(f, "libssl.so.") {
					return f, nil
				}
			}
		}
	}
	return "", fmt.Errorf("libssl not found in /proc/%d/maps", pid)
}

func sessionStatus(cs *collectionSession) types.SessionStatus {
	if cs.attached {
		return types.SessionStatusRunning
	}
	return types.SessionStatusCompleted
}
