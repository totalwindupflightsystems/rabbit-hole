package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/attach"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/express"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// startTestDaemon starts an in-process Rabbit-Hole daemon (SQLite store +
// degraded collector + express server with a session manager) on a random
// port and points RABBITHOLE_DB_PATH / RABBITHOLE_LISTEN_ADDR at it. The
// CLI commands under test read those env vars at execution time, so each
// cobra invocation behaves like a separate CLI process talking to a
// running `rabbit-hole serve`.
func startTestDaemon(t *testing.T) (*express.Server, *storage.SQLiteStore) {
	t.Helper()

	dataDir := t.TempDir()
	dbPath := dataDir + "/rh.db"

	store, err := storage.NewSQLiteStore(dbPath, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Real collector — degraded on unprivileged hosts, session bookkeeping
	// works either way.
	coll, err := collector.NewEBPFCollector(0, 0, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}

	server := express.NewServer(store, nil, "127.0.0.1:0", nil)
	server.RegisterSessionManager(attach.NewSessionManager(coll, store, nil))
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	t.Setenv("RABBITHOLE_DATA_DIR", dataDir)
	t.Setenv("RABBITHOLE_DB_PATH", dbPath)
	t.Setenv("RABBITHOLE_LISTEN_ADDR", server.Addr())

	return server, store
}

// executeCLI runs a cobra command with the given args, capturing stdout and
// returning any Execute error — the CLI commands here behave like separate
// invocations against the test daemon.
func executeCLI(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var (
		out    string
		runErr error
	)
	out = captureStdout(func() {
		cmd.SetArgs(args)
		runErr = cmd.Execute()
	})
	return out, runErr
}

func extractSessionID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "Session ID: "); ok {
			return rest
		}
	}
	t.Fatalf("no 'Session ID: ' line in output:\n%s", out)
	return ""
}

