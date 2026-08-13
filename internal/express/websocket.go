// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Create a subscriber channel
	flowCh := make(chan types.Flow, 64)
	s.subMu.Lock()
	s.subscribers[sessionID] = append(s.subscribers[sessionID], flowCh)
	s.subMu.Unlock()

	defer func() {
		s.subMu.Lock()
		subs := s.subscribers[sessionID]
		for i, ch := range subs {
			if ch == flowCh {
				s.subscribers[sessionID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(s.subscribers[sessionID]) == 0 {
			delete(s.subscribers, sessionID)
		}
		s.subMu.Unlock()
		close(flowCh)
	}()

	// Set read deadline for ping/pong
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Write loop: push flows to WebSocket
	ticker := time.NewTicker(30 * time.Second) // ping interval
	defer ticker.Stop()

	for {
		select {
		case flow, ok := <-flowCh:
			if !ok {
				// Channel closed — session ended
				conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"))
				return
			}
			data, _ := json.Marshal(flow)
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}

		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

// PublishFlow pushes a flow to all WebSocket subscribers for a session.
// Called by the collector→classifier pipeline as new flows are generated.
func (s *Server) PublishFlow(sessionID string, flow types.Flow) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subscribers[sessionID] {
		select {
		case ch <- flow:
		default:
			// Subscriber buffer full — drop (non-blocking)
		}
	}
}
