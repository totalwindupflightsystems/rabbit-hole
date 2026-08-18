// Package express — WebSocket disconnect hygiene (DF-027).
//
// DF-027 audit: a WS subscriber that connects and disconnects must not
// linger in the subscriber registry. Before the fix, the handler had no
// read loop, so a dead peer was only noticed when a WRITE failed (up to
// the 10s write deadline) — the "WS connect/disconnect" leg of the wedge
// combo. The dedicated reader makes disconnect detection immediate and the
// deferred cleanup removes the subscriber right away.
package express

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWebSocket_DisconnectRemovesSubscriber connects a WS client to a
// session, waits for the server to register it, drops the connection, and
// asserts the subscriber is cleaned up within a short window (far shorter
// than the old write-deadline path of up to 10s).
func TestWebSocket_DisconnectRemovesSubscriber(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)

	url := "ws://" + srv.Addr() + "/api/v1/ws/sessions/" + sess.ID
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Deterministic synchronization: wait for registration (same pattern as
	// TestWebSocket — the upgrade completes before the subscriber append).
	deadline := time.Now().Add(5 * time.Second)
	for {
		srv.subMu.Lock()
		n := len(srv.subscribers[sess.ID])
		srv.subMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("websocket subscriber never registered")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Drop the connection.
	conn.Close()

	// The read loop notices the close immediately and the deferred cleanup
	// removes the subscriber. Give it a generous 2s — still far below the
	// old up-to-10s write-deadline discovery path.
	deadline = time.Now().Add(2 * time.Second)
	for {
		srv.subMu.Lock()
		n := len(srv.subscribers[sess.ID])
		srv.subMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriber still registered %s after client disconnect — leak", 2*time.Second)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
