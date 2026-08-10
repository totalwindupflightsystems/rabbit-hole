// Package express — Prometheus /metrics endpoint and collector.
//
// Exposes runtime, classify, collector, and session metrics in the standard
// Prometheus text format. The endpoint is registered at /metrics and
// /api/v1/metrics and bypasses both API-key auth and per-endpoint rate
// limiting (consistent with /health).
//
// Architecture:
//
//   - All metric definitions live as package-level vars registered against a
//     dedicated *prometheus.Registry. This keeps the global default registry
//     untouched and prevents accidental duplicate-registration in tests.
//   - A background goroutine updates the gauges every 15s (goroutines,
//     uptime). Counters and histograms are recorded inline by their owners
//     (HTTP middleware, classify hook) — this file owns the registry and
//     goroutine-counting ticker; owners reference the exported vars.
//   - The Server owns a *Metrics handle created in NewServer and exposed via
//     Server.Metrics() for tests and external wiring.

package express

import (
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsCollectorInterval is how often runtime gauges (goroutine count,
// uptime) are refreshed. Counters and histograms are recorded inline and
// are always live. A var (not const) so tests can shrink it and keep
// -short runs fast (DF-013).
var MetricsCollectorInterval = 15 * time.Second

// Metrics owns the Prometheus registry and the background ticker that
// refreshes runtime-derived gauges. It is safe for concurrent use.
type MetricsCollector struct {
	registry  *prometheus.Registry
	startTime time.Time

	// Gauges refreshed by the background goroutine.
	goroutines     prometheus.Gauge
	uptimeSeconds  prometheus.Gauge
	sessionsActive prometheus.Gauge

	// Counters.
	tracesCollected prometheus.Counter
	bufferDrops     prometheus.Counter

	// Histogram of classify latency in seconds (observed from gemmaMetrics).
	classifyLatency prometheus.Histogram

	// HTTP request counter labeled by method/path/status. Updated by the
	// middleware wrapper.
	httpRequests *prometheus.CounterVec

	// Snapshotted values from collector / store. Set via SetBufferDrops /
	// SetTracesCollected / SetSessionsActive; consumed by the ticker.
	bufferDropsTotal atomic.Uint64
	tracesTotal      atomic.Uint64
	sessionsTotal    atomic.Int64

	// Classification latency samples fed in via ObserveClassifyLatency.
	// Last-sample-since-start is also tracked so the histogram always has
	// something to report even before any classification has happened.
	classifyLatencySum atomic.Uint64 // nanoseconds total
	classifyLatencyCnt atomic.Uint64

	stop chan struct{}
	done chan struct{}
}

// NewMetricsCollector constructs the collector, registers all metrics, and
// starts the background ticker. Pass nil for logger to use slog.Default().
func NewMetricsCollector(startTime time.Time, logger *slog.Logger) *MetricsCollector {
	if logger == nil {
		logger = slog.Default()
	}

	mc := &MetricsCollector{
		registry:  prometheus.NewRegistry(),
		startTime: startTime,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}

	mc.goroutines = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "rabbit_hole_goroutines",
		Help: "Number of goroutines that currently exist (runtime.NumGoroutine).",
	})
	mc.uptimeSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "rabbit_hole_uptime_seconds",
		Help: "Seconds since the server started.",
	})
	mc.sessionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "rabbit_hole_sessions_active",
		Help: "Total number of sessions in storage (sampled every collection interval).",
	})
	mc.tracesCollected = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rabbit_hole_traces_collected_total",
		Help: "Total number of traces collected since the server started.",
	})
	mc.bufferDrops = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rabbit_hole_buffer_drops_total",
		Help: "Total number of traces dropped from the collector ring buffer.",
	})
	mc.classifyLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "rabbit_hole_classify_latency_seconds",
		Help:    "Distribution of classification latency observed by the Gemma backend.",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 12), // 5ms .. ~10s
	})
	mc.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rabbit_hole_http_requests_total",
		Help: "Total HTTP requests handled, labeled by method, path, and status code.",
	}, []string{"method", "path", "status"})

	mc.registry.MustRegister(
		mc.goroutines,
		mc.uptimeSeconds,
		mc.sessionsActive,
		mc.tracesCollected,
		mc.bufferDrops,
		mc.classifyLatency,
		mc.httpRequests,
	)

	// Seed gauges so /metrics is non-empty before the first tick.
	mc.goroutines.Set(float64(runtime.NumGoroutine()))
	mc.uptimeSeconds.Set(0)

	// Pre-create a labeled combination on the HTTP counter so HELP/TYPE
	// lines are always emitted, even when no traffic has hit any handler
	// yet. Without this the vec shows up only after the first request.
	mc.httpRequests.WithLabelValues("GET", "unseen", "0").Add(0)

	go mc.run(logger)

	return mc
}

