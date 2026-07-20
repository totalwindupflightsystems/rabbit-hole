package collector

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// ---- Ring Buffer Tests ----

func TestRingBuffer_PushPop(t *testing.T) {
	rb := NewRingBuffer(10)
	ctx := context.Background()

	t1 := types.Trace{ID: "1", PID: 100}
	t2 := types.Trace{ID: "2", PID: 100}
	t3 := types.Trace{ID: "3", PID: 100}

	rb.Push(t1)
	rb.Push(t2)
	rb.Push(t3)

	stats := rb.Stats()
	if stats.Used != 3 {
		t.Errorf("expected 3 used, got %d", stats.Used)
	}

	got1, _ := rb.Pop(ctx)
	if got1.ID != "1" {
		t.Errorf("expected ID 1, got %s", got1.ID)
	}

	got2, _ := rb.Pop(ctx)
	if got2.ID != "2" {
		t.Errorf("expected ID 2, got %s", got2.ID)
	}

	got3, _ := rb.Pop(ctx)
	if got3.ID != "3" {
		t.Errorf("expected ID 3, got %s", got3.ID)
	}

	if rb.Stats().Used != 0 {
		t.Errorf("expected empty buffer, got %d used", rb.Stats().Used)
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Push(types.Trace{ID: "1"})
	rb.Push(types.Trace{ID: "2"})
	rb.Push(types.Trace{ID: "3"})

	stats := rb.Stats()
	if stats.Used != 3 {
		t.Errorf("expected 3 used, got %d", stats.Used)
	}
	if stats.Dropped != 0 {
		t.Errorf("expected 0 dropped, got %d", stats.Dropped)
	}

	rb.Push(types.Trace{ID: "4"})

	stats = rb.Stats()
	if stats.Used != 3 {
		t.Errorf("expected 3 used after overflow, got %d", stats.Used)
	}
	if stats.Dropped != 1 {
		t.Errorf("expected 1 dropped, got %d", stats.Dropped)
	}

	ctx := context.Background()
	got, _ := rb.Pop(ctx)
	if got.ID != "2" {
		t.Errorf("expected ID 2 after overflow, got %s", got.ID)
	}
}

func TestRingBuffer_PopBatch(t *testing.T) {
	rb := NewRingBuffer(10)

	for i := 0; i < 5; i++ {
		rb.Push(types.Trace{ID: string(rune('A' + i))})
	}

	batch := rb.PopBatch(3)
	if len(batch) != 3 {
		t.Errorf("expected 3 in batch, got %d", len(batch))
	}
	if batch[0].ID != "A" {
		t.Errorf("expected A, got %s", batch[0].ID)
	}
	if batch[2].ID != "C" {
		t.Errorf("expected C, got %s", batch[2].ID)
	}

	if rb.Stats().Used != 2 {
		t.Errorf("expected 2 remaining, got %d", rb.Stats().Used)
	}

	batch = rb.PopBatch(10)
	if len(batch) != 2 {
		t.Errorf("expected 2 in overflow batch, got %d", len(batch))
	}
}

func TestRingBuffer_PopAll(t *testing.T) {
	rb := NewRingBuffer(10)

	for i := 0; i < 5; i++ {
		rb.Push(types.Trace{ID: string(rune('0' + i))})
	}

	batch := rb.PopAll()
	if len(batch) != 5 {
		t.Errorf("expected 5, got %d", len(batch))
	}
	if rb.Stats().Used != 0 {
		t.Errorf("expected empty, got %d", rb.Stats().Used)
	}
}

func TestRingBuffer_EmptyPop(t *testing.T) {
	rb := NewRingBuffer(10)

	batch := rb.PopBatch(5)
	if batch != nil {
		t.Errorf("expected nil from empty buffer, got %v", batch)
	}

	all := rb.PopAll()
	if all != nil {
		t.Errorf("expected nil from empty buffer PopAll, got %v", all)
	}
}

func TestRingBuffer_PopContextCancel(t *testing.T) {
	rb := NewRingBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := rb.Pop(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRingBuffer_Stats(t *testing.T) {
	rb := NewRingBuffer(100)

	stats := rb.Stats()
	if stats.Size != 100 {
		t.Errorf("expected size 100, got %d", stats.Size)
	}
	if stats.Used != 0 {
		t.Errorf("expected 0 used, got %d", stats.Used)
	}
	if stats.Dropped != 0 {
		t.Errorf("expected 0 dropped, got %d", stats.Dropped)
	}

	for i := 0; i < 50; i++ {
		rb.Push(types.Trace{})
	}

	stats = rb.Stats()
	if stats.Used != 50 {
		t.Errorf("expected 50 used, got %d", stats.Used)
	}
}

func TestRingBuffer_StressOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const size = 100000
	const total = 200000

	rb := NewRingBuffer(size)
	for i := 0; i < total; i++ {
		rb.Push(types.Trace{ID: fmt.Sprintf("#%d", i)})
	}

	stats := rb.Stats()
	if stats.Used != size {
		t.Errorf("expected Used=%d, got %d", size, stats.Used)
	}
	if stats.Dropped != uint64(total-size) {
		t.Errorf("expected Dropped=%d, got %d", total-size, stats.Dropped)
	}

	batch := rb.PopAll()
	if len(batch) != size {
		t.Fatalf("expected PopAll=%d, got %d", size, len(batch))
	}
	if batch[0].ID != "#100000" {
		t.Errorf("expected oldest trace #100000, got %s", batch[0].ID)
	}
	if batch[size-1].ID != "#199999" {
		t.Errorf("expected newest trace #199999, got %s", batch[size-1].ID)
	}
}

func TestRingBuffer_ConcurrentPush(t *testing.T) {
	rb := NewRingBuffer(10000)
	var wg sync.WaitGroup

	n := 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rb.Push(types.Trace{ID: string(rune('A' + id%26))})
		}(i)
	}
	wg.Wait()

	stats := rb.Stats()
	if stats.Used != n {
		t.Errorf("expected %d after concurrent push, got %d", n, stats.Used)
	}

	batch := rb.PopAll()
	if len(batch) != n {
		t.Errorf("expected %d in PopAll, got %d", n, len(batch))
	}
}

