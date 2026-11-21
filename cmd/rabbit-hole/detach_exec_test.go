package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetachCmd_WithNonExistentSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/nonexistent-session-id/detach" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":"session not found"}`)
	}))
	defer ts.Close()

	cmd := newDetachCmd()
	cmd.SetArgs([]string{"--addr", strings.TrimPrefix(ts.URL, "http://"), "nonexistent-session-id"})
	err := cmd.Execute()
	if err == nil || err.Error() != "session not found" {
		t.Errorf("detach error = %v, want session not found", err)
	}
	if !hit {
		t.Error("detach did not reach the daemon")
	}
}

func TestDetachCmd_WithAllFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	var requests []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"sessions":[{"id":"running-id","agent_pid":123,"status":"running"},{"id":"finished-id","status":"completed"}]}`)
		case "/api/v1/sessions/running-id/detach":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cmd := newDetachCmd()
	cmd.SetArgs([]string{"--addr", strings.TrimPrefix(ts.URL, "http://"), "--all"})
	var err error
	output := captureStdout(func() { err = cmd.Execute() })
	if err != nil {
		t.Errorf("detach --all: %v", err)
	}
	if got := strings.Join(requests, ", "); got != "GET /api/v1/sessions?limit=100, POST /api/v1/sessions/running-id/detach" {
		t.Errorf("requests = %q", got)
	}
	if !strings.Contains(output, "Detached session running-id") {
		t.Errorf("detach --all output = %q", output)
	}
}
