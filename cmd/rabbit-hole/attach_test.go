package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func TestNewAttachCmd_Structure(t *testing.T) {
	cmd := newAttachCmd()

	if cmd.Use != "attach --pid <PID>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "attach --pid <PID>")
	}
	if cmd.Short != "Attach to an agent process and start collecting traces" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Attach to an agent process and start collecting traces")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
	if cmd.Args == nil {
		t.Fatal("Args validator is nil")
	}
}

func TestNewAttachCmd_Flags(t *testing.T) {
	cmd := newAttachCmd()

	tests := []struct {
		name         string
		flagName     string
		defaultValue string
	}{
		{"daemon address", "addr", ""},
		{"pid flag", "pid", "0"},
		{"context windows", "context-windows", "false"},
		{"categories", "categories", "[]"},
		{"no tls intercept", "no-tls-intercept", "false"},
		{"no ebpf", "no-ebpf", "false"},
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

func TestNewAttachCmd_PidSetAndRead(t *testing.T) {
	cmd := newAttachCmd()
	pidFlag := cmd.Flags().Lookup("pid")
	if pidFlag == nil {
		t.Fatal("--pid flag not found")
	}

	if err := cmd.Flags().Set("pid", "1234"); err != nil {
		t.Fatalf("failed to set --pid: %v", err)
	}
	pid, err := cmd.Flags().GetInt32("pid")
	if err != nil {
		t.Fatalf("GetInt32(pid): %v", err)
	}
	if pid != 1234 {
		t.Errorf("pid = %d, want %d", pid, 1234)
	}
}

func TestNewAttachCmd_AddrFlag(t *testing.T) {
	// DF-006: --addr overrides the daemon address; help must mention the
	// RABBITHOLE_LISTEN_ADDR fallback.
	cmd := newAttachCmd()

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

func TestAttachCmd_AddrFlagReachesServer(t *testing.T) {
	// DF-006: --addr (not the env var) must route the attach request to the
	// given daemon address. --no-ebpf skips the eBPF preflight; the data dir
	// is isolated so config.Validate() passes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/attach" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.SessionSummary{
			ID:        "sess-addr-override",
			AgentPID:  4242,
			AgentName: "test-agent",
		})
	}))
	defer ts.Close()
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())

	output := captureStdout(func() {
		cmd := newAttachCmd()
		cmd.SetArgs([]string{"--pid", "4242", "--no-ebpf", "--addr", strings.TrimPrefix(ts.URL, "http://")})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("attach with --addr: %v", err)
		}
	})
	if !strings.Contains(output, "sess-addr-override") {
		t.Errorf("attach output missing session id, got: %q", output)
	}
}

func TestNewAttachCmd_DefaultValues(t *testing.T) {
	cmd := newAttachCmd()

	pid, err := cmd.Flags().GetInt32("pid")
	if err != nil {
		t.Fatalf("GetInt32(pid): %v", err)
	}
	if pid != 0 {
		t.Errorf("default pid = %d, want 0", pid)
	}

	cw, err := cmd.Flags().GetBool("context-windows")
	if err != nil {
		t.Fatalf("GetBool(context-windows): %v", err)
	}
	if cw {
		t.Error("default context-windows should be false")
	}

	noTLS, err := cmd.Flags().GetBool("no-tls-intercept")
	if err != nil {
		t.Fatalf("GetBool(no-tls-intercept): %v", err)
	}
	if noTLS {
		t.Error("default no-tls-intercept should be false")
	}
}
