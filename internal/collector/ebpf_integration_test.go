//go:build integration
// +build integration

// Integration test for the eBPF collector against a real, spawned process.
//
// This test exercises the actual kernel probe path: it forks `sleep 5`,
// attaches the collector to that PID, opens a Stream channel, and verifies
// that at least one syscall trace is captured within 5 seconds.
//
// Run with:
//   sudo -E go test -v -run TestEBPFIntegration -count=1 ./internal/collector/
//
// Skipped (not failed) when:
//   - testing.Short() is true
//   - we are not running as root (os.Getuid() != 0)
//   - the eBPF program fails to load (kernel rejects BPF, no clang output,
//     stack-trace map disabled, etc.) — the collector logs a warning and
//     runs in degraded mode with no kernel events
//
// Without the `integration` build tag this file is excluded from `go test ./...`
// and CI runs.
package collector

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ebpLoadFailureLogSnippet is the substring emitted by NewEBPFCollector when
// loadEBPF() returns an error. Detecting it from the logger output lets us
// decide between "skip gracefully" and "assert traces arrive".
const ebpLoadFailureLogSnippet = "kernel probes unavailable"

// streamBudget is the wall-clock budget for at least one trace to arrive
// after Attach. `sleep 5` makes a handful of syscalls (rt_sigprocmask,
// mmap, clock_nanosleep, futex, exit_group) within the first few hundred ms,
// so 5s is generous headroom for slow CI.
const streamBudget = 5 * time.Second

// TestEBPFIntegration_AttachRealProcess spawns a real `sleep` process,
// attaches the eBPF collector to its PID, opens a Stream channel, and
// verifies that the ring buffer receives at least one syscall trace.
func TestEBPFIntegration_AttachRealProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eBPF integration test in -short mode")
	}
	if os.Getuid() != 0 {
		t.Skipf("eBPF integration test requires root (current uid=%d); re-run with sudo", os.Getuid())
	}

	// Capture collector logs so we can tell the difference between
	// "eBPF loaded successfully" and "eBPF unavailable, degraded mode".
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// 1) Build the collector. NewEBPFCollector never returns an error from
	//    eBPF load failure — it logs a warning and runs without kernel probes.
	c, err := NewEBPFCollector(100000, 50, logger)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// 2) Spawn a real process. `sleep 5` issues a few syscalls (clock_nanosleep,
	//    futex, mmap, rt_sigprocmask, exit_group) — plenty for the raw_syscalls
	//    tracepoints to fire and our filter to deliver events.
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	// Defer Kill+Wait first so cleanup runs even on test failure; the
	// collector Close() above runs after t.Cleanup LIFO, which is fine.
	t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	pid := int32(cmd.Process.Pid)
	t.Logf("spawned sleep PID=%d", pid)

	// 3) Attach the collector to the spawned PID.
	attachCtx, attachCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer attachCancel()

	session, err := c.Attach(attachCtx, pid, CollectOptions{
		BufferSize:      100000,
		TLSInterception: false, // `sleep` has no libssl — skip cleanly
	})
	if err != nil {
		t.Fatalf("Attach(pid=%d): %v", pid, err)
	}
	if session == nil || session.ID == "" {
		t.Fatalf("Attach returned empty session")
	}
	t.Logf("attached session=%s pid=%d", session.ID, session.AgentPID)

	// Always detach and close on test exit.
	t.Cleanup(func() {
		detachCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.Detach(detachCtx, session.ID); err != nil {
			t.Logf("cleanup Detach: %v", err)
		}
	})

	// 4) Decide whether eBPF actually loaded. If not, we cannot capture any
	//    real kernel traces — skip with a descriptive message rather than
	//    hanging until the stream budget elapses.
	if strings.Contains(logBuf.String(), ebpLoadFailureLogSnippet) {
		t.Skipf("eBPF program failed to load on this kernel — skipping real-process test.\n"+
			"collector log:\n%s\n"+
			"hint: regenerate BPF objects with `go generate ./internal/collector/` "+
			"and ensure the running kernel supports all declared BPF map types.",
			logBuf.String())
	}

	// 5) Open a stream channel. Stream() launches a goroutine that pops from
	//    the ring buffer and forwards to the channel until the context is
	//    cancelled.
	streamCtx, streamCancel := context.WithCancel(context.Background())
	t.Cleanup(streamCancel)

	ch, err := c.Stream(streamCtx, session.ID)
	if err != nil {
		t.Fatalf("Stream(session=%s): %v", session.ID, err)
	}

	// 6) Read at least one trace within the budget. We also confirm the
	//    session is visible via List() and that the ring buffer reports
	//    non-zero usage after capture.
	deadline := time.After(streamBudget)
	var captured []byte // first trace ID for reporting

	select {
	case trace, ok := <-ch:
		if !ok {
			t.Fatalf("stream channel closed before any trace arrived")
		}
		if trace.PID != pid {
			t.Errorf("trace.PID = %d, want %d", trace.PID, pid)
		}
		if trace.Syscall == "" {
			t.Errorf("trace.Syscall is empty (parsed incorrectly)")
		}
		if trace.Timestamp.IsZero() {
			t.Errorf("trace.Timestamp is zero")
		}
		if trace.ID == "" {
			t.Errorf("trace.ID is empty")
		}
		captured = []byte(trace.ID)
		t.Logf("captured trace: pid=%d syscall=%s id=%s duration=%s",
			trace.PID, trace.Syscall, trace.ID, trace.Duration)
	case <-deadline:
		stats := c.BufferStats()
		t.Fatalf("no trace captured within %s — kernel probes did not fire.\n"+
			"ring buffer stats: used=%d/%d dropped=%d",
			streamBudget, stats.Used, stats.Size, stats.Dropped)
	}

	// 7) Sanity check: ring buffer should have at least one consumed trace.
	stats := c.BufferStats()
	if stats.Used == 0 && len(captured) == 0 {
		t.Errorf("ring buffer Used=0 and no trace captured — contradictory state")
	}

	// 8) Session bookkeeping — the session we created should be listed.
	sessions, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == session.ID {
			found = true
			if s.AgentPID != pid {
				t.Errorf("List session.AgentPID = %d, want %d", s.AgentPID, pid)
			}
			if s.Status != "running" {
				t.Errorf("List session.Status = %q, want %q", s.Status, "running")
			}
			break
		}
	}
	if !found {
		t.Errorf("session %s not found in List() output (got %d sessions)", session.ID, len(sessions))
	}
}