// TestCLI_AttachListStatusDetach_AcrossInvocations is the DF-001 acceptance
// flow: attach (persists via the daemon), then list --all, status, and
// GET /api/v1/sessions from SEPARATE CLI invocations all see the session,
// and detach completes it.
func TestCLI_AttachListStatusDetach_AcrossInvocations(t *testing.T) {
	server, store := startTestDaemon(t)
	pid := int32(os.Getpid())

	// 1. attach — separate CLI invocation, hands the session to the daemon.
	out, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", pid), "--no-ebpf")
	if err != nil {
		t.Fatalf("attach failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Session ID: ") {
		t.Fatalf("attach output missing Session ID:\n%s", out)
	}
	if !strings.Contains(out, "Session persisted") {
		t.Errorf("attach output missing persistence note:\n%s", out)
	}
	sessionID := extractSessionID(t, out)

	// 2. The session row exists in the DB immediately (the daemon persisted it).
	sess, err := store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("store.GetSession: %v", err)
	}
	if sess.Status != types.SessionStatusRunning {
		t.Errorf("DB status: got %q, want running", sess.Status)
	}

	// 3. list --all — separate invocation, reads the DB.
	out, err = executeCLI(t, newListCmd(), "--all")
	if err != nil {
		t.Fatalf("list --all failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, sessionID[:12]) {
		t.Errorf("list --all output missing session %q:\n%s", sessionID[:12], out)
	}
	if !strings.Contains(out, "1 active") {
		t.Errorf("list --all output missing active count:\n%s", out)
	}

	// 4. status — separate invocation, active count comes from the DB.
	out, err = executeCLI(t, newStatusCmd())
	if err != nil {
		t.Fatalf("status failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "(1 active, ") {
		t.Errorf("status output missing active count:\n%s", out)
	}

	// 5. GET /api/v1/sessions — the API sees the persisted session.
	listResp, err := http.Get("http://" + server.Addr() + "/api/v1/sessions?limit=100")
	if err != nil {
		t.Fatalf("GET /api/v1/sessions: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/sessions: status %d", listResp.StatusCode)
	}
	var apiList struct {
		Sessions []types.SessionSummary `json:"sessions"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&apiList); err != nil {
		t.Fatalf("decode API list: %v", err)
	}
	var apiFound bool
	for _, s := range apiList.Sessions {
		if s.ID == sessionID && s.Status == types.SessionStatusRunning {
			apiFound = true
		}
	}
	if !apiFound {
		t.Errorf("session %q not running in GET /api/v1/sessions", sessionID)
	}

	// 6. detach — separate invocation, completes the session via the daemon.
	out, err = executeCLI(t, newDetachCmd(), sessionID)
	if err != nil {
		t.Fatalf("detach failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Detached session "+sessionID) {
		t.Errorf("detach output missing confirmation:\n%s", out)
	}

	// 7. DB: session completed with an end time.
	sess, err = store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("store.GetSession after detach: %v", err)
	}
	if sess.Status != types.SessionStatusCompleted {
		t.Errorf("DB status after detach: got %q, want completed", sess.Status)
	}
	if sess.EndTime == nil {
		t.Error("DB EndTime after detach is nil")
	}

	// 8. list without --all → "No active sessions." (AC5 preserved).
	out, err = executeCLI(t, newListCmd())
	if err != nil {
		t.Fatalf("list failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "No active sessions.") {
		t.Errorf("list output after detach:\n%s", out)
	}
}

// TestCLI_AttachSamePIDTwice_SecondFailsCleanly covers DF-001 AC3: two
// attaches to the same PID must not silently create orphan sessions — the
// daemon collector rejects the duplicate.
func TestCLI_AttachSamePIDTwice_SecondFailsCleanly(t *testing.T) {
	_, store := startTestDaemon(t)
	pid := int32(os.Getpid())

	out, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", pid), "--no-ebpf")
	if err != nil {
		t.Fatalf("first attach failed: %v\noutput:\n%s", err, out)
	}
	firstID := extractSessionID(t, out)

	out, err = executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", pid), "--no-ebpf")
	if err == nil {
		t.Fatalf("second attach to same PID should fail; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "already attached") {
		t.Errorf("second attach error = %q, want 'already attached'", err.Error())
	}

	// Exactly one session row exists for the PID — no silent orphan.
	sessions, err := store.ListSessions(context.Background(), 0, listSessionLimit)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var forPID int
	for _, s := range sessions {
		if s.AgentPID == pid {
			forPID++
		}
	}
	if forPID != 1 {
		t.Errorf("expected exactly 1 session for PID %d, got %d", pid, forPID)
	}
	if sessions[0].ID != firstID {
		t.Errorf("persisted session id = %q, want %q", sessions[0].ID, firstID)
	}
}

// TestCLI_DetachUnknownSession_ReturnsNotFound ensures detach of a session
// that was never attached fails cleanly (and does NOT silently succeed).
func TestCLI_DetachUnknownSession_ReturnsNotFound(t *testing.T) {
	startTestDaemon(t)

	out, err := executeCLI(t, newDetachCmd(), "does-not-exist")
	if err == nil {
		t.Fatalf("detach of unknown session should fail; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("detach error = %q, want 'not found'", err.Error())
	}
}

// TestCLI_AttachWithoutDaemon_FailsCleanly ensures attach explains that a
// daemon must be running instead of silently doing nothing (the DF-001
// failure mode: session created and lost).
func TestCLI_AttachWithoutDaemon_FailsCleanly(t *testing.T) {
	// Reserve a port and release it — nothing is listening there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	dataDir := t.TempDir()
	t.Setenv("RABBITHOLE_DATA_DIR", dataDir)
	t.Setenv("RABBITHOLE_DB_PATH", dataDir+"/rh.db")
	t.Setenv("RABBITHOLE_LISTEN_ADDR", addr)

	out, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", os.Getpid()), "--no-ebpf")
	if err == nil {
		t.Fatalf("attach without daemon should fail; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") {
		t.Errorf("attach error = %q, want daemon-unreachable message", err.Error())
	}
}

// TestCLI_AttachAndDetachAll exercises `detach --all` against sessions
// attached by separate invocations.
func TestCLI_AttachAndDetachAll(t *testing.T) {
	_, store := startTestDaemon(t)
	pid := int32(os.Getpid())

	out, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", pid), "--no-ebpf")
	if err != nil {
		t.Fatalf("attach failed: %v\noutput:\n%s", err, out)
	}
	sessionID := extractSessionID(t, out)

	out, err = executeCLI(t, newDetachCmd(), "--all")
	if err != nil {
		t.Fatalf("detach --all failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Detached session "+sessionID) {
		t.Errorf("detach --all output missing session:\n%s", out)
	}

	sess, err := store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("store.GetSession: %v", err)
	}
	if sess.Status != types.SessionStatusCompleted {
		t.Errorf("status after detach --all: got %q, want completed", sess.Status)
	}
}