// run is the background ticker. It exits when stop is closed and signals
// completion on done.
func (mc *MetricsCollector) run(logger *slog.Logger) {
	defer close(mc.done)

	t := time.NewTicker(MetricsCollectorInterval)
	defer t.Stop()

	// Apply snapshot deltas on every tick.
	for {
		select {
		case <-mc.stop:
			return
		case <-t.C:
			mc.refresh(logger)
		}
	}
}

// refresh pulls atomic snapshots from collectors and updates gauges.
// Errors are logged but not fatal — a failed store query shouldn't kill
// the metrics endpoint.
func (mc *MetricsCollector) refresh(logger *slog.Logger) {
	mc.goroutines.Set(float64(runtime.NumGoroutine()))
	mc.uptimeSeconds.Set(time.Since(mc.startTime).Seconds())

	// Counter increments from cumulative snapshots.
	mc.bufferDrops.Add(float64(swapDelta(&mc.bufferDropsTotal)))
	mc.tracesCollected.Add(float64(swapDelta(&mc.tracesTotal)))

	mc.sessionsActive.Set(float64(mc.sessionsTotal.Load()))

	logger.Debug("metrics refreshed",
		"goroutines", runtime.NumGoroutine(),
		"uptime_s", time.Since(mc.startTime).Seconds(),
		"sessions", mc.sessionsTotal.Load(),
	)
}

// swapDelta atomically reads+zeroes a uint64 counter so each tick records
// only the new delta. Returns 0 on the first call before any value was set.
func swapDelta(c *atomic.Uint64) uint64 {
	return c.Swap(0)
}

// Handler returns an http.Handler that serves the registered metrics in
// Prometheus text format.
func (mc *MetricsCollector) Handler() http.Handler {
	return promhttp.HandlerFor(mc.registry, promhttp.HandlerOpts{
		Registry:          mc.registry,
		EnableOpenMetrics: false,
	})
}

// Registry exposes the underlying registry for tests that want to scrape
// metrics directly without going through HTTP.
func (mc *MetricsCollector) Registry() *prometheus.Registry { return mc.registry }

// Stop signals the background ticker to exit and waits for it to finish.
// Safe to call multiple times; subsequent calls are no-ops.
func (mc *MetricsCollector) Stop() {
	select {
	case <-mc.stop:
		return // already stopped
	default:
		close(mc.stop)
	}
	<-mc.done
}

// SetBufferDrops records the cumulative number of buffer drops observed
// from the collector. Called periodically by the server with
// collector.BufferStats().Dropped; only the delta is added to the counter.
func (mc *MetricsCollector) SetBufferDrops(total uint64) {
	// Store absolute total; the ticker converts to deltas.
	mc.bufferDropsTotal.Store(total)
}

// SetTracesCollected records the cumulative trace count.
func (mc *MetricsCollector) SetTracesCollected(total uint64) {
	mc.tracesTotal.Store(total)
}

// SetSessionsActive records the current session count.
func (mc *MetricsCollector) SetSessionsActive(total int64) {
	mc.sessionsTotal.Store(total)
}

// ObserveClassifyLatency records a single classification latency sample.
// Duration is converted from whatever unit the caller uses to seconds.
func (mc *MetricsCollector) ObserveClassifyLatency(d time.Duration) {
	if d <= 0 {
		return
	}
	mc.classifyLatency.Observe(d.Seconds())
	mc.classifyLatencySum.Add(uint64(d.Nanoseconds()))
	mc.classifyLatencyCnt.Add(1)
}

// IncHTTPRequests bumps the HTTP request counter. statusCode is recorded
// as a string label (e.g. "200", "404").
func (mc *MetricsCollector) IncHTTPRequests(method, path string, statusCode int) {
	mc.httpRequests.WithLabelValues(method, path, intToString(statusCode)).Inc()
}

// intToString is a small helper that avoids importing strconv in the hot
// path of HTTP request accounting. Only handles non-negative ints, which
// is sufficient for HTTP status codes (100-599).
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		// Should never happen; fall back to "0".
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
