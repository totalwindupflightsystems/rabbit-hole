package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// UTC writers and a UTC-5 cutoff must identify the same absolute instants.
func TestCompactWriterOffsetDiffersFromCutoff(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cutoff := time.Date(2026, 11, 20, 12, 42, 23, 0, time.UTC)
	localCutoff := cutoff.In(time.FixedZone("host", -5*60*60))
	session := &types.Session{ID: "offset", AgentPID: 1, AgentName: "test", StartTime: cutoff.Add(-8 * time.Hour), Status: types.SessionStatusCompleted}
	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	for i, delta := range []time.Duration{-6 * time.Hour, -4 * time.Hour, -time.Second, 0, time.Second} {
		when := cutoff.Add(delta)
		id := fmt.Sprintf("%d", i)
		if err := store.StoreFlows(ctx, []types.Flow{{ID: "flow-" + id, SessionID: session.ID, StartTime: when, EndTime: when}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(ctx, `INSERT INTO traces (id, session_id, timestamp, category) VALUES (?, ?, ?, ?)`, "trace-"+id, session.ID, when.Format(time.RFC3339), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Compact(ctx, localCutoff); err != nil {
		t.Fatal(err)
	}
	var traceFloor, flowFloor string
	var traces, flows int
	if err := store.DB().QueryRowContext(ctx, `SELECT MIN(timestamp), COUNT(*) FROM traces`).Scan(&traceFloor, &traces); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT MIN(start_time), COUNT(*) FROM flows`).Scan(&flowFloor, &flows); err != nil {
		t.Fatal(err)
	}
	t.Logf("cutoff=%s local_cutoff=%s traces_floor=%s flows_floor=%s traces=%d flows=%d ratio=%.3f", cutoff.Format(time.RFC3339), localCutoff.Format(time.RFC3339), traceFloor, flowFloor, traces, flows, float64(traces)/float64(flows))
	traceTime, err := time.Parse(time.RFC3339Nano, traceFloor)
	if err != nil {
		t.Fatal(err)
	}
	flowTime, err := time.Parse(time.RFC3339Nano, flowFloor)
	if err != nil {
		t.Fatal(err)
	}
	if !traceTime.Equal(cutoff) || !flowTime.Equal(cutoff) || traces != 2 || flows != 2 {
		t.Errorf("offset mismatch: expected floors at cutoff and 2 rows in each table")
	}
}

func TestCompactKeepsMalformedTimestamps(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	cutoff := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	end := cutoff.Add(-time.Hour)
	for _, id := range []string{"malformed-trace", "malformed-flow"} {
		if err := store.StoreSession(ctx, &types.Session{ID: id, AgentPID: 1, AgentName: "test", StartTime: end, EndTime: &end, Status: types.SessionStatusCompleted}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO traces (id, session_id, timestamp, category) VALUES ('bad', 'malformed-trace', 'invalid', 'test')`); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreFlows(ctx, []types.Flow{{ID: "bad-flow", SessionID: "malformed-flow", StartTime: end, EndTime: end}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE flows SET start_time = 'invalid' WHERE id = 'bad-flow'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT COUNT(*) FROM traces WHERE id = 'bad'`, `SELECT COUNT(*) FROM flows WHERE id = 'bad-flow'`} {
		var n int
		if err := store.DB().QueryRowContext(ctx, query).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s: count=%d err=%v", query, n, err)
		}
	}
}