// TestEBPFIntegration_AttachDetachLifecycle (INT-002) exercises the full
// attach → detach lifecycle against a real spawned process and verifies
// session bookkeeping: the session appears in List() while attached and is
// removed (or marked closed/detached) after Detach.
func TestEBPFIntegration_AttachDetachLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eBPF integration test in -short mode")
	}
	if os.Getuid() != 0 {
		t.Skipf("eBPF integration test requires root (current uid=%d); re-run with sudo", os.Getuid())
	}

	// Capture collector logs so we can skip gracefully when the eBPF
	// program fails to load on this kernel (degraded mode).
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// 1) Build the collector.
	c, err := NewEBPFCollector(100000, 50, logger)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// 2) Spawn a real process. 3s is enough for the attach/detach cycle
	//    and keeps the integration suite fast.
	cmd := exec.Command("sleep", "3")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	pid := int32(cmd.Process.Pid)
	t.Logf("spawned sleep PID=%d", pid)

	// 3) Attach the collector to the spawned PID.
	attachCtx, attachCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer attachCancel()

	session, err := c.Attach(attachCtx, pid, CollectOptions{
		BufferSize:      100000,
		TLSInterception: false, // `sleep` has no libssl — skip cleanly
	})
	if err != nil {
		t.Fatalf("Attach(pid=%d): %v", pid, err)
	}
	if session == nil || session.ID == "" {
		t.Fatalf("Attach returned empty session")
	}
	t.Logf("attached session=%s pid=%d", session.ID, session.AgentPID)

	// Best-effort detach on test exit in case an assertion below fails
	// before the explicit Detach call. A second Detach on an already
	// removed session returns ErrSessionNotFound — log and ignore.
	t.Cleanup(func() {
		detachCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.Detach(detachCtx, session.ID); err != nil {
			t.Logf("cleanup Detach (already detached?): %v", err)
		}
	})

	// 4) If the eBPF program failed to load, session bookkeeping still
	//    works but the kernel probe path was never exercised — skip
	//    gracefully like INT-001 rather than asserting a degraded run.
	if strings.Contains(logBuf.String(), ebpLoadFailureLogSnippet) {
		t.Skipf("eBPF program failed to load on this kernel — skipping attach/detach lifecycle test.\n"+
			"collector log:\n%s\n"+
			"hint: regenerate BPF objects with `go generate ./internal/collector/` "+
			"and ensure the running kernel supports all declared BPF map types.",
			logBuf.String())
	}

	// 5) The session must be visible via List() with status "running".
	sessions, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List after Attach: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == session.ID {
			found = true
			if s.AgentPID != pid {
				t.Errorf("List session.AgentPID = %d, want %d", s.AgentPID, pid)
			}
			if s.Status != "running" {
				t.Errorf("List session.Status = %q, want %q", s.Status, "running")
			}
			break
		}
	}
	if !found {
		t.Fatalf("session %s not found in List() output after Attach (got %d sessions)",
			session.ID, len(sessions))
	}

	// 6) Detach the session.
	detachCtx, detachCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer detachCancel()
	if err := c.Detach(detachCtx, session.ID); err != nil {
		t.Fatalf("Detach(session=%s): %v", session.ID, err)
	}

	// 7) Session cleanup: List() must no longer return the session, or
	//    must report it with a terminal status ("closed"/"detached").
	sessions, err = c.List(context.Background())
	if err != nil {
		t.Fatalf("List after Detach: %v", err)
	}
	for _, s := range sessions {
		if s.ID == session.ID {
			switch s.Status {
			case "closed", "detached", "completed":
				t.Logf("session %s still listed with terminal status %q — acceptable", session.ID, s.Status)
			default:
				t.Errorf("session %s still listed after Detach with non-terminal status %q", session.ID, s.Status)
			}
		}
	}
	t.Logf("session %s detached; %d sessions remain in List()", session.ID, len(sessions))

	// 8) A second Detach on the removed session must fail — guards against
	//    silently accepting cleanup of unknown sessions.
	if err := c.Detach(detachCtx, session.ID); err == nil {
		t.Errorf("second Detach(session=%s) returned nil error; expected session-not-found", session.ID)
	}
}

// TestEBPFIntegration_AttachNonRootPID checks that Attach rejects an obviously
// invalid PID even when running as root. This guards the integration suite
// against silently passing on a broken /proc.
func TestEBPFIntegration_AttachNonRootPID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eBPF integration test in -short mode")
	}
	if os.Getuid() != 0 {
		t.Skipf("requires root (uid=%d)", os.Getuid())
	}

	c, err := NewEBPFCollector(1000, 10, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// PID 1 is init / always exists. Use a clearly-invalid PID instead so
	// the error path is exercised regardless of host setup.
	const bogusPID int32 = 0x7ffffff0
	_, err = c.Attach(context.Background(), bogusPID, CollectOptions{})
	if err == nil {
		t.Errorf("Attach(bogusPID=%d) returned nil error; expected ErrProcessNotFound", bogusPID)
	}
}
