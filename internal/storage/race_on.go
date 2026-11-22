//go:build race
// +build race

package storage

// raceEnabled reports whether the test binary was built with -race.
//
// The stress SLO tests that assert wall-clock budgets
// (TestStress_100KTraceInsert, TestStress_ConcurrentFTS5Search,
// TestStress_WALPressure) skip themselves when this is true: their budgets
// are calibrated without the race detector, which inflates the SQLite-heavy
// paths 20-55x (see docs/testing.md). The budgets remain enforced without
// -race via `make test-slo`. DF-RABBIT-HOLE-013.
const raceEnabled = true
