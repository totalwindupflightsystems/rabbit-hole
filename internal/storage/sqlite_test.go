package storage

import (
	"context"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// newLocalZone returns a fixed UTC-5 zone like the production host, used to
// simulate flows being written with LOCAL offset timestamps (DF-034).
func newLocalZone() *time.Location {
	return time.FixedZone("LOCAL", -5*60*60)
}

// TestQueryFlows_TimezoneAgnosticTimeRange is a regression test for DF-034.
//
// Flows are stored with LOCAL-offset RFC3339Nano timestamps (e.g.
// "2026-08-27T05:11:39.436655825-05:00") while the chat model's translate
// step emits UTC "Z" timestamps (e.g. "2026-08-27T10:11:26Z"). Raw SQLite
// TEXT comparison sorts a UTC 'Z' timestamp BEFORE a '-05:00' timestamp for
// the same instant, so time-window queries matched 0 rows. Comparisons must
// go through unixepoch() so both sides normalize to UTC seconds.
func TestQueryFlows_TimezoneAgnosticTimeRange(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	local := newLocalZone()

	session := &types.Session{
		ID:        "ses-tz-001",
		AgentPID:  12345,
		AgentName: "hermes",
		StartTime: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "hermes chat",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Instant 2026-08-27T10:11:39Z — 09 fractional digits, written in LOCAL (-05:00).
	inWindow := types.Flow{
		ID: "fl-tz-in", SessionID: session.ID, Intent: "deploy_service",
		Phase: types.FlowPhaseAction, Description: "inside the UTC window",
		Outcome: types.FlowOutcomeSuccess, Confidence: 0.9,
		StartTime: time.Date(2026, 8, 27, 5, 11, 39, 436655825, local), // 10:11:39Z
		EndTime:   time.Date(2026, 8, 27, 5, 11, 40, 0, local),
		Duration:  time.Second,
	}
	// 09:00:00Z — before the UTC query window.
	beforeWindow := types.Flow{
		ID: "fl-tz-before", SessionID: session.ID, Intent: "read_logs",
		Phase: types.FlowPhaseObservation, Description: "before the UTC window",
		Outcome: types.FlowOutcomeSuccess, Confidence: 0.8,
		StartTime: time.Date(2026, 8, 27, 4, 0, 0, 0, local), // 09:00:00Z
		EndTime:   time.Date(2026, 8, 27, 4, 0, 1, 0, local),
		Duration:  time.Second,
	}
	// 12:00:00Z — after the UTC query window.
	afterWindow := types.Flow{
		ID: "fl-tz-after", SessionID: session.ID, Intent: "read_logs",
		Phase: types.FlowPhaseObservation, Description: "after the UTC window",
		Outcome: types.FlowOutcomeSuccess, Confidence: 0.8,
		StartTime: time.Date(2026, 8, 27, 7, 0, 0, 0, local), // 12:00:00Z
		EndTime:   time.Date(2026, 8, 27, 7, 0, 1, 0, local),
		Duration:  time.Second,
	}
	if err := store.StoreFlows(ctx, []types.Flow{inWindow, beforeWindow, afterWindow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// Query with UTC "Z" timestamps spanning the exact instant of inWindow.
	t.Run("utc z range matches local-offset row", func(t *testing.T) {
		flows, _, err := store.QueryFlows(ctx, types.FlowQuery{
			TimeRange: types.TimeRange{
				Start: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC),
			},
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("QueryFlows: %v", err)
		}
		if len(flows) != 1 {
			t.Fatalf("len(flows) = %d, want 1 (DF-034: UTC Z query must match LOCAL-offset rows)", len(flows))
		}
		if flows[0].ID != inWindow.ID {
			t.Errorf("flow ID = %q, want %q", flows[0].ID, inWindow.ID)
		}
		if !flows[0].StartTime.Equal(inWindow.StartTime) {
			t.Errorf("StartTime = %v, want instant %v", flows[0].StartTime, inWindow.StartTime)
		}
	})

	// Start-only boundary: 10:00:00Z must include inWindow (10:11:39Z) and
	// afterWindow (12:00:00Z) but exclude beforeWindow (09:00:00Z).
	t.Run("utc z start boundary", func(t *testing.T) {
		flows, _, err := store.QueryFlows(ctx, types.FlowQuery{
			TimeRange: types.TimeRange{
				Start: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC),
			},
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("QueryFlows: %v", err)
		}
		if len(flows) != 2 {
			t.Fatalf("len(flows) = %d, want 2", len(flows))
		}
		for _, f := range flows {
			if f.ID == beforeWindow.ID {
				t.Errorf("flow %q (09:00:00Z) matched start boundary 10:00:00Z", f.ID)
			}
		}
	})
}

// TestCompact_TimezoneAgnosticCutoff is a regression test for DF-034 on the
// Compact maintenance path: the flows DELETE must compare via unixepoch() so
// a UTC cutoff correctly ages out rows stored with LOCAL offsets.
func TestCompact_TimezoneAgnosticCutoff(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	local := newLocalZone()

	session := &types.Session{
		ID:        "ses-tz-compact",
		AgentPID:  4242,
		AgentName: "hermes",
		StartTime: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "hermes chat",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	oldFlow := types.Flow{
		ID: "fl-tz-compact-old", SessionID: session.ID, Intent: "old",
		Phase: types.FlowPhaseAction, Description: "older than cutoff",
		Outcome: types.FlowOutcomeSuccess, Confidence: 0.5,
		StartTime: time.Date(2026, 8, 26, 23, 0, 0, 0, local), // 2026-08-27T04:00:00Z
		EndTime:   time.Date(2026, 8, 26, 23, 0, 1, 0, local),
		Duration:  time.Second,
	}
	keepFlow := types.Flow{
		ID: "fl-tz-compact-keep", SessionID: session.ID, Intent: "keep",
		Phase: types.FlowPhaseAction, Description: "newer than cutoff",
		Outcome: types.FlowOutcomeSuccess, Confidence: 0.5,
		StartTime: time.Date(2026, 8, 27, 14, 0, 0, 0, local), // 2026-08-27T19:00:00Z
		EndTime:   time.Date(2026, 8, 27, 14, 0, 1, 0, local),
		Duration:  time.Second,
	}
	if err := store.StoreFlows(ctx, []types.Flow{oldFlow, keepFlow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// UTC cutoff between the two instants (12:00:00Z).
	if err := store.Compact(ctx, time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if _, err := store.GetFlow(ctx, oldFlow.ID); err == nil {
		t.Errorf("GetFlow(%q): expected error after Compact, got nil (DF-034: UTC cutoff must age out LOCAL-offset rows)", oldFlow.ID)
	}

	got, err := store.GetFlow(ctx, keepFlow.ID)
	if err != nil {
		t.Fatalf("GetFlow(%q) after Compact: %v", keepFlow.ID, err)
	}
	if !got.StartTime.Equal(keepFlow.StartTime) {
		t.Errorf("kept flow StartTime = %v, want %v", got.StartTime, keepFlow.StartTime)
	}
}
