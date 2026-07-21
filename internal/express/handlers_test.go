package express

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// decodeHealth fetches /health and decodes it into a healthResponse,
// asserting the HTTP status code along the way.
func decodeHealth(t *testing.T, srv *Server) healthResponse {
	t.Helper()
	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var hr healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return hr
}

// TestHealth_NoExtraComponents_StorageAndMetrics verifies that a fresh
// server (no extra checks registered) reports storage + metrics as ok
// and does NOT emit classifier/collector entries.
func TestHealth_NoExtraComponents_StorageAndMetrics(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	hr := decodeHealth(t, srv)

	if hr.Status != "ok" {
		t.Errorf("status: got %q, want \"ok\"", hr.Status)
	}
	if hr.Version != healthVersion {
		t.Errorf("version: got %q, want %q", hr.Version, healthVersion)
	}
	if hr.Uptime == "" {
		t.Error("uptime empty")
	}
	if hr.Components == nil {
		t.Fatal("components map is nil")
	}

	// storage and metrics are always reported on a wired server.
	if cs, ok := hr.Components["storage"]; !ok {
		t.Error("storage component missing")
	} else if cs.Status != "ok" {
		t.Errorf("storage status: got %q, want \"ok\"", cs.Status)
	}

	if cs, ok := hr.Components["metrics"]; !ok {
		t.Error("metrics component missing")
	} else if cs.Status != "ok" {
		t.Errorf("metrics status: got %q, want \"ok\"", cs.Status)
	}

	// classifier and collector must be omitted when not registered.
	for _, name := range []string{"classifier", "collector"} {
		if _, ok := hr.Components[name]; ok {
			t.Errorf("component %q should be omitted when unregistered", name)
		}
	}
}

// TestHealth_RegisteredComponent_Ok verifies that a registered component
// with a passing probe shows up with status "ok" and its detail string.
func TestHealth_RegisteredComponent_Ok(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "model loaded",
		Check:  func(ctx context.Context) error { return nil },
	})

	hr := decodeHealth(t, srv)

	if hr.Status != "ok" {
		t.Errorf("top status: got %q, want \"ok\"", hr.Status)
	}
	cs, ok := hr.Components["classifier"]
	if !ok {
		t.Fatal("classifier component missing")
	}
	if cs.Status != "ok" {
		t.Errorf("classifier status: got %q, want \"ok\"", cs.Status)
	}
	if cs.Detail != "model loaded" {
		t.Errorf("classifier detail: got %q, want \"model loaded\"", cs.Detail)
	}
}

// TestHealth_RegisteredComponent_Error_DegradesOverall verifies that a
// failing component probe flips the top-level status to "degraded" and
// surfaces the error string as detail.
func TestHealth_RegisteredComponent_Error_DegradesOverall(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	probeErr := errors.New("connection refused")
	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "model loaded",
		Check:  func(ctx context.Context) error { return probeErr },
	})

	hr := decodeHealth(t, srv)

	if hr.Status != "degraded" {
		t.Errorf("top status: got %q, want \"degraded\"", hr.Status)
	}
	cs, ok := hr.Components["classifier"]
	if !ok {
		t.Fatal("classifier component missing")
	}
	if cs.Status != "error" {
		t.Errorf("classifier status: got %q, want \"error\"", cs.Status)
	}
	if !strings.Contains(cs.Detail, "connection refused") {
		t.Errorf("classifier detail: got %q, want it to contain the error string", cs.Detail)
	}

	// Storage should still be ok even though classifier is degraded.
	if cs, ok := hr.Components["storage"]; !ok || cs.Status != "ok" {
		t.Errorf("storage should remain ok, got %+v", cs)
	}
}

// TestHealth_MultipleRegisteredComponents verifies storage, metrics,
// classifier, and collector all appear together.
func TestHealth_MultipleRegisteredComponents(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "model loaded",
		Check:  func(ctx context.Context) error { return nil },
	})
	srv.RegisterHealthCheck(HealthCheck{
		Name:   "collector",
		Detail: "attached to 3 sessions",
		Check:  func(ctx context.Context) error { return nil },
	})

	hr := decodeHealth(t, srv)

	if hr.Status != "ok" {
		t.Errorf("top status: got %q, want \"ok\"", hr.Status)
	}
	for _, name := range []string{"storage", "metrics", "classifier", "collector"} {
		cs, ok := hr.Components[name]
		if !ok {
			t.Errorf("component %q missing", name)
			continue
		}
		if cs.Status != "ok" {
			t.Errorf("component %q status: got %q, want \"ok\"", name, cs.Status)
		}
	}

	if hr.Components["collector"].Detail != "attached to 3 sessions" {
		t.Errorf("collector detail: got %q, want %q",
			hr.Components["collector"].Detail, "attached to 3 sessions")
	}
}

