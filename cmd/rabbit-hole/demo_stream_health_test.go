package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/express"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
)

func TestDemoStreamHealthReportsWriteFailureAndRecovery(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	srv := express.NewServer(store, nil, "127.0.0.1:0", nil)
	h := &demoStreamHealth{}
	srv.RegisterHealthCheck(express.HealthCheck{Name: "demo_stream", Check: h.Check})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	check := func(want string) {
		t.Helper()
		resp, err := http.Get("http://" + srv.Addr() + "/health")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Status     string `json:"status"`
			Components map[string]struct {
				Status string `json:"status"`
			} `json:"components"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if out.Components["demo_stream"].Status != want {
			t.Fatalf("health: %+v", out)
		}
		if want == "error" && out.Status != "degraded" {
			t.Fatalf("health: %+v", out)
		}
	}
	check("error")
	h.Set(errors.New("write failed"))
	check("error")
	h.Set(nil)
	check("ok")
}
