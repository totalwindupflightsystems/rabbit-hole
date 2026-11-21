package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func TestChatCmd_ConnectionRefused(t *testing.T) {
	// Allocate an ephemeral port, then release it so nothing is listening
	// there. The chat POST must be refused even if a real daemon happens to
	// be running on the default 127.0.0.1:9734 (DF-010 — hermetic test).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ephemeral listener: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	// Chat tries to POST to the server — no server running, connection refused
	output := captureStdout(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"--addr", addr, "what did the agent do"})
		err := cmd.Execute()
		if err == nil {
			t.Error("expected error (no server running), got nil")
		} else {
			errMsg := err.Error()
			t.Logf("chat error: %v", err)
			// Should mention server not running
			if !strings.Contains(errMsg, "Is 'rabbit-hole serve' running?") &&
				!strings.Contains(errMsg, "connect") {
				t.Errorf("unexpected error message: %v", err)
			}
		}
	})
	t.Logf("chat output: %s", output)
}

func TestChatCmd_WithSessionFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	var request types.ChatRequest
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chat" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = fmt.Fprint(w, `{"answer":"session answer","flows":[],"suggestions":[]}`)
	}))
	defer ts.Close()
	cmd := newChatCmd()
	cmd.SetArgs([]string{"--addr", strings.TrimPrefix(ts.URL, "http://"), "--session", "sess-abc", "what happened?"})
	var err error
	output := captureStdout(func() { err = cmd.Execute() })
	if err != nil {
		t.Errorf("chat: %v", err)
	}
	if !hit || request.SessionID != "sess-abc" || request.Message != "what happened?" {
		t.Errorf("chat request: hit=%v request=%+v", hit, request)
	}
	if strings.TrimSpace(output) != "session answer" {
		t.Errorf("chat output = %q", output)
	}
}

func TestChatCmd_WithJsonFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chat" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"answer":"json answer","flows":[],"suggestions":["follow up"]}`)
	}))
	defer ts.Close()
	cmd := newChatCmd()
	cmd.SetArgs([]string{"--addr", strings.TrimPrefix(ts.URL, "http://"), "--json", "status?"})
	var err error
	output := captureStdout(func() { err = cmd.Execute() })
	if err != nil {
		t.Errorf("chat --json: %v", err)
	}
	var response types.ChatResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Errorf("chat --json output is not JSON: %q: %v", output, err)
	}
	if !hit || response.Answer != "json answer" || len(response.Suggestions) != 1 || response.Suggestions[0] != "follow up" {
		t.Errorf("chat --json: hit=%v response=%+v", hit, response)
	}
}
