package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func TestAttachCmd_NonExistentPid(t *testing.T) {
	// Running attach with a PID that doesn't exist should fail at processExists
	output := captureStdout(func() {
		cmd := newAttachCmd()
		cmd.SetArgs([]string{"--pid=999999"})
		err := cmd.Execute()
		if err != nil {
			t.Logf("attach error (expected): %v", err)
		} else {
			t.Error("expected error for non-existent PID")
		}
	})
	t.Logf("attach output: %s", output)
}

func TestAttachCmd_AlternativeFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	var request types.AttachSessionRequest
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/attach" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id":"attached-id","agent_pid":123}`)
	}))
	defer ts.Close()
	cmd := newAttachCmd()
	cmd.SetArgs([]string{"--addr", strings.TrimPrefix(ts.URL, "http://"), "--pid", "123", "--context-windows", "--categories", "syscall,network", "--no-tls-intercept", "--no-ebpf"})
	var err error
	output := captureStdout(func() { err = cmd.Execute() })
	if err != nil {
		t.Errorf("attach: %v", err)
	}
	if !hit || request.PID != 123 || !request.ContextWindows || !reflect.DeepEqual(request.Categories, []string{"syscall", "network"}) || request.TLSInterception || !request.NoEBPF {
		t.Errorf("attach request: hit=%v request=%+v", hit, request)
	}
	if !strings.Contains(output, "Session ID: attached-id") {
		t.Errorf("attach output = %q", output)
	}
}
