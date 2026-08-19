package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// runCompact executes the compact command with the given args and returns its
// stdout. RABBITHOLE_DATA_DIR points at a temp dir so config.Load never
// touches the real home directory; the standalone DB path is intentionally
// not used by the --addr tests.
func runCompact(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())

	out := captureStdout(func() {
		cmd := newCompactCmd()
		var cobraOut, cobraErr strings.Builder
		cmd.SetOut(&cobraOut)
		cmd.SetErr(&cobraErr)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("compact: %v (stderr: %s)", err, cobraErr.String())
		}
	})
	return out
}

func TestNewCompactCmd_Structure(t *testing.T) {
	cmd := newCompactCmd()

	if cmd.Use != "compact" {
		t.Errorf("Use = %q, want %q", cmd.Use, "compact")
	}
	if cmd.Short != "Compact and clean up old data" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Compact and clean up old data")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewCompactCmd_Flags(t *testing.T) {
	cmd := newCompactCmd()

	tests := []struct {
		name         string
		flagName     string
		defaultValue string
	}{
		{"before flag", "before", ""},
		{"retention flag", "retention", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flagName)
			if f == nil {
				t.Fatalf("flag --%s not registered", tt.flagName)
			}
			if f.DefValue != tt.defaultValue {
				t.Errorf("flag --%s default = %q, want %q", tt.flagName, f.DefValue, tt.defaultValue)
			}
		})
	}
}

func TestNewCompactCmd_FlagValues(t *testing.T) {
	cmd := newCompactCmd()

	if err := cmd.Flags().Set("before", "-30d"); err != nil {
		t.Fatalf("failed to set --before: %v", err)
	}
	before, err := cmd.Flags().GetString("before")
	if err != nil {
		t.Fatalf("GetString(before): %v", err)
	}
	if before != "-30d" {
		t.Errorf("before = %q, want %q", before, "-30d")
	}

	if err := cmd.Flags().Set("retention", "60"); err != nil {
		t.Fatalf("failed to set --retention: %v", err)
	}
	retention, err := cmd.Flags().GetInt("retention")
	if err != nil {
		t.Fatalf("GetInt(retention): %v", err)
	}
	if retention != 60 {
		t.Errorf("retention = %d, want 60", retention)
	}
}

func TestNewCompactCmd_NoArgs(t *testing.T) {
	cmd := newCompactCmd()
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error for 1 arg with NoArgs")
	}
}

// TestNewCompactCmd_AddrFlag verifies the GAP-007 --addr flag is registered
// with the same default/help contract as status/attach.
func TestNewCompactCmd_AddrFlag(t *testing.T) {
	cmd := newCompactCmd()

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

// TestCompactCmd_AddrRoutesToDaemon verifies that --addr sends the resolved
// RFC3339 cutoff to POST /api/v1/compact on the daemon (GAP-007), prints the
// same success messages, and never opens a local DB.
func TestCompactCmd_AddrRoutesToDaemon(t *testing.T) {
	var (
		gotPath     string
		gotMethod   string
		gotCutoff   string
		serverCalls int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		gotPath = r.URL.Path
		gotMethod = r.Method
		var body struct {
			Cutoff string `json:"cutoff"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotCutoff = body.Cutoff
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"cutoff": body.Cutoff})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	out := runCompact(t, "--addr", addr, "--before", "-720h")

	if gotPath != "/api/v1/compact" {
		t.Errorf("path = %q, want /api/v1/compact", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if _, err := time.Parse(time.RFC3339, gotCutoff); err != nil {
		t.Errorf("cutoff %q is not RFC3339: %v", gotCutoff, err)
	}
	if !strings.Contains(out, "Compacting data older than -720h ago...") {
		t.Errorf("output missing 'Compacting data older than -720h ago...', got: %q", out)
	}
	if !strings.Contains(out, "Compact complete.") {
		t.Errorf("output missing 'Compact complete.', got: %q", out)
	}
	if serverCalls == 0 {
		t.Error("daemon was never called")
	}
}

// TestCompactCmd_AddrUnreachable verifies a missing daemon surfaces the
// unreachable error instead of silently compacting a local DB (GAP-007).
func TestCompactCmd_AddrUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	captured := captureStdout(func() {
		cmd := newCompactCmd()
		cmd.SetArgs([]string{"--addr", addr, "--before", "720h"})
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("compact --addr against dead daemon: expected error, got nil")
		} else if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") {
			t.Errorf("error = %q, want daemon-unreachable message", err.Error())
		}
	})
	if !strings.Contains(captured, "Compacting data older than") {
		t.Errorf("output missing progress line, got: %q", captured)
	}
}
