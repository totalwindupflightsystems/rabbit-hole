// Package attach — memory leak detection stress test.
//
// STR-007: 1000 attach/detach cycles verifying RSS returns to baseline.
// Uses runtime.ReadMemStats for Go heap tracking and /proc/self/statm for RSS
// (Linux only). Verifies that repeated session lifecycle operations do not
// cause unbounded memory growth.
package attach

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// RSS tripwire ceilings — see the semantics note on TestStress_MemoryLeak.
const (
	maxRSSGrowthPct   = 10.0     // 10% of the measured baseline
	maxRSSGrowthBytes = 50 << 20 // 50 MB, absolute
)

// warmupCycles are discarded attach/detach cycles run before the baseline is
// measured, so the process is already at steady state when measurement starts.
// The one-time cost that fixes this number is the race detector's shadow
// memory: it is mapped lazily per touched address, measured to add ~34 MB over
// the first 1000 cycles in an isolated -race run and ~115 MB over 3000 cycles
// when the rest of the package has already run, and it is never returned to the
// OS. A shorter warm-up leaves that growth inside the measured phase and the
// 10% arm then fires on a clean tree. Allocator arenas and stacks add ~1 MB.
// See docs/testing.md.
const warmupCycles = 3000

// TestStress_MemoryLeak runs 1000 attach/detach cycles through the
// SessionManager and verifies that memory returns to baseline (within
// a reasonable threshold).
//
// STR-007: Memory leak detection — 1000 attach/detach cycles.
// Verify RSS returns to baseline.
//
// # RSS gate semantics (DF-RABBIT-HOLE-010)
//
// The gate is an OR: the run FAILS when EITHER ceiling is exceeded.
//
//   - 10% of the measured baseline RSS (maxRSSGrowthPct) is the binding
//     ceiling whenever the baseline is under 500 MB — i.e. every configuration
//     this test runs in (measured baselines here: ~18 MB plain and ~143–158 MB
//     under -race, so 1.8–16 MB of headroom). It catches a leak that scales with
//     the process footprint, which is the shape a per-session/per-cycle leak
//     takes.
//   - 50 MB absolute (maxRSSGrowthBytes) is the binding ceiling above a
//     500 MB baseline, where 10% would be the looser of the two. It catches a
//     large leak even if the process is already big.
//
// Both arms are evaluated and each reports itself, so the effective allowance
// is the *smaller* (stricter) of the two at the measured baseline. The previous
// code ANDed them, i.e. demanded growth > 50 MB and > 10% at the same time.
// At an 18–40 MB baseline the 50 MB arm alone is already >125% growth, so the
// pair could never both hold and the tripwire could never fire — a leak could
// grow RSS 90%+ and the test stayed green.
//
// # What is measured, and why
//
// Two artifacts would otherwise be charged to the workload and drown a 10%
// ceiling (both measured on this host):
//
//   - the mock collector and mock store record every session they see, so the
//     fixture itself retains ~17 KB per cycle (~17 MB of heap per 1000 cycles).
//     Those records are the test's own ledger, not the code under test, so the
//     test drops them before each measurement.
//   - RSS above HeapAlloc is memory the Go runtime has freed but not yet
//     returned to the OS. Read naively, a clean 1000-cycle run "grows" 25–37%.
//     measureRSS therefore ends with debug.FreeOSMemory, so the number reflects
//     memory the process cannot release. Memory held by a real leak is live and
//     is not returned — it still trips the gate (red-proof: injecting a held
//     40 MB, which is >10% and <50 MB, fails the percentage arm at 210.8%).
//
// The measured phase is the full 1000 cycles; warmupCycles are extra and assert
// nothing.
func TestStress_MemoryLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const cycles = 1000

	// --- Setup ---

	coll := newMockCollector()
	store := newMockStorage()
	mgr := NewSessionManager(coll, store, nil)

	ctx := context.Background()

	// Warm up, then measure the baseline at steady state.
	runAttachCycles(t, ctx, mgr, 5000, warmupCycles)
	releaseFixtureSessions(t, coll, store)

	baselineRSS := measureRSS(t)
	baselineHeap := readHeapAlloc(t)
	t.Logf("baseline: heap=%d bytes, RSS=%d bytes (%.1f MB) after %d warm-up cycles",
		baselineHeap, baselineRSS, float64(baselineRSS)/(1<<20), warmupCycles)

	// --- Run 1000 attach/detach cycles ---

	start := time.Now()
	runAttachCycles(t, ctx, mgr, 10000, cycles)

	elapsed := time.Since(start)
	t.Logf("completed %d cycles in %s (%.2f ops/s)",
		cycles, elapsed, float64(cycles)/elapsed.Seconds())

	// Measure after releasing the fixture's ledger.
	releaseFixtureSessions(t, coll, store)
	afterRSS := measureRSS(t)
	afterHeap := readHeapAlloc(t)
	t.Logf("after:    heap=%d bytes, RSS=%d bytes (%.1f MB)",
		afterHeap, afterRSS, float64(afterRSS)/(1<<20))

	// --- Assertions ---

	// Heap: allow up to 5 MB growth (allocated strings, session metadata, etc.)
	heapDelta := int64(afterHeap) - int64(baselineHeap)
	t.Logf("heap delta: %d bytes (%.1f KB)", heapDelta, float64(heapDelta)/1024)

	const maxHeapGrowth = 5 << 20 // 5 MB
	if heapDelta > maxHeapGrowth {
		t.Errorf("heap grew by %d bytes (%.1f MB), threshold is %d bytes (%.1f MB) — possible memory leak",
			heapDelta, float64(heapDelta)/(1<<20),
			maxHeapGrowth, float64(maxHeapGrowth)/(1<<20))
	}

	// RSS: fail on either ceiling — see the semantics note in the doc comment.
	rssDelta := int64(afterRSS) - int64(baselineRSS)
	rssPct := float64(rssDelta) / float64(baselineRSS) * 100
	t.Logf("RSS delta: %d bytes (%.1f MB, %.1f%%)", rssDelta, float64(rssDelta)/(1<<20), rssPct)

	if rssDelta > maxRSSGrowthBytes {
		t.Errorf("RSS grew by %d bytes (%.1f MB, %.1f%%) — exceeds absolute threshold %d MB — possible memory leak",
			rssDelta, float64(rssDelta)/(1<<20), rssPct,
			int64(maxRSSGrowthBytes)/(1<<20))
	}
	if rssPct > maxRSSGrowthPct {
		t.Errorf("RSS grew by %d bytes (%.1f MB, %.1f%%) — exceeds percentage threshold %.1f%% of a %.1f MB baseline — possible memory leak",
			rssDelta, float64(rssDelta)/(1<<20), rssPct,
			maxRSSGrowthPct, float64(baselineRSS)/(1<<20))
	}
}

