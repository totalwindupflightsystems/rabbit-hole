package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

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
