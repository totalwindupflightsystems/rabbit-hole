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

// Session age must likewise be judged by instant: a host offset ahead of the
// writer's must not delete a session that is still inside the retention
// window, and a host offset behind must not strand an expired one.
func TestCompactSessionRetentionUsesInstant(t *testing.T) {
	ctx := context.Background()
	cutoff := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		hostHours int
		endDelta  time.Duration
		wantKept  bool
	}{
		// Host behind UTC: local cutoff wall clock sorts BEFORE the expired
		// session's UTC string, so a string compare under-deletes it.
		{name: "host_behind_expired_session", hostHours: -5, endDelta: -time.Hour, wantKept: false},
		// Host ahead of UTC: local cutoff wall clock sorts AFTER the live
		// session's UTC string, so a string compare over-deletes it.
		{name: "host_ahead_live_session", hostHours: +5, endDelta: time.Hour, wantKept: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			end := cutoff.Add(tc.endDelta)
			if err := store.StoreSession(ctx, &types.Session{
				ID: "sess", AgentPID: 1, AgentName: "test",
				StartTime: end.Add(-time.Minute), EndTime: &end,
				Status: types.SessionStatusCompleted,
			}); err != nil {
				t.Fatal(err)
			}
			localCutoff := cutoff.In(time.FixedZone("host", tc.hostHours*60*60))
			if err := store.Compact(ctx, localCutoff); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = 'sess'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if kept := n == 1; kept != tc.wantKept {
				t.Errorf("cutoff=%s session_end=%s kept=%v want kept=%v",
					localCutoff.Format(time.RFC3339), end.Format(time.RFC3339), kept, tc.wantKept)
			}
		})
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
