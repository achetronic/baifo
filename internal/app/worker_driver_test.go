// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/workers"
)

// drainBus subscribes to bus, runs fn (which publishes synchronously),
// then unsubscribes (closing the channel) and returns everything that
// was published. publishWorkerStreamEvent is synchronous, so by the time
// fn returns every event is already buffered on the subscriber channel.
func drainBus(bus *workers.EventBus, fn func()) []workers.WorkerEvent {
	ch, unsub := bus.Subscribe()
	fn()
	unsub() // closes ch
	var out []workers.WorkerEvent
	for evt := range ch {
		out = append(out, evt)
	}
	return out
}

func countKind(events []workers.WorkerEvent, kind workers.EventKind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// Regression guard for the worker tool-rendering bug: the adk-utils
// anthropic/openai adapters emit tool calls ONLY in the final aggregate
// (Replace=true) event. The driver used to skip that event entirely, so
// the tool cards never reached the bus. They must now be published.
func TestPublishWorkerStreamEvent_ToolCallsFromReplaceAggregate(t *testing.T) {
	bus := workers.NewEventBus()
	calls := map[string]bool{}
	results := map[string]bool{}
	var sb strings.Builder

	events := drainBus(bus, func() {
		publishWorkerStreamEvent(bus, "w1", &facade.Event{
			Replace:     true, // anthropic/openai carry tool calls here
			ToolCalls:   []facade.ToolCallInfo{{CallID: "c1", Name: "ls", Args: map[string]any{"path": "/"}}},
			ToolResults: []facade.ToolResultInfo{{CallID: "c1", Name: "ls", Result: map[string]any{"ok": true}}},
		}, calls, results, &sb)
	})

	if got := countKind(events, workers.EventToolCall); got != 1 {
		t.Errorf("EventToolCall count = %d, want 1 (Replace aggregate must not be dropped)", got)
	}
	if got := countKind(events, workers.EventToolResult); got != 1 {
		t.Errorf("EventToolResult count = %d, want 1", got)
	}
}

// Regression guard for the dedupe path: gemini emits tool calls
// incrementally AND repeats them in the final aggregate. The same call ID
// must produce exactly one card, not two.
func TestPublishWorkerStreamEvent_DedupesRepeatedToolCalls(t *testing.T) {
	bus := workers.NewEventBus()
	calls := map[string]bool{}
	results := map[string]bool{}
	var sb strings.Builder

	events := drainBus(bus, func() {
		// Incremental event (Replace=false) carrying the call.
		publishWorkerStreamEvent(bus, "w1", &facade.Event{
			ToolCalls: []facade.ToolCallInfo{{CallID: "c1", Name: "ls"}},
		}, calls, results, &sb)
		// Final aggregate repeats the same call.
		publishWorkerStreamEvent(bus, "w1", &facade.Event{
			Replace:   true,
			ToolCalls: []facade.ToolCallInfo{{CallID: "c1", Name: "ls"}},
		}, calls, results, &sb)
	})

	if got := countKind(events, workers.EventToolCall); got != 1 {
		t.Errorf("EventToolCall count = %d, want 1 (repeated call ID must be deduped)", got)
	}
}

// Plain assistant text is streamed incrementally; the final aggregate
// repeats the full text. Only the incremental text must be published, or
// the reply shows up twice.
func TestPublishWorkerStreamEvent_TextSkippedOnReplace(t *testing.T) {
	bus := workers.NewEventBus()
	calls := map[string]bool{}
	results := map[string]bool{}
	var sb strings.Builder

	events := drainBus(bus, func() {
		publishWorkerStreamEvent(bus, "w1", &facade.Event{Text: "hola"}, calls, results, &sb)
		publishWorkerStreamEvent(bus, "w1", &facade.Event{Replace: true, Text: "hola"}, calls, results, &sb)
	})

	if got := countKind(events, workers.EventAssistantMessage); got != 1 {
		t.Errorf("EventAssistantMessage count = %d, want 1 (Replace text must be skipped)", got)
	}
	if sb.String() != "hola" {
		t.Errorf("accumulated reply = %q, want %q", sb.String(), "hola")
	}
}

// Executor errors (Role == "error") are never part of an aggregate and
// must always surface, even though they carry text.
func TestPublishWorkerStreamEvent_ErrorAlwaysSurfaced(t *testing.T) {
	bus := workers.NewEventBus()
	calls := map[string]bool{}
	results := map[string]bool{}
	var sb strings.Builder

	events := drainBus(bus, func() {
		publishWorkerStreamEvent(bus, "w1", &facade.Event{Role: "error", Text: "boom"}, calls, results, &sb)
	})

	if got := countKind(events, workers.EventToolResult); got != 1 {
		t.Errorf("error event count = %d, want 1", got)
	}
}
