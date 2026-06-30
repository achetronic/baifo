// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package workers

import (
	"sync"
	"sync/atomic"
	"time"
)

// EventKind classifies a WorkerEvent. Mirrors WORKER_RUNTIME.md.
type EventKind int

const (
	// EventToolCall is emitted when the worker invokes a tool.
	EventToolCall EventKind = iota

	// EventToolResult carries the result of a tool call back to the
	// worker (already redacted).
	EventToolResult

	// EventThought is a model "thinking" step, when the provider
	// exposes one.
	EventThought

	// EventAssistantMessage is a chunk of assistant text.
	EventAssistantMessage

	// EventStatusChange signals a Status transition. Useful for the
	// TUI to repaint without polling.
	EventStatusChange
)

// String renders an EventKind as a short token used by the audit log
// and the inspect_agent tool output.
func (k EventKind) String() string {
	switch k {
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventThought:
		return "thought"
	case EventAssistantMessage:
		return "assistant_message"
	case EventStatusChange:
		return "status_change"
	}
	return "unknown"
}

// WorkerEvent is the unit pushed onto the event bus. Index is a
// monotonic per-worker counter; subscribers reading from the global
// channel use (WorkerID, Index) to detect gaps.
type WorkerEvent struct {
	WorkerID  string
	Index     int
	Timestamp time.Time
	Kind      EventKind
	Payload   any
}

// ToolCallPayload is the Payload shape published with
// EventToolCall events. It carries the full call envelope so
// subscribers can render the tool boundary with as much context as
// the agent itself sees (name, ID for pairing with the response,
// and the post-expansion args map after the BeforeToolCallback ran).
//
// Subscribers should treat Args as read-only; the same map is
// shared with ADK's tool-execution path.
type ToolCallPayload struct {
	Name string
	ID   string
	Args map[string]any
}

// ToolResultPayload is the Payload shape published with
// EventToolResult events. ID pairs with the ToolCallPayload.ID of
// the matching call so the TUI can collapse the pair into a single
// card. Result is the post-redaction response map.
type ToolResultPayload struct {
	Name   string
	ID     string
	Result map[string]any
}

// busBufferSize is the per-subscriber channel size mandated by the
// design doc.
const busBufferSize = 256

// EventBus is a fan-out channel for worker events. Each subscriber
// gets its own buffered channel; slow subscribers see Drops counted
// but do not block the publisher.
//
// The implementation is intentionally simple: a slice of subscribers
// guarded by a RWMutex, and Publish drops the event for any
// subscriber whose buffer is full.
type EventBus struct {
	mu    sync.RWMutex
	subs  []chan WorkerEvent
	idx   int
	drops uint64
}

// NewEventBus returns a ready-to-use bus.
func NewEventBus() *EventBus {
	return &EventBus{}
}

// Subscribe registers a new subscriber and returns the channel plus
// an unsubscribe function. Callers must drain the channel until the
// unsubscribe is called.
func (b *EventBus) Subscribe() (<-chan WorkerEvent, func()) {
	ch := make(chan WorkerEvent, busBufferSize)

	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, c := range b.subs {
			if c == ch {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(c)
				return
			}
		}
	}
}

// Publish fans out evt to every subscriber. Index is assigned by the
// bus so callers do not have to coordinate. Subscribers whose buffer
// is full lose the event (counted via Drops).
func (b *EventBus) Publish(evt WorkerEvent) {
	b.mu.Lock()
	b.idx++
	evt.Index = b.idx
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	subs := append([]chan WorkerEvent{}, b.subs...)
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- evt:
		default:
			atomic.AddUint64(&b.drops, 1)
		}
	}
}

// Drops reports how many events were dropped because of full
// subscriber buffers. Mostly for diagnostics / status bar.
func (b *EventBus) Drops() uint64 {
	return atomic.LoadUint64(&b.drops)
}

// Close closes every subscriber channel. After Close, Publish becomes
// a no-op. Used by the Manager on shutdown.
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}
