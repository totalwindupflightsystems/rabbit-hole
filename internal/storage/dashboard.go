package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// DashboardSummary aggregates the metrics the web dashboard renders on its
// overview page: totals, error rate, activity by hour, phase and outcome
// breakdowns, and the most common intents.
type DashboardSummary struct {
	TotalFlows    int64            `json:"total_flows"`
	TotalSessions int64            `json:"total_sessions"`
	TotalTraces   int64            `json:"total_traces"`
	ErrorCount    int64            `json:"error_count"` // failure + timeout
	AvgDurationNS int64            `json:"avg_duration_ns"`
	ByPhase       map[string]int64 `json:"by_phase"`
	ByOutcome     map[string]int64 `json:"by_outcome"`
	TopIntents    []IntentCount    `json:"top_intents"`
	Hourly        []HourlyCount    `json:"hourly"` // last 24h, oldest first
	RecentFlows   []types.Flow     `json:"recent_flows"`
}

// IntentCount is a single (intent, count) pair for the top-intents list.
type IntentCount struct {
	Intent string `json:"intent"`
	Count  int64  `json:"count"`
}

// HourlyCount is the number of flows started in one UTC hour bucket.
type HourlyCount struct {
	Hour  time.Time `json:"hour"`
	Count int64     `json:"count"`
}

// DashboardSummary computes the overview aggregates in a single pass over
// the flows table plus three lightweight aggregate queries.
func (s *SQLiteStore) DashboardSummary(ctx context.Context) (*DashboardSummary, error) {
	sum := &DashboardSummary{
		ByPhase:   make(map[string]int64),
		ByOutcome: make(map[string]int64),
	}

	// Totals + error count + average duration.
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN outcome IN ('failure','timeout') THEN 1 ELSE 0 END), 0),
		       COALESCE(AVG(duration_ns), 0)
		FROM flows`)
	var avgDur float64
	if err := row.Scan(&sum.TotalFlows, &sum.ErrorCount, &avgDur); err != nil {
		return nil, fmt.Errorf("dashboard totals: %w", err)
	}
	sum.AvgDurationNS = int64(avgDur)

	// Session and trace totals from the other tables.
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sum.TotalSessions); err != nil {
		return nil, fmt.Errorf("dashboard sessions: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traces`).Scan(&sum.TotalTraces); err != nil {
		return nil, fmt.Errorf("dashboard traces: %w", err)
	}

	// Phase breakdown.
	rows, err := s.db.QueryContext(ctx, `SELECT phase, COUNT(*) FROM flows GROUP BY phase`)
	if err != nil {
		return nil, fmt.Errorf("dashboard phases: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var phase string
		var n int64
		if err := rows.Scan(&phase, &n); err != nil {
			return nil, err
		}
		sum.ByPhase[phase] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Outcome breakdown.
	rows, err = s.db.QueryContext(ctx, `SELECT outcome, COUNT(*) FROM flows GROUP BY outcome`)
	if err != nil {
		return nil, fmt.Errorf("dashboard outcomes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var outcome string
		var n int64
		if err := rows.Scan(&outcome, &n); err != nil {
			return nil, err
		}
		sum.ByOutcome[outcome] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Top intents.
	rows, err = s.db.QueryContext(ctx, `
		SELECT intent, COUNT(*) AS n FROM flows
		GROUP BY intent ORDER BY n DESC LIMIT 8`)
	if err != nil {
		return nil, fmt.Errorf("dashboard intents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ic IntentCount
		if err := rows.Scan(&ic.Intent, &ic.Count); err != nil {
			return nil, err
		}
		sum.TopIntents = append(sum.TopIntents, ic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Hourly activity for the last 24 hours (UTC).
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	rows, err = s.db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%dT%H:00:00Z', start_time) AS hour, COUNT(*)
		FROM flows WHERE start_time >= ? GROUP BY hour ORDER BY hour`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("dashboard hourly: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		var n int64
		if err := rows.Scan(&h, &n); err != nil {
			return nil, err
		}
		ht, err := time.Parse("2006-01-02T15:04:05Z", h)
		if err != nil {
			continue
		}
		sum.Hourly = append(sum.Hourly, HourlyCount{Hour: ht, Count: n})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Most recent flows for the table preview.
	recent, _, err := s.QueryFlows(ctx, types.FlowQuery{Limit: 12})
	if err != nil {
		return nil, fmt.Errorf("dashboard recent: %w", err)
	}
	sum.RecentFlows = recent

	return sum, nil
}
