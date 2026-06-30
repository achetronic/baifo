// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"github.com/achetronic/baifo/internal/facade"
)

// These tests pin the StreamID-based coalescing: the in-flight reply
// bubble is found by identity, never by array position, so anything
// inserted after it (tool rows, lifecycle notices, errors) cannot
// split the reply into duplicate bubbles. Each case reproduces a real
// regression we hit before the refactor.

func rootRows(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if m.Kind == MessageRoot {
			out = append(out, m)
		}
	}
	return out
}

// TestStream_LifecycleNoticeMidStreamNoSplit reproduces the exact bug
// the user saw: a "worker spawned" lifecycle row arriving while the
// root is streaming must NOT split the reply. After the notice, more
// text plus the final replace artifact must land in the SAME bubble.
func TestStream_LifecycleNoticeMidStreamNoSplit(t *testing.T) {
	m := NewModel(&fakeFacade{}, false, "v0")
	m.splash = false

	// First streamed chunk opens the reply bubble.
	cur, _ := m.handleAgentChunk(agentChunkMsg{text: "Launching a worker"})
	mm := cur.(Model)

	// A lifecycle notice lands mid-stream (this is what split the
	// bubble before the StreamID refactor).
	cur, _ = mm.Update(workerLifecycleMsg{event: facade.WorkerLifecycleEvent{
		Kind:     facade.WorkerLifecycleSpawned,
		WorkerID: "w_1",
		Name:     "tester",
	}})
	mm = cur.(Model)

	// More text, then the final aggregated artifact (replace=true).
	cur, _ = mm.handleAgentChunk(agentChunkMsg{text: " to test the render."})
	mm = cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{
		text:    "Launching a worker to test the render.",
		replace: true,
	})
	mm = cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{done: true})
	mm = cur.(Model)

	roots := rootRows(mm.messages)
	if len(roots) != 1 {
		t.Fatalf("expected exactly 1 root bubble, got %d: %+v", len(roots), mm.messages)
	}
	if roots[0].Text != "Launching a worker to test the render." {
		t.Fatalf("bubble text = %q, want the full reply once", roots[0].Text)
	}
}

// TestStream_ToolCallMidStreamSplitsBubble verifies that a tool call
// DOES start a fresh bubble for the text after it (tool calls are a
// real semantic break in the reply), while the text before keeps its
// own bubble intact.
func TestStream_ToolCallMidStreamSplitsBubble(t *testing.T) {
	m := NewModel(&fakeFacade{}, false, "v0")
	m.splash = false

	cur, _ := m.handleAgentChunk(agentChunkMsg{text: "Before tool"})
	mm := cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{
		toolCalls: []facade.ToolCallInfo{{Name: "read_file", CallID: "c1"}},
	})
	mm = cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{text: "After tool"})
	mm = cur.(Model)

	roots := rootRows(mm.messages)
	if len(roots) != 2 {
		t.Fatalf("expected 2 root bubbles (split by tool call), got %d", len(roots))
	}
	if roots[0].Text != "Before tool" || roots[1].Text != "After tool" {
		t.Fatalf("bubbles = %q / %q, want Before tool / After tool", roots[0].Text, roots[1].Text)
	}
	// The two bubbles must have different StreamIDs.
	if roots[0].StreamID == roots[1].StreamID {
		t.Fatalf("both bubbles share StreamID %q; the tool break should mint a new id", roots[0].StreamID)
	}
}

// TestStream_SeparateTurnsSeparateBubbles ensures two distinct turns
// get distinct StreamIDs so a second reply never coalesces into the
// first.
func TestStream_SeparateTurnsSeparateBubbles(t *testing.T) {
	m := NewModel(&fakeFacade{}, false, "v0")
	m.splash = false

	cur, _ := m.handleAgentChunk(agentChunkMsg{text: "Turn one"})
	mm := cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{done: true})
	mm = cur.(Model)

	cur, _ = mm.handleAgentChunk(agentChunkMsg{text: "Turn two"})
	mm = cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{done: true})
	mm = cur.(Model)

	roots := rootRows(mm.messages)
	if len(roots) != 2 {
		t.Fatalf("expected 2 root bubbles, got %d", len(roots))
	}
	if roots[0].Text != "Turn one" || roots[1].Text != "Turn two" {
		t.Fatalf("bubbles = %q / %q", roots[0].Text, roots[1].Text)
	}
	if roots[0].StreamID == roots[1].StreamID {
		t.Fatalf("separate turns must not share StreamID, both = %q", roots[0].StreamID)
	}
}
