package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// runDemo executes the demo command with the given args and returns its
// stdout. RABBITHOLE_DATA_DIR points at a temp dir so config.Load never
// touches the real home directory. Used by the --addr daemon-routing tests.
func runDemoArgs(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())

	out := captureStdout(func() {
		cmd := newDemoCmd()
		var cobraOut, cobraErr bytes.Buffer
		cmd.SetOut(&cobraOut)
		cmd.SetErr(&cobraErr)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("demo: %v (stderr: %s)", err, cobraErr.String())
		}
	})
	return out
}

// runDemo executes the demo command with a temp DB and returns its stdout.
// The store writes to RABBITHOLE_DB_PATH (a temp dir), so no real data is
// touched. --flows 1 keeps the seed fast. The demo prints via fmt to
// os.Stdout (not cobra's Out writer), so captureStdout wraps the execution;
// SetOut/SetErr still get buffers to keep cobra's own output contained.
func runDemo(t *testing.T) string {
	t.Helper()
	t.Setenv("RABBITHOLE_DB_PATH", filepath.Join(t.TempDir(), "demo.db"))

	out := captureStdout(func() {
		cmd := newDemoCmd()
		var cobraOut, cobraErr bytes.Buffer
		cmd.SetOut(&cobraOut)
		cmd.SetErr(&cobraErr)
		cmd.SetArgs([]string{"--flows", "1"})
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("demo: %v (stderr: %s)", err, cobraErr.String())
		}
	})
	return out
}

func TestDemoCmd_NextHintUsesDefaultPort(t *testing.T) {
	// An empty RABBITHOLE_LISTEN_ADDR is treated as unset by config.Load,
	// so the default 127.0.0.1:9734 applies regardless of the outer env.
	t.Setenv("RABBITHOLE_LISTEN_ADDR", "")

	out := runDemo(t)

	if !strings.Contains(out, "9734") {
		t.Errorf("output missing default port 9734, got: %q", out)
	}
	if !strings.Contains(out, "/dashboard") {
		t.Errorf("output missing /dashboard path, got: %q", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:9734/dashboard") {
		t.Errorf("output missing full default URL, got: %q", out)
	}
	if strings.Contains(out, ":8080") {
		t.Errorf("output still references stale port 8080, got: %q", out)
	}
}

func TestDemoCmd_NextHintHonorsEnvOverride(t *testing.T) {
	t.Setenv("RABBITHOLE_LISTEN_ADDR", "127.0.0.1:9999")

	out := runDemo(t)

	if !strings.Contains(out, ":9999") {
		t.Errorf("output missing env-override port 9999, got: %q", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:9999/dashboard") {
		t.Errorf("output missing env-override URL, got: %q", out)
	}
	if strings.Contains(out, ":8080") {
		t.Errorf("output still references stale port 8080, got: %q", out)
	}
}

func TestDemoCmd_NextHintReplacesZeroHostWithLocalhost(t *testing.T) {
	t.Setenv("RABBITHOLE_LISTEN_ADDR", "0.0.0.0:9734")

	out := runDemo(t)

	if !strings.Contains(out, "http://localhost:9734/dashboard") {
		t.Errorf("output missing localhost-substituted URL, got: %q", out)
	}
	if strings.Contains(out, "0.0.0.0") {
		t.Errorf("output still contains unreachable 0.0.0.0 host, got: %q", out)
	}
}

// TestNewDemoCmd_AddrFlag verifies the GAP-007 --addr flag is registered with
// the same default/help contract as status/attach.
func TestNewDemoCmd_AddrFlag(t *testing.T) {
	cmd := newDemoCmd()

	f := cmd.Flags().Lookup("addr")
	if f == nil {
		t.Fatal("flag --addr not registered")
	}
	if f.DefValue != "" {
		t.Errorf("flag --addr default = %q, want %q", f.DefValue, "")
	}
	if !strings.Contains(f.Usage, "RABBITHOLE_LISTEN_ADDR") {
		t.Errorf("--addr help should mention RABBITHOLE_LISTEN_ADDR fallback, got: %q", f.Usage)
	}
}

// TestDemoCmd_AddrRoutesToDaemon verifies that --addr sends the seed request
// to POST /api/v1/demo/seed on the daemon (GAP-007), prints the seeded
// session ID and the dashboard hint, and never opens a local DB.
func TestDemoCmd_AddrRoutesToDaemon(t *testing.T) {
	var (
		gotPath     string
		gotMethod   string
		gotFlows    int
		gotHours    int
		gotSpread   bool
		serverCalls int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		gotPath = r.URL.Path
		gotMethod = r.Method
		var body struct {
			Flows     int  `json:"flows"`
			HoursBack int  `json:"hours_back"`
			Spread    bool `json:"spread"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotFlows = body.Flows
		gotHours = body.HoursBack
		gotSpread = body.Spread
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"session_id": "0192-demo-seed-id"})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	out := runDemoArgs(t, "--addr", addr, "--flows", "5")

	if gotPath != "/api/v1/demo/seed" {
		t.Errorf("path = %q, want /api/v1/demo/seed", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotFlows != 5 {
		t.Errorf("flows = %d, want 5", gotFlows)
	}
	if gotHours != 3 {
		t.Errorf("hours_back = %d, want default 3", gotHours)
	}
	if gotSpread {
		t.Errorf("spread = true, want default false")
	}
	if !strings.Contains(out, "Seeded dogfood session 0192-demo-seed-id") {
		t.Errorf("output missing seeded session id, got: %q", out)
	}
	if !strings.Contains(out, "Next: run `rabbit-hole serve` and open http://"+addr+"/dashboard") {
		t.Errorf("output missing dashboard hint for %s, got: %q", addr, out)
	}
	if serverCalls == 0 {
		t.Error("daemon was never called")
	}
}

// TestDemoCmd_AddrUnreachable verifies a missing daemon surfaces the
// unreachable error instead of silently seeding a local DB (GAP-007).
func TestDemoCmd_AddrUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	captured := captureStdout(func() {
		cmd := newDemoCmd()
		cmd.SetArgs([]string{"--addr", addr, "--flows", "1"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("demo --addr against dead daemon: expected error, got nil")
		} else if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") {
			t.Errorf("error = %q, want daemon-unreachable message", err.Error())
		}
	})
	if strings.Contains(captured, "Seeded dogfood session") {
		t.Errorf("demo must not print a seeded session when the daemon is unreachable, got: %q", captured)
	}
}
