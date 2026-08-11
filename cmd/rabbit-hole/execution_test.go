package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execTestEnv sets up env vars for command execution tests.
// Returns a cleanup function that must be called.
func execTestEnv(t *testing.T) func() {
	t.Helper()
	dbDir := filepath.Join(os.TempDir(), "rabbit-hole-test-"+t.Name())
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dbDir, err)
	}
	dbPath := filepath.Join(dbDir, "test.db")

	oldDB := os.Getenv("RABBITHOLE_DB_PATH")
	oldListen := os.Getenv("RABBITHOLE_LISTEN_ADDR")
	os.Setenv("RABBITHOLE_DB_PATH", dbPath)
	os.Setenv("RABBITHOLE_LISTEN_ADDR", "127.0.0.1:0")

	return func() {
		os.Unsetenv("RABBITHOLE_DB_PATH")
		if oldDB != "" {
			os.Setenv("RABBITHOLE_DB_PATH", oldDB)
		}
		if oldListen != "" {
			os.Setenv("RABBITHOLE_LISTEN_ADDR", oldListen)
		} else {
			os.Unsetenv("RABBITHOLE_LISTEN_ADDR")
		}
		os.RemoveAll(dbDir)
	}
}

func TestCompactCmd_WithBeforeFlag(t *testing.T) {
	cleanup := execTestEnv(t)
	defer cleanup()

	output := captureStdout(func() {
		cmd := newCompactCmd()
		cmd.SetArgs([]string{"--before", "-30d"})
		if err := cmd.Execute(); err != nil {
			t.Logf("compact with --before error: %v", err)
		}
	})

	t.Logf("compact output: %s", output)
	if !strings.Contains(output, "Compacting data older than -30d ago") {
		t.Errorf("output missing expected message, got: %s", output)
	}
}

func TestCompactCmd_WithRetentionFlag(t *testing.T) {
	cleanup := execTestEnv(t)
	defer cleanup()

	output := captureStdout(func() {
		cmd := newCompactCmd()
		cmd.SetArgs([]string{"--retention", "60"})
		if err := cmd.Execute(); err != nil {
			t.Logf("compact retention error: %v", err)
		}
	})

	t.Logf("compact retention output: %s", output)
	if !strings.Contains(output, "Compact complete.") {
		t.Errorf("output missing 'Compact complete.', got: %s", output)
	}
}

func TestCompactCmd_InvalidBefore(t *testing.T) {
	cleanup := execTestEnv(t)
	defer cleanup()

	output := captureStdout(func() {
		cmd := newCompactCmd()
		cmd.SetArgs([]string{"--before", "xyz"})
		if err := cmd.Execute(); err != nil {
			t.Logf("compact invalid before error (expected): %v", err)
		}
	})

	t.Logf("compact invalid before output: %s", output)
	// Should output nothing to stdout — error was returned from RunE
}

func TestSearchCmd_WithJsonFlag(t *testing.T) {
	startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newSearchCmd()
		cmd.SetArgs([]string{"--json", "--limit", "5"})
		if err := cmd.Execute(); err != nil {
			t.Logf("search json error: %v", err)
		}
	})

	t.Logf("search json output: %s", output)
	if !strings.Contains(output, "\"flows\"") || !strings.Contains(output, "\"total\"") {
		t.Errorf("json output missing expected fields, got: %s", output)
	}
}

func TestSearchCmd_NoResults(t *testing.T) {
	startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newSearchCmd()
		cmd.SetArgs([]string{"nonexistent_query"})
		if err := cmd.Execute(); err != nil {
			t.Logf("search error: %v", err)
		}
	})

	t.Logf("search no-results output: %s", output)
	if !strings.Contains(output, "No results found") {
		t.Errorf("output missing 'No results found', got: %q", output)
	}
}

func TestSearchCmd_WithFilters(t *testing.T) {
	startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newSearchCmd()
		cmd.SetArgs([]string{"--intent", "read_file", "--outcome", "success"})
		if err := cmd.Execute(); err != nil {
			t.Logf("search filters error: %v", err)
		}
	})

	t.Logf("search filters output: %s", output)
	// On empty DB should say "No results found" or show 0 results
}

func TestChatCmd_ExecuteNoArgs(t *testing.T) {
	cmd := newChatCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for executing chat without args")
	}
}

func TestStatusCmd_Execution(t *testing.T) {
	// Status reads the daemon over HTTP (DF-014) — it must render a
	// snapshot against a running daemon.
	startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newStatusCmd()
		if err := cmd.Execute(); err != nil {
			t.Logf("status error: %v", err)
		}
	})

	t.Logf("status output: %s", output)
	if !strings.Contains(output, "Rabbit-Hole") {
		t.Errorf("status output missing header, got: %q", output)
	}
}

func TestListCmd_Execution(t *testing.T) {
	// List against a running daemon with no sessions → "No active sessions"
	startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newListCmd()
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Errorf("list execution error: %v", err)
		}
	})

	t.Logf("list output: %s", output)
	if !strings.Contains(output, "No active sessions") {
		t.Errorf("unexpected list output: %s", output)
	}
}

func TestListCmd_WithAllFlag(t *testing.T) {
	// list --all against a running daemon with no sessions → "No sessions found."
	startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newListCmd()
		cmd.SetArgs([]string{"--all"})
		if err := cmd.Execute(); err != nil {
			t.Errorf("list with --all error: %v", err)
		}
	})

	t.Logf("list --all output: %s", output)
	if !strings.Contains(output, "No sessions found.") {
		t.Errorf("unexpected list --all output: %s", output)
	}
}

func TestDetachCmd_MaximumNArgs(t *testing.T) {
	// cobra.MaximumNArgs(1) — 0-1 args valid
	cmd := newDetachCmd()

	// 0 args passes arg validation (used with --all)
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}

	// 1 arg passes
	if err := cmd.Args(cmd, []string{"session-abcd"}); err != nil {
		t.Errorf("unexpected error for 1 arg: %v", err)
	}

	// 2 args should fail
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error for 2 args with MaximumNArgs(1)")
	}
}
