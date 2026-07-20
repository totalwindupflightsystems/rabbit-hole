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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
)

// TestStress_MemoryLeak runs 1000 attach/detach cycles through the
// SessionManager and verifies that memory returns to baseline (within
// a reasonable threshold).
//
// STR-007: Memory leak detection — 1000 attach/detach cycles.
// Verify RSS returns to baseline.
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

	// Force GC and measure baseline.
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	baselineHeap := readHeapAlloc(t)
	baselineRSS := readRSS(t)
	t.Logf("baseline: heap=%d bytes, RSS=%d bytes (%.1f MB)",
		baselineHeap, baselineRSS, float64(baselineRSS)/(1<<20))

	// --- Run 1000 attach/detach cycles ---

	start := time.Now()
	pidCounter := int32(10000)

	for i := 0; i < cycles; i++ {
		pidCounter++
		session, err := mgr.StartSession(ctx, pidCounter, collector.CollectOptions{})
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

	elapsed := time.Since(start)
	t.Logf("completed %d cycles in %s (%.2f ops/s)",
		cycles, elapsed, float64(cycles)/elapsed.Seconds())

	// Force GC and measure after.
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	afterHeap := readHeapAlloc(t)
	afterRSS := readRSS(t)
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

	// RSS: allow up to 10% growth or 50 MB, whichever is larger.
	// RSS includes Go runtime overhead, GC arenas that haven't been
	// released to the OS, etc. We're looking for unbounded growth.
	rssDelta := int64(afterRSS) - int64(baselineRSS)
	rssPct := float64(rssDelta) / float64(baselineRSS) * 100
	t.Logf("RSS delta: %d bytes (%.1f MB, %.1f%%)", rssDelta, float64(rssDelta)/(1<<20), rssPct)

	const maxRSSGrowthBytes = 50 << 20 // 50 MB
	const maxRSSGrowthPct = 10.0       // 10%

	if rssDelta > maxRSSGrowthBytes && rssPct > maxRSSGrowthPct {
		t.Errorf("RSS grew by %d bytes (%.1f MB, %.1f%%) — exceeds both absolute threshold %d MB and percentage threshold %.1f%% — possible memory leak",
			rssDelta, float64(rssDelta)/(1<<20), rssPct,
			maxRSSGrowthBytes>>20, maxRSSGrowthPct)
	}
}

// TestStress_MemoryLeakParallel runs the leak test with concurrent
// session lifecycle operations to catch data-race-induced leaks.
func TestStress_MemoryLeakParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	const cycles = 1000
	const goroutines = 4

	coll := newMockCollector()
	store := newMockStorage()

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	baselineHeap := readHeapAlloc(t)
	baselineRSS := readRSS(t)
	t.Logf("baseline: heap=%d bytes, RSS=%d bytes (%.1f MB)",
		baselineHeap, baselineRSS, float64(baselineRSS)/(1<<20))

	start := time.Now()

	// Each goroutine gets its own SessionManager (same collector + store)
	// to test that shared state doesn't leak.
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func(offset int32) {
			mgr := NewSessionManager(coll, store, nil)
			ctx := context.Background()
			basePID := int32(90000) + offset
			for i := 0; i < cycles/goroutines; i++ {
				pid := basePID + int32(i)
				session, err := mgr.StartSession(ctx, pid, collector.CollectOptions{})
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d StartSession: %w", offset, i, err)
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

	elapsed := time.Since(start)
	t.Logf("completed %d concurrent cycles in %s (%.2f ops/s)",
		cycles, elapsed, float64(cycles)/elapsed.Seconds())

	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	afterHeap := readHeapAlloc(t)
	afterRSS := readRSS(t)
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
	const maxRSSGrowthBytes = 50 << 20 // 50 MB
	const maxRSSGrowthPct = 10.0       // 10%
	rssPct := float64(rssDelta) / float64(baselineRSS) * 100

	if rssDelta > maxRSSGrowthBytes && rssPct > maxRSSGrowthPct {
		t.Errorf("RSS grew by %d bytes (%.1f MB, %.1f%%) — exceeds thresholds — possible memory leak",
			rssDelta, float64(rssDelta)/(1<<20), rssPct)
	}
}

// --- Helpers ---

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
