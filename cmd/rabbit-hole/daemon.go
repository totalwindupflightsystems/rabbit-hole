// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// daemonClient is a small HTTP client for talking to a running
// `rabbit-hole serve` daemon. attach and detach hand session lifecycle to
// the daemon (DF-001): the daemon owns the collector and the database, so
// sessions persist across CLI invocations instead of dying with the
// attaching process. The daemon address is cfg.ListenAddr (default
// 127.0.0.1:9734, RABBITHOLE_LISTEN_ADDR).
type daemonClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newDaemonClient(listenAddr string) *daemonClient {
	return &daemonClient{
		baseURL: "http://" + listenAddr,
		apiKey:  os.Getenv("RABBITHOLE_API_KEY"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// attachSession asks the daemon to attach its collector to pid, persist the
// session, and returns the wire summary of the created session.
func (d *daemonClient) attachSession(ctx context.Context, req types.AttachSessionRequest) (*types.SessionSummary, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode attach request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.baseURL+"/api/v1/sessions/attach", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if d.apiKey != "" {
		httpReq.Header.Set("X-API-Key", d.apiKey)
	}

	resp, err := d.http.Do(httpReq)
	if err != nil {
		return nil, d.unreachable(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, d.decodeError(resp)
	}

	var summary types.SessionSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("decode attach response: %w", err)
	}
	return &summary, nil
}

// detachSession asks the daemon to stop tracing a session and mark it
// completed in the database.
func (d *daemonClient) detachSession(ctx context.Context, sessionID string) error {
	path := fmt.Sprintf("/api/v1/sessions/%s/detach", url.PathEscape(sessionID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+path, nil)
	if err != nil {
		return err
	}
	if d.apiKey != "" {
		httpReq.Header.Set("X-API-Key", d.apiKey)
	}

	resp, err := d.http.Do(httpReq)
	if err != nil {
		return d.unreachable(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return d.decodeError(resp)
	}
	return nil
}

// listSessions returns all sessions the daemon knows about (used by
// `detach --all` to find the running ones).
func (d *daemonClient) listSessions(ctx context.Context) ([]types.SessionSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.baseURL+"/api/v1/sessions?limit=100", nil)
	if err != nil {
		return nil, err
	}
	if d.apiKey != "" {
		httpReq.Header.Set("X-API-Key", d.apiKey)
	}

	resp, err := d.http.Do(httpReq)
	if err != nil {
		return nil, d.unreachable(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, d.decodeError(resp)
	}

	var body struct {
		Sessions []types.SessionSummary `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	if body.Sessions == nil {
		body.Sessions = []types.SessionSummary{}
	}
	return body.Sessions, nil
}

// unreachable wraps transport-level failures with a hint to start the
// daemon, which is the fix for the DF-001 failure mode.
func (d *daemonClient) unreachable(err error) error {
	return fmt.Errorf("cannot reach Rabbit-Hole daemon at %s (is 'rabbit-hole serve' running?): %w",
		d.baseURL, err)
}

// decodeError extracts the {"error": "..."} message the daemon returns for
// non-2xx responses, falling back to the bare status code.
func (d *daemonClient) decodeError(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err == nil && e.Error != "" {
		return fmt.Errorf("%s", e.Error)
	}
	return fmt.Errorf("daemon returned status %d", resp.StatusCode)
}
