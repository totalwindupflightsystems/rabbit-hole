package main

import (
	"context"
	"fmt"
	"sync"
)

// demoStreamHealth reflects the last completed write, including recovery.
type demoStreamHealth struct {
	mu      sync.RWMutex
	written bool
	err     error
}

func (h *demoStreamHealth) Set(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.written = true
	h.err = err
}

func (h *demoStreamHealth) Check(context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if !h.written {
		return fmt.Errorf("waiting for first demo stream write")
	}
	return h.err
}
