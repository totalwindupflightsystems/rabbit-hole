package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
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

func TestStatusCmd_AddrFlagPrintsOverriddenAddress(t *testing.T) {
	// DF-006: status prints the overridden daemon address in its Server line.
	// The data dir is isolated so a fresh DB is created in the temp dir.
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())

	output := captureStdout(func() {
		cmd := newStatusCmd()
		cmd.SetArgs([]string{"--addr", "127.0.0.1:19734"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status with --addr: %v", err)
		}
	})
	if !strings.Contains(output, "127.0.0.1:19734") {
		t.Errorf("status output should show the overridden address, got: %q", output)
	}
}

func TestStatusCmd_PrintsStoredListenAddrFromMetadata(t *testing.T) {
	// DF-007: status reports the daemon's ACTUAL bound address from DB
	// metadata (written by serve at startup), not the configured default —
	// the stored value wins even when --addr differs. Old DBs without the
	// row still fall back to --addr (covered by
	// TestStatusCmd_AddrFlagPrintsOverriddenAddress).
	dataDir := t.TempDir()
	t.Setenv("RABBITHOLE_DATA_DIR", dataDir)

	store, err := storage.NewSQLiteStore(filepath.Join(dataDir, "rabbit-hole.db"), nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.SetMetadata(context.Background(), "listen_addr", "127.0.0.1:19734"); err != nil {
		store.Close()
		t.Fatalf("SetMetadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	output := captureStdout(func() {
		cmd := newStatusCmd()
		cmd.SetArgs([]string{"--addr", "127.0.0.1:9999"}) // differs from stored value
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
	})
	if !strings.Contains(output, "Server:      127.0.0.1:19734") {
		t.Errorf("status should print the stored listen addr, got: %q", output)
	}
	if strings.Contains(output, "127.0.0.1:9999") {
		t.Errorf("status must not print the --addr override when stored metadata exists, got: %q", output)
	}
}