// TestHealth_OneComponentFails_OthersStillReported ensures a single
// failure does not hide the other components' status.
func TestHealth_OneComponentFails_OthersStillReported(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "model loaded",
		Check:  func(ctx context.Context) error { return nil },
	})
	srv.RegisterHealthCheck(HealthCheck{
		Name:   "collector",
		Detail: "",
		Check:  func(ctx context.Context) error { return errors.New("ring buffer overflow: 500000 traces dropped") },
	})

	hr := decodeHealth(t, srv)

	if hr.Status != "degraded" {
		t.Errorf("top status: got %q, want \"degraded\"", hr.Status)
	}
	if cs := hr.Components["classifier"]; cs.Status != "ok" {
		t.Errorf("classifier should be ok: got %+v", cs)
	}
	if cs := hr.Components["collector"]; cs.Status != "error" {
		t.Errorf("collector should be error: got %+v", cs)
	}
	if cs := hr.Components["storage"]; cs.Status != "ok" {
		t.Errorf("storage should be ok: got %+v", cs)
	}
}

// TestHealth_DuplicateRegistration_Replaces verifies that registering a
// check with the same name as an existing one replaces the previous entry
// rather than duplicating it.
func TestHealth_DuplicateRegistration_Replaces(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "first",
		Check:  func(ctx context.Context) error { return nil },
	})
	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "second",
		Check:  func(ctx context.Context) error { return nil },
	})

	hr := decodeHealth(t, srv)
	cs, ok := hr.Components["classifier"]
	if !ok {
		t.Fatal("classifier missing")
	}
	if cs.Detail != "second" {
		t.Errorf("detail: got %q, want \"second\" (last registration should win)", cs.Detail)
	}

	// Exactly one entry named classifier should exist.
	count := 0
	snap := srv.snapshotHealthChecks()
	for _, hc := range snap {
		if hc.Name == "classifier" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate registration: expected 1 classifier entry, got %d", count)
	}
}

// TestHealth_PassiveCheck_NilCheckFunc verifies that a registered check
// with a nil Check func is treated as healthy and surfaces its detail.
func TestHealth_PassiveCheck_NilCheckFunc(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	srv.RegisterHealthCheck(HealthCheck{
		Name:   "classifier",
		Detail: "disabled",
		Check:  nil,
	})

	hr := decodeHealth(t, srv)
	cs, ok := hr.Components["classifier"]
	if !ok {
		t.Fatal("classifier missing")
	}
	if cs.Status != "ok" {
		t.Errorf("status: got %q, want \"ok\"", cs.Status)
	}
	if cs.Detail != "disabled" {
		t.Errorf("detail: got %q, want \"disabled\"", cs.Detail)
	}
}

// TestHealth_StorageFailure_DegradesOverall verifies that when storage
// returns an error, the top-level status is "degraded" and storage is
// marked "error". We close the underlying DB connection to force the
// SQLiteStore.Health probe to fail.
func TestHealth_StorageFailure_DegradesOverall(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	// Close the store out from under the server to force a probe failure.
	srv.store.Close()

	hr := decodeHealth(t, srv)
	if hr.Status != "degraded" {
		t.Errorf("top status: got %q, want \"degraded\"", hr.Status)
	}
	if cs := hr.Components["storage"]; cs.Status != "error" {
		t.Errorf("storage status: got %q, want \"error\"", cs.Status)
	}
}

// TestHealth_RegisterAfterStart verifies that a check registered after
// NewServer (the realistic serve-command wiring pattern) is picked up on
// the next /health request.
func TestHealth_RegisterAfterStart(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	// First request: no classifier.
	hr1 := decodeHealth(t, srv)
	if _, ok := hr1.Components["classifier"]; ok {
		t.Fatal("classifier should not be present before registration")
	}

	// Register after the server is already serving.
	srv.RegisterHealthCheck(HealthCheck{
		Name:   "collector",
		Detail: "attached",
		Check:  func(ctx context.Context) error { return nil },
	})

	hr2 := decodeHealth(t, srv)
	if _, ok := hr2.Components["collector"]; !ok {
		t.Fatal("collector should appear after post-start registration")
	}
}
