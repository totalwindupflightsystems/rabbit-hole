// Package attach — regression coverage for DF-033.
//
// DF-033: flows were double-stored in the attach pipeline. The
// classification engine persisted the whole batch internally
// (engine.Classify), and then attach.Pipeline.processSession persisted
// EACH flow again with the same ID. flows.id is a TEXT PRIMARY KEY, so
// the second insert failed with a UNIQUE/constraint error — a WARN on
// every batch in daemon logs. The fix: the engine is pure
// classification and processSession is the SINGLE persistence point.
package attach

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/classify"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// logCapture is a mutex-guarded byte buffer shared by every handler in
// the recording chain (WithAttrs/WithGroup return new handlers that
// still write into the same capture).
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// lockedWriter routes every Write from the text handler into the
// shared capture under its mutex — the pipeline logs from its own
// goroutines while the test reads the capture, so the buffer must be
// guarded on both ends.
type lockedWriter struct{ c *logCapture }

func (w lockedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	return w.c.buf.Write(p)
}

// recordingHandler implements slog.Handler and records every record's
// rendered text into a shared logCapture for test assertions.
type recordingHandler struct {
	capture *logCapture
	inner   slog.Handler
}

func newRecordingHandler() *recordingHandler {
	c := &logCapture{}
	return &recordingHandler{capture: c, inner: slog.NewTextHandler(lockedWriter{c}, nil)}
}

func (h *recordingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *recordingHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{capture: h.capture, inner: h.inner.WithAttrs(attrs)}
}

func (h *recordingHandler) WithGroup(name string) slog.Handler {
	return &recordingHandler{capture: h.capture, inner: h.inner.WithGroup(name)}
}

// String returns the captured log text.
func (h *recordingHandler) String() string {
	h.capture.mu.Lock()
	defer h.capture.mu.Unlock()
	return h.capture.buf.String()
}

// TestPipeline_FlowsStoredExactlyOnce is the DF-033 regression core: a
// classified flow must reach storage exactly once. Before the fix the
// engine persisted the whole batch (engine.go) and then processSession
// re-persisted each flow (attach.go), so the second pass failed with a
// UNIQUE/constraint error on flows.id — a WARN per flow on every batch.
// This test drives N pattern-classified flows through the full pipeline
// and asserts exactly N rows in the store with ZERO constraint-failure
// or store-failure log lines. Before the fix the log assertions fail
// with N constraint WARNs.
func TestPipeline_FlowsStoredExactlyOnce(t *testing.T) {
	const flowCount = 4

	store, err := storage.NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	capture := newRecordingHandler()
	logger := slog.New(capture)

	coll := newCountingMockCollector()
	engine := classify.NewClassificationEngine(nil, store, nil) // pattern-only
	cls := classify.NewClassifier(engine)

	p := NewPipeline(coll, cls, store, logger)
	p.pollInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("pipeline did not stop within 3s of cancel")
		}
	})

	sess, err := coll.Attach(ctx, 4242, collector.CollectOptions{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// flows.session_id is a FK to sessions(id) and the migration turns
	// PRAGMA foreign_keys=ON — the session row must exist before the
	// pipeline can persist flows for it.
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Wait for the pipeline to pick up the session and open its stream.
	if !waitForCond(t, 5*time.Second, func() bool {
		return coll.streamCallsFor(sess.ID) >= 1
	}) {
		t.Fatal("pipeline never opened a stream")
	}

	// Feed flowCount file-read groups (openat → read ×3 → close, the
	// same shape as the e2e/still-flows tests). Groups are separated by
	// >100ms so groupTracesByTime splits them into flowCount distinct
	// flows, all matched by the file_read pattern (confidence 0.95).
	base := time.Now()
	ch := coll.TraceIn(sess.ID)
	for g := 0; g < flowCount; g++ {
		off := time.Duration(g) * 200 * time.Millisecond
		traces := []types.Trace{
			{ID: fmt.Sprintf("t-%d-0", g), PID: sess.AgentPID, Timestamp: base.Add(off), Category: types.TraceCategoryFile, Syscall: "openat", Args: []string{"AT_FDCWD", fmt.Sprintf("/tmp/x%d", g), "O_RDONLY"}, ReturnValue: 7, Duration: 200 * time.Microsecond},
			{ID: fmt.Sprintf("t-%d-1", g), PID: sess.AgentPID, Timestamp: base.Add(off + 10*time.Millisecond), Category: types.TraceCategoryFile, Syscall: "read", Args: []string{"7", "4096"}, ReturnValue: 4096, Duration: 50 * time.Microsecond},
			{ID: fmt.Sprintf("t-%d-2", g), PID: sess.AgentPID, Timestamp: base.Add(off + 20*time.Millisecond), Category: types.TraceCategoryFile, Syscall: "read", Args: []string{"7", "4096"}, ReturnValue: 1024, Duration: 50 * time.Microsecond},
			{ID: fmt.Sprintf("t-%d-3", g), PID: sess.AgentPID, Timestamp: base.Add(off + 30*time.Millisecond), Category: types.TraceCategoryFile, Syscall: "read", Args: []string{"7", "4096"}, ReturnValue: 0, Duration: 30 * time.Microsecond},
			{ID: fmt.Sprintf("t-%d-4", g), PID: sess.AgentPID, Timestamp: base.Add(off + 40*time.Millisecond), Category: types.TraceCategoryFile, Syscall: "close", Args: []string{"7"}, ReturnValue: 0, Duration: 10 * time.Microsecond},
		}
		for _, tr := range traces {
			ch <- tr
		}
	}

	// Wait for all flowCount flows to reach storage (processSession is
	// the persistence point; the engine flushes batches on its 500ms
	// timeout).
	ok := waitForCond(t, 10*time.Second, func() bool {
		flows, err := store.SearchFlows(context.Background(), "", 100)
		return err == nil && len(flows) >= flowCount
	})
	if !ok {
		t.Fatalf("fewer than %d flows stored within 10s — pipeline no longer persists flows", flowCount)
	}

	// Exactly-once: no duplicate rows (flows.id is the PK, so any
	// re-insert of the same flow either fails or would double the
	// count).
	flows, err := store.SearchFlows(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("SearchFlows: %v", err)
	}
	if len(flows) != flowCount {
		t.Fatalf("stored flows = %d, want exactly %d (duplicates?)", len(flows), flowCount)
	}

	// Read the captured log only after all writes are done — check for
	// constraint/store failures. Before DF-033 the engine stored N and
	// processSession re-stored N, so the log carries N constraint WARNs
	// (modernc.org/sqlite reports "constraint failed", the bug report
	// quoted "UNIQUE constraint failed: flows.id" — match both).
	logText := capture.String()
	for _, forbidden := range []string{"UNIQUE constraint", "constraint failed", "failed to store flow"} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("log contains %q after classification — flows double-stored (DF-033); log:\n%s", forbidden, logText)
		}
	}
}