// ---- Collector Tests ----

func newTestCollector(t *testing.T) *eBPFCollector {
	t.Helper()
	c, err := NewEBPFCollector(1000, 10, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestCollector_NewEBPFCollector(t *testing.T) {
	c := newTestCollector(t)

	if c.maxSessions != 10 {
		t.Errorf("expected maxSessions 10, got %d", c.maxSessions)
	}
	if c.ringBuffer == nil {
		t.Error("ring buffer is nil")
	}
	if c.sessions == nil {
		t.Error("sessions map is nil")
	}
}

func TestCollector_Defaults(t *testing.T) {
	c, err := NewEBPFCollector(0, 0, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	defer c.Close()

	if c.maxSessions != 50 {
		t.Errorf("expected default maxSessions 50, got %d", c.maxSessions)
	}

	bs := c.BufferStats()
	if bs.Size != 100000 {
		t.Errorf("expected default buffer size 100000, got %d", bs.Size)
	}
}

func TestCollector_AttachDetach(t *testing.T) {
	c := newTestCollector(t)
	pid := int32(os.Getpid())

	session, err := c.Attach(context.Background(), pid, DefaultCollectOptions())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if session.ID == "" {
		t.Error("session ID is empty")
	}
	if session.AgentPID != pid {
		t.Errorf("expected pid %d, got %d", pid, session.AgentPID)
	}
	if session.Status != types.SessionStatusRunning {
		t.Errorf("expected running status, got %s", session.Status)
	}

	sessions, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}

	// Double attach should be rejected
	_, err = c.Attach(context.Background(), pid, DefaultCollectOptions())
	if err == nil {
		t.Error("expected ErrAlreadyAttached, got nil")
	}

	// Detach
	if err := c.Detach(context.Background(), session.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	sessions, err = c.List(context.Background())
	if err != nil {
		t.Fatalf("List after detach: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions after detach, got %d", len(sessions))
	}
}

func TestCollector_AttachNonexistent(t *testing.T) {
	c := newTestCollector(t)

	_, err := c.Attach(context.Background(), 999999, DefaultCollectOptions())
	if err == nil {
		t.Error("expected error for nonexistent PID")
	}
}

func TestCollector_DetachUnknown(t *testing.T) {
	c := newTestCollector(t)

	err := c.Detach(context.Background(), "nonexistent-session-id")
	if err == nil {
		t.Error("expected error for unknown session")
	}
}

func TestCollector_Stream(t *testing.T) {
	c := newTestCollector(t)
	pid := int32(os.Getpid())

	session, err := c.Attach(context.Background(), pid, DefaultCollectOptions())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch, err := c.Stream(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	c.ringBuffer.Push(types.Trace{ID: "test-1", PID: pid})

	select {
	case trace := <-ch:
		if trace.ID != "test-1" {
			t.Errorf("expected test-1, got %s", trace.ID)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for trace on stream")
	}
}

func TestCollector_StreamUnknownSession(t *testing.T) {
	c := newTestCollector(t)

	_, err := c.Stream(context.Background(), "no-such-session")
	if err == nil {
		t.Error("expected error for unknown session")
	}
}

func TestCollector_Health(t *testing.T) {
	c := newTestCollector(t)

	if err := c.Health(context.Background()); err != nil {
		t.Errorf("Health: %v", err)
	}
}

func TestCollector_MaxSessions(t *testing.T) {
	c, err := NewEBPFCollector(1000, 1, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	defer c.Close()

	pid := int32(os.Getpid())
	_, err = c.Attach(context.Background(), pid, DefaultCollectOptions())
	if err != nil {
		t.Fatalf("first Attach: %v", err)
	}

	_, err = c.Attach(context.Background(), int32(os.Getppid()), DefaultCollectOptions())
	if err == nil {
		t.Error("expected ErrMaxSessionsReached, got nil")
	}
}

// ---- proc helpers ----

func TestProcessExists(t *testing.T) {
	pid := int32(os.Getpid())
	if !processExists(pid) {
		t.Errorf("current process PID %d should exist", pid)
	}
	if processExists(999999) {
		t.Error("PID 999999 should not exist")
	}
}

func TestDetectAgentName(t *testing.T) {
	name := detectAgentName(int32(os.Getpid()))
	if name == "" || name == "unknown" {
		t.Errorf("expected non-empty name for self, got %q", name)
	}
}

// ---- Benchmarks ----

func BenchmarkRingBuffer_Push(b *testing.B) {
	rb := NewRingBuffer(100000)
	trace := types.Trace{ID: "bench", PID: 100, Syscall: "read"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rb.Push(trace)
	}
}

func BenchmarkRingBuffer_PopBatch(b *testing.B) {
	rb := NewRingBuffer(100000)
	trace := types.Trace{ID: "bench", PID: 100}
	for i := 0; i < 50000; i++ {
		rb.Push(trace)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch := rb.PopBatch(100)
		if len(batch) == 0 {
			b.Fatal("unexpected empty batch")
		}
		// Re-fill to avoid draining mid-benchmark
		for j := 0; j < len(batch); j++ {
			rb.Push(trace)
		}
	}
}

func BenchmarkRingBuffer_ConcurrentPush(b *testing.B) {
	rb := NewRingBuffer(100000)
	trace := types.Trace{ID: "bench", PID: 100}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rb.Push(trace)
			}()
		}
		wg.Wait()
	}
}

func BenchmarkRingBuffer_PopAll(b *testing.B) {
	rb := NewRingBuffer(100000)
	trace := types.Trace{ID: "bench", PID: 100}
	for i := 0; i < 50000; i++ {
		rb.Push(trace)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch := rb.PopAll()
		if len(batch) == 0 {
			b.Fatal("unexpected empty batch")
		}
		for j := 0; j < len(batch); j++ {
			rb.Push(trace)
		}
	}
}