// TestStress_MemoryLeakParallel runs the leak test with concurrent
// session lifecycle operations to catch data-race-induced leaks.
//
// It asserts heap and RSS ceilings, not wall-clock time, so it stays valid in
// both modes the suite is run in — `make test-all` (-race, mandatory) and plain
// `go test`. Wall-clock SLO budgets elsewhere in the module are per-host
// budgets calibrated WITHOUT -race; see docs/testing.md before treating a
// timing breach under -race as a regression.
//
// The RSS gate is the same OR gate (either ceiling trips it) documented on
// TestStress_MemoryLeak, and it measures the same way: warm-up, release the
// fixture ledger, debug.FreeOSMemory before each reading.
func TestStress_MemoryLeakParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const cycles = 1000
	const goroutines = 4

	coll := newMockCollector()
	store := newMockStorage()

	runConcurrentAttachCycles(t, coll, store, 80000, goroutines, warmupCycles)
	releaseFixtureSessions(t, coll, store)

	baselineRSS := measureRSS(t)
	baselineHeap := readHeapAlloc(t)
	t.Logf("baseline: heap=%d bytes, RSS=%d bytes (%.1f MB) after %d warm-up cycles",
		baselineHeap, baselineRSS, float64(baselineRSS)/(1<<20), warmupCycles)

	start := time.Now()
	runConcurrentAttachCycles(t, coll, store, 90000, goroutines, cycles)
	elapsed := time.Since(start)
	t.Logf("completed %d concurrent cycles in %s (%.2f ops/s)",
		cycles, elapsed, float64(cycles)/elapsed.Seconds())

	releaseFixtureSessions(t, coll, store)
	afterRSS := measureRSS(t)
	afterHeap := readHeapAlloc(t)
	t.Logf("after:    heap=%d bytes, RSS=%d bytes (%.1f MB)",
		afterHeap, afterRSS, float64(afterRSS)/(1<<20))

	heapDelta := int64(afterHeap) - int64(baselineHeap)
	const maxHeapGrowth = 10 << 20 // 10 MB — higher for concurrency overhead
	if heapDelta > maxHeapGrowth {
		t.Errorf("heap grew by %d bytes (%.1f MB), threshold is %d bytes (%.1f MB) — possible memory leak",
			heapDelta, float64(heapDelta)/(1<<20),
			maxHeapGrowth, float64(maxHeapGrowth)/(1<<20))
	}

	rssDelta := int64(afterRSS) - int64(baselineRSS)
	rssPct := float64(rssDelta) / float64(baselineRSS) * 100
	t.Logf("RSS delta: %d bytes (%.1f MB, %.1f%%)", rssDelta, float64(rssDelta)/(1<<20), rssPct)

	if rssDelta > maxRSSGrowthBytes {
		t.Errorf("RSS grew by %d bytes (%.1f MB, %.1f%%) — exceeds absolute threshold %d MB — possible memory leak",
			rssDelta, float64(rssDelta)/(1<<20), rssPct,
			int64(maxRSSGrowthBytes)/(1<<20))
	}
	if rssPct > maxRSSGrowthPct {
		t.Errorf("RSS grew by %d bytes (%.1f MB, %.1f%%) — exceeds percentage threshold %.1f%% of a %.1f MB baseline — possible memory leak",
			rssDelta, float64(rssDelta)/(1<<20), rssPct,
			maxRSSGrowthPct, float64(baselineRSS)/(1<<20))
	}
}

