// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package workers

import "sync"

// historyCapacity is the maximum number of events the ring buffer
// keeps per worker. Picked to comfortably cover the typical "I open
// the spy a minute late" case: a chatty worker emits ~1 event per
// LLM call, so 200 is several turns of context.
const historyCapacity = 200

// eventHistory is a bounded ring buffer of WorkerEvents protected by
// a mutex. New events overwrite the oldest when full. Used by the
// Manager to give late subscribers (e.g. the TUI opening a spy view
// after the worker has been running for a while) immediate context.
//
// The buffer is intentionally per-worker rather than global: each
// worker's stream is its own conversation, mixing them in a single
// buffer would defeat the point of the per-worker bus.
type eventHistory struct {
	mu  sync.Mutex
	buf []WorkerEvent
	// next is the index where the next Append will write; wraps
	// modulo capacity when the buffer is full.
	next int
	full bool
}

// newEventHistory returns an empty buffer with the package-level
// capacity. Capacity is not configurable today; if a particular
// deployment needs a different size we will plumb it through
// ManagerConfig at that point.
func newEventHistory() *eventHistory {
	return &eventHistory{
		buf: make([]WorkerEvent, historyCapacity),
	}
}

// Append records evt, dropping the oldest entry when the buffer is
// already full. Safe for concurrent use.
func (h *eventHistory) Append(evt WorkerEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf[h.next] = evt
	h.next = (h.next + 1) % historyCapacity
	if h.next == 0 {
		h.full = true
	}
}

// Snapshot returns a fresh slice containing every event currently in
// the buffer, oldest first. Callers can keep / mutate the slice
// without affecting the buffer.
func (h *eventHistory) Snapshot() []WorkerEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.full {
		out := make([]WorkerEvent, h.next)
		copy(out, h.buf[:h.next])
		return out
	}
	out := make([]WorkerEvent, historyCapacity)
	// next points at the oldest entry when the buffer is full
	// (because we wrapped past it on the previous Append).
	copy(out, h.buf[h.next:])
	copy(out[historyCapacity-h.next:], h.buf[:h.next])
	return out
}

// Len reports how many events are currently stored, capped at
// historyCapacity. Useful for diagnostics and tests.
func (h *eventHistory) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.full {
		return historyCapacity
	}
	return h.next
}
