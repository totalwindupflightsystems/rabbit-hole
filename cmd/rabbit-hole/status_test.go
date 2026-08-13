package main

import (
	"net"
	"strings"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/express"
)

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{"zero bytes", 0, "0 B"},
		{"bytes under 1 KB", 500, "500 B"},
		{"exactly 1 KB", 1024, "1.0 KB"},
		{"~1.5 KB", 1536, "1.5 KB"},
		{"~2 KB", 2048, "2.0 KB"},
		{"exactly 1 MB", 1048576, "1.0 MB"},
		{"~1.5 MB", 1572864, "1.5 MB"},
		{"exactly 1 GB", 1073741824, "1.0 GB"},
		{"~1.5 GB", 1610612736, "1.5 GB"},
		{"exactly 1 TB", 1099511627776, "1.0 TB"},
		{"1 PB", 1125899906842624, "1.0 PB"},
		{"1 EB", 1152921504606846976, "1.0 EB"},
		{"~2.5 KB", 2560, "2.5 KB"},
		{"~10 MB", 10485760, "10.0 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanizeBytes(tt.input); got != tt.want {
				t.Errorf("humanizeBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewStatusCmd_Structure(t *testing.T) {
	cmd := newStatusCmd()

	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
	if cmd.Short != "Show Rabbit-Hole daemon status" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Show Rabbit-Hole daemon status")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewStatusCmd_AddrFlag(t *testing.T) {
	// DF-006: status accepts --addr; help must mention the
	// RABBITHOLE_LISTEN_ADDR fallback.
	cmd := newStatusCmd()

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

func TestStatusCmd_WithoutDaemon_ReportsAddr(t *testing.T) {
	// status now reads the daemon over HTTP (DF-014); a missing daemon
	// must fail with the unreachable message naming the address, not
	// print a local-DB snapshot.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	t.Setenv("RABBITHOLE_LISTEN_ADDR", addr)

	output := captureStdout(func() {
		cmd := newStatusCmd()
		if err := cmd.Execute(); err == nil {
			t.Fatal("status without daemon should fail")
		} else if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") ||
			!strings.Contains(err.Error(), addr) {
			t.Errorf("status error = %q, want unreachable message naming %s", err.Error(), addr)
		}
	})
	if strings.Contains(output, "Rabbit-Hole") {
		t.Errorf("status must not print a snapshot when the daemon is unreachable, got:\n%s", output)
	}
}

func TestStatusCmd_ShowsDaemonFacts(t *testing.T) {
	// DF-014: status renders the DAEMON's facts — DB path, bound address,
	// session counts, log level, eBPF state — all over HTTP.
	server, store := startTestDaemon(t)
	server.SetRuntimeInfo(express.RuntimeInfo{
		LogLevel:    "debug",
		EBPFEnabled: true,
		EBPFDetail:  "kernel probes attached",
	})

	output := captureStdout(func() {
		cmd := newStatusCmd()
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
	})
	if !strings.Contains(output, "Server:      "+server.Addr()) {
		t.Errorf("status should show the daemon's bound address, got:\n%s", output)
	}
	if !strings.Contains(output, "Database:    "+store.Path()) {
		t.Errorf("status should show the daemon's DB path, got:\n%s", output)
	}
	if !strings.Contains(output, "Sessions:    0 (0 active, 0 completed)") {
		t.Errorf("status session line wrong, got:\n%s", output)
	}
	if !strings.Contains(output, "Log Level:   debug") {
		t.Errorf("status should show the daemon's log level, got:\n%s", output)
	}
	if !strings.Contains(output, "eBPF:        enabled") {
		t.Errorf("status should show eBPF enabled, got:\n%s", output)
	}
}

func TestStatusCmd_ReportsDaemonDB_NotLocalPath(t *testing.T) {
	// DF-014 regression: the CLI's own RABBITHOLE_DB_PATH differs from the
	// daemon's, yet status must report the daemon's database — never the
	// CLI's local (phantom) path.
	server, store := startTestDaemon(t)
	_ = server

	phantom := t.TempDir() + "/phantom.db"
	t.Setenv("RABBITHOLE_DB_PATH", phantom)

	output := captureStdout(func() {
		cmd := newStatusCmd()
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
	})
	if !strings.Contains(output, "Database:    "+store.Path()) {
		t.Errorf("status must report the daemon's DB path %q, got:\n%s", store.Path(), output)
	}
	if strings.Contains(output, phantom) {
		t.Errorf("status must not report the CLI's local DB path %q, got:\n%s", phantom, output)
	}
}

func TestStatusCmd_AddrFlagRoutesToDaemon(t *testing.T) {
	// --addr must override the env/default daemon address (DF-006): the
	// request goes to the given address, which here is a real daemon.
	server, _ := startTestDaemon(t)

	output := captureStdout(func() {
		cmd := newStatusCmd()
		cmd.SetArgs([]string{"--addr", server.Addr()})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status with --addr: %v", err)
		}
	})
	if !strings.Contains(output, "Server:      "+server.Addr()) {
		t.Errorf("status --addr should report the daemon at %s, got:\n%s", server.Addr(), output)
	}
}