// --- Helpers ---

// runAttachCycles drives n sequential attach/detach cycles through mgr and
// fails the test on the first lifecycle error. PIDs are drawn from startPID so
// the warm-up phase and the measured phase never collide.
func runAttachCycles(t *testing.T, ctx context.Context, mgr *SessionManager, startPID int32, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		session, err := mgr.StartSession(ctx, startPID+int32(i), collector.CollectOptions{})
		if err != nil {
			t.Fatalf("cycle %d: StartSession: %v", i, err)
		}
		if session == nil {
			t.Fatalf("cycle %d: nil session", i)
		}
		if err := mgr.StopSession(ctx, session.ID); err != nil {
			t.Fatalf("cycle %d: StopSession: %v", i, err)
		}
	}
}

// runConcurrentAttachCycles drives total cycles split across goroutines, each
// with its own SessionManager over the shared collector and store, so shared
// state is exercised from several goroutines. It fails the test on the first
// lifecycle error.
func runConcurrentAttachCycles(t *testing.T, coll collector.Collector, store SessionStore, basePID int32, goroutines, total int) {
	t.Helper()

	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func(offset int32) {
			mgr := NewSessionManager(coll, store, nil)
			ctx := context.Background()
			pidBase := basePID + offset
			for i := 0; i < total/goroutines; i++ {
				session, err := mgr.StartSession(ctx, pidBase+int32(i), collector.CollectOptions{})
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d StartSession: %w", offset, i, err)
					return
				}
				if session == nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: nil session", offset, i)
					return
				}
				if err := mgr.StopSession(ctx, session.ID); err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d StopSession: %w", offset, i, err)
					return
				}
			}
			errCh <- nil
		}(int32(g))
	}

	for g := 0; g < goroutines; g++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

// releaseFixtureSessions drops every session record the mock collector and mock
// store retain. Both mocks keep a record per cycle so other tests can inspect
// history; that ledger is the fixture's bookkeeping, not memory the attach/
// detach path leaks, and at ~17 KB per cycle it would swamp the RSS ceiling.
// Dropping the references here leaves only what the code under test still
// holds. Call it before each measurement.
func releaseFixtureSessions(t *testing.T, coll *mockCollector, store *mockStorage) {
	t.Helper()

	coll.mu.Lock()
	coll.sessions = make(map[string]*types.Session)
	coll.traceCh = make(map[string]chan types.Trace)
	coll.mu.Unlock()

	store.mu.Lock()
	store.sessions = nil
	store.flows = nil
	store.mu.Unlock()
}

// measureRSS returns the RSS the process cannot release: it forces a GC, then
// debug.FreeOSMemory so freed-but-unreturned pages are handed back to the OS.
// Without that step RSS still counts pages the runtime has already freed, and a
// clean run would look like 25–37% growth. Live memory held by a leak cannot be
// freed and is still measured.
func measureRSS(t *testing.T) uint64 {
	t.Helper()
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(100 * time.Millisecond)
	return readRSS(t)
}

// readHeapAlloc returns the current heap allocation in bytes from
// runtime.ReadMemStats.
func readHeapAlloc(t *testing.T) uint64 {
	t.Helper()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// readRSS returns the resident set size in bytes for the current
// process. Uses /proc/self/statm on Linux; skips on other platforms.
func readRSS(t *testing.T) uint64 {
	t.Helper()

	const statmPath = "/proc/self/statm"
	data, err := os.ReadFile(statmPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("skipping RSS check: %s not available (non-Linux?)", statmPath)
		}
		t.Skipf("skipping RSS check: read %s: %v", statmPath, err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		t.Skipf("skipping RSS check: unexpected %s format: %q", statmPath, string(data))
	}

	// Field 1 is resident set size in pages.
	rssPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		t.Skipf("skipping RSS check: parse RSS field: %v", err)
	}

	return rssPages * uint64(os.Getpagesize())
}
