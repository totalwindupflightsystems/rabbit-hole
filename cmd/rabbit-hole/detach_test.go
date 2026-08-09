package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewDetachCmd_Structure(t *testing.T) {
	cmd := newDetachCmd()

	if cmd.Use != "detach <session-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "detach <session-id>")
	}
	if cmd.Short != "Stop tracing a session" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Stop tracing a session")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewDetachCmd_Flags(t *testing.T) {
	cmd := newDetachCmd()

	f := cmd.Flags().Lookup("all")
	if f == nil {
		t.Fatal("flag --all not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("flag --all default = %q, want %q", f.DefValue, "false")
	}
}

func TestNewDetachCmd_AllFlagDefault(t *testing.T) {
	cmd := newDetachCmd()
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		t.Fatalf("GetBool(all): %v", err)
	}
	if all {
		t.Error("default --all should be false")
	}
}

func TestNewDetachCmd_AddrFlag(t *testing.T) {
	// DF-006: --addr overrides the daemon address; help must mention the
	// RABBITHOLE_LISTEN_ADDR fallback.
	cmd := newDetachCmd()

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

	if err := cmd.Flags().Set("addr", "127.0.0.1:9999"); err != nil {
		t.Fatalf("failed to set --addr: %v", err)
	}
	addr, err := cmd.Flags().GetString("addr")
	if err != nil {
		t.Fatalf("GetString(addr): %v", err)
	}
	if addr != "127.0.0.1:9999" {
		t.Errorf("addr = %q, want %q", addr, "127.0.0.1:9999")
	}
}

func TestDetachCmd_AddrFlagReachesServer(t *testing.T) {
	// DF-006: --addr (not the env var) must route the detach request to the
	// given daemon address. The data dir is isolated so config.Validate()
	// passes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/sess-abc/detach" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())

	output := captureStdout(func() {
		cmd := newDetachCmd()
		cmd.SetArgs([]string{"--addr", strings.TrimPrefix(ts.URL, "http://"), "sess-abc"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("detach with --addr: %v", err)
		}
	})
	if !strings.Contains(output, "sess-abc") {
		t.Errorf("detach output missing session id, got: %q", output)
	}
}

func TestNewDetachCmd_Args(t *testing.T) {
	// MaximumNArgs(1) — 0 args is allowed (used with --all)
	cmd := newDetachCmd()
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}
	// 1 arg is allowed
	if err := cmd.Args(cmd, []string{"session-abc"}); err != nil {
		t.Errorf("unexpected error for 1 arg: %v", err)
	}
	// 2 args should fail
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error for 2 args")
	}
}
