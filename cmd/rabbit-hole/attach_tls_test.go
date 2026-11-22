package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// TestAttachCmd_TLSInterceptFromConfig proves RABBITHOLE_TLS_INTERCEPT reaches
// the attach request (§06 configuration table). The daemon default is
// interception ON; the env var sets the per-session value, and an explicit
// --no-tls-intercept still wins (DF-RABBIT-HOLE-011).
func TestAttachCmd_TLSInterceptFromConfig(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		extraArgs    []string
		wantIntercep bool
	}{
		{name: "default config enables interception", envValue: "", wantIntercep: true},
		{name: "env disables interception", envValue: "false", wantIntercep: false},
		{name: "env enables interception", envValue: "true", wantIntercep: true},
		{name: "flag wins over env", envValue: "true", extraArgs: []string{"--no-tls-intercept"}, wantIntercep: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(chan types.AttachSessionRequest, 1)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req types.AttachSessionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode attach request: %v", err)
				}
				got <- req
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(types.SessionSummary{ID: "sess-tls", AgentPID: 4242, AgentName: "test-agent"})
			}))
			defer ts.Close()

			t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
			if tt.envValue != "" {
				t.Setenv("RABBITHOLE_TLS_INTERCEPT", tt.envValue)
			}

			args := append([]string{"--pid", "4242", "--no-ebpf", "--addr", strings.TrimPrefix(ts.URL, "http://")}, tt.extraArgs...)
			captureStdout(func() {
				cmd := newAttachCmd()
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("attach: %v", err)
				}
			})

			req := <-got
			if req.TLSInterception != tt.wantIntercep {
				t.Errorf("TLSInterception = %v, want %v", req.TLSInterception, tt.wantIntercep)
			}
		})
	}
}
