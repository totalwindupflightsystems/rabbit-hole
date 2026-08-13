package storage

import (
	"context"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/demo"
)

func TestDashboardSummary_Empty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	sum, err := store.DashboardSummary(context.Background())
	if err != nil {
		t.Fatalf("DashboardSummary: %v", err)
	}
	if sum.TotalFlows != 0 {
		t.Errorf("TotalFlows = %d, want 0", sum.TotalFlows)
	}
	if len(sum.RecentFlows) != 0 {
		t.Errorf("RecentFlows = %d, want 0", len(sum.RecentFlows))
	}
}

func TestDashboardSummary_Seeded(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	start := time.Now().Add(-3 * time.Hour)
	sc := demo.Generate(40, start, 42)

	if err := store.StoreSession(ctx, &sc.Session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := store.StoreFlows(ctx, sc.Flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	sum, err := store.DashboardSummary(ctx)
	if err != nil {
		t.Fatalf("DashboardSummary: %v", err)
	}

	if sum.TotalFlows != 40 {
		t.Errorf("TotalFlows = %d, want 40", sum.TotalFlows)
	}
	if sum.TotalSessions != 1 {
		t.Errorf("TotalSessions = %d, want 1", sum.TotalSessions)
	}
	if sum.AvgDurationNS <= 0 {
		t.Errorf("AvgDurationNS = %d, want > 0", sum.AvgDurationNS)
	}

	// Every seeded flow is either success/failure/timeout/unknown.
	total := int64(0)
	for _, n := range sum.ByOutcome {
		total += n
	}
	if total != 40 {
		t.Errorf("ByOutcome sums to %d, want 40", total)
	}

	if len(sum.TopIntents) == 0 {
		t.Error("TopIntents empty, want at least 1")
	}
	if len(sum.RecentFlows) != 12 {
		t.Errorf("RecentFlows = %d, want 12 (dashboard preview cap)", len(sum.RecentFlows))
	}
}

func TestDashboardSummary_ErrorCount(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	sc := demo.Generate(50, time.Now().Add(-1*time.Hour), 7)
	if err := store.StoreSession(ctx, &sc.Session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := store.StoreFlows(ctx, sc.Flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	sum, err := store.DashboardSummary(ctx)
	if err != nil {
		t.Fatalf("DashboardSummary: %v", err)
	}

	fail := sum.ByOutcome["failure"]
	timeout := sum.ByOutcome["timeout"]
	if sum.ErrorCount != fail+timeout {
		t.Errorf("ErrorCount = %d, want %d (failure %d + timeout %d)", sum.ErrorCount, fail+timeout, fail, timeout)
	}
}
