package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestServeHealthVersionEqualsCLIVersion verifies that the serve command's
// wiring (wireHealthVersion) publishes the CLI build version to the express
// server, so GET /health reports the same version string as
// `rabbit-hole version` (DF-021).
func TestServeHealthVersionEqualsCLIVersion(t *testing.T) {
	oldVersion := Version
	Version = "v9.9.9-wired"
	defer func() { Version = oldVersion }()

	// newServeCmd's RunE calls wireHealthVersion() after constructing the
	// server; exercise the same seam.
	wireHealthVersion()

	srv, _ := startTestDaemon(t)

	resp, err := http.Get("http://" + srv.Addr() + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	var hr struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		t.Fatalf("decode /health: %v", err)
	}

	if hr.Version != Version {
		t.Errorf("health version = %q, want CLI version %q", hr.Version, Version)
	}
}
