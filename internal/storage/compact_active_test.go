package storage

import (
	"context"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func TestCompactPreservesRunningSessionAndRows(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	old := time.Now().UTC().Add(-2 * time.Hour)
	end := old.Add(time.Minute)
	for _, tc := range []struct {
		id     string
		status types.SessionStatus
	}{{"active", types.SessionStatusRunning}, {"finished", types.SessionStatusCompleted}} {
		sess := &types.Session{ID: tc.id, AgentPID: 1, AgentName: "test", StartTime: old, EndTime: &end, Status: tc.status}
		if err := store.StoreSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
		flow := types.Flow{ID: "flow-" + tc.id, SessionID: tc.id, StartTime: old, EndTime: end}
		if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(ctx, `INSERT INTO traces (id, session_id, timestamp, category) VALUES (?, ?, ?, ?)`, "trace-"+tc.id, tc.id, old.Format(time.RFC3339Nano), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Compact(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(ctx, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFlow(ctx, "flow-active"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want int
	}{{"active", 1}, {"finished", 0}} {
		var count int
		if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM traces WHERE session_id = ?`, tc.id).Scan(&count); err != nil || count != tc.want {
			t.Fatalf("trace count for %s = %d, want %d: %v", tc.id, count, tc.want, err)
		}
	}
	if _, err := store.GetSession(ctx, "finished"); err == nil {
		t.Fatal("completed session retained")
	}
	if _, err := store.GetFlow(ctx, "flow-finished"); err == nil {
		t.Fatal("completed flow retained")
	}
	now := time.Now().UTC()
	if err := store.StoreFlows(ctx, []types.Flow{{ID: "after", SessionID: "active", StartTime: now, EndTime: now}}); err != nil {
		t.Fatalf("insert after compact: %v", err)
	}
	sessions, err := store.ListSessions(ctx, 0, 10)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "active" {
		t.Fatalf("sessions=%v err=%v", sessions, err)
	}
}
