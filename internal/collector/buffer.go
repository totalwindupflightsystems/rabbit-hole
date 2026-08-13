// Package collector implements the eBPF-based telemetry collection layer
// for Rabbit-Hole.
package collector

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// BufferStats holds ring buffer performance counters.
type BufferStats struct {
	Size    int    // capacity in traces
	Used    int    // current count
	Dropped uint64 // total dropped since creation
}

// RingBuffer is a lock-based circular buffer of Trace events.
type RingBuffer struct {
	buf     []types.Trace
	size    int
	head    int // next write position
	tail    int // next read position
	count   int // current number of elements
	mu      sync.Mutex
	dropped atomic.Uint64
	cond    *sync.Cond
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	rb := &RingBuffer{
		buf:  make([]types.Trace, size),
		size: size,
	}
	rb.cond = sync.NewCond(&rb.mu)
	return rb
}

// Push adds a trace. If full, the oldest trace is silently dropped.
func (rb *RingBuffer) Push(t types.Trace) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.count == rb.size {
		rb.tail = (rb.tail + 1) % rb.size
		rb.dropped.Add(1)
	} else {
		rb.count++
	}

	rb.buf[rb.head] = t
	rb.head = (rb.head + 1) % rb.size
	rb.cond.Signal()
}

// Pop removes and returns the oldest trace. Blocks until data is available
// or the context is cancelled.
func (rb *RingBuffer) Pop(ctx context.Context) (types.Trace, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for rb.count == 0 {
		select {
		case <-ctx.Done():
			return types.Trace{}, ctx.Err()
		default:
		}
		rb.cond.Wait()
	}

	t := rb.buf[rb.tail]
	rb.tail = (rb.tail + 1) % rb.size
	rb.count--
	return t, nil
}

// PopBatch returns up to max traces without blocking.
func (rb *RingBuffer) PopBatch(max int) []types.Trace {
	if max <= 0 {
		return nil
	}
	rb.mu.Lock()
	defer rb.mu.Unlock()

	n := max
	if n > rb.count {
		n = rb.count
	}
	if n == 0 {
		return nil
	}

	batch := make([]types.Trace, n)
	for i := 0; i < n; i++ {
		batch[i] = rb.buf[rb.tail]
		rb.tail = (rb.tail + 1) % rb.size
	}
	rb.count -= n
	return batch
}

// PopAll returns all currently buffered traces without blocking.
func (rb *RingBuffer) PopAll() []types.Trace {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.popLocked(rb.count)
}

func (rb *RingBuffer) popLocked(n int) []types.Trace {
	if n <= 0 || rb.count == 0 {
		return nil
	}
	if n > rb.count {
		n = rb.count
	}
	batch := make([]types.Trace, n)
	for i := 0; i < n; i++ {
		batch[i] = rb.buf[rb.tail]
		rb.tail = (rb.tail + 1) % rb.size
	}
	rb.count -= n
	return batch
}

// Stats returns current buffer statistics.
func (rb *RingBuffer) Stats() BufferStats {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return BufferStats{
		Size:    rb.size,
		Used:    rb.count,
		Dropped: rb.dropped.Load(),
	}
}

// String returns a human-readable summary.
func (rb *RingBuffer) String() string {
	s := rb.Stats()
	return fmt.Sprintf("RingBuffer{size=%d, used=%d/%d, dropped=%d}",
		s.Size, s.Used, s.Size, s.Dropped)
}
