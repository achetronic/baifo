// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import "testing"

// TestStreamingDeltasThenAggregateNoDuplication reproduces the real
// adka2a streaming shape that produced the "[tail of reply] + [whole
// reply]" duplication: a run streams incremental text deltas (append)
// and then emits one final aggregated chunk carrying the COMPLETE turn
// text with replace=true. The single root row must end up holding the
// full text exactly once — not the last delta with the whole reply
// concatenated onto it.
func TestStreamingDeltasThenAggregateNoDuplication(t *testing.T) {
	m := NewModel(&fakeFacade{}, "v0")
	m.splash = false

	// Seed a user row so the root reply lands at index 1, mirroring a
	// real turn.
	cur, _ := m.handleAgentChunk(agentChunkMsg{text: "Hello", replace: false})
	mm := cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{text: ", world", replace: false})
	mm = cur.(Model)
	cur, _ = mm.handleAgentChunk(agentChunkMsg{text: "!", replace: false})
	mm = cur.(Model)

	// At this point the streamed deltas have accumulated.
	if got := mm.messages[len(mm.messages)-1].Text; got != "Hello, world!" {
		t.Fatalf("after deltas: got %q, want %q", got, "Hello, world!")
	}

	// The executor now emits the final aggregated artifact with the
	// COMPLETE text and replace=true.
	cur, _ = mm.handleAgentChunk(agentChunkMsg{text: "Hello, world!", replace: true})
	mm = cur.(Model)

	last := mm.messages[len(mm.messages)-1]
	if last.Kind != MessageRoot {
		t.Fatalf("last row kind = %v, want MessageRoot", last.Kind)
	}
	if last.Text != "Hello, world!" {
		t.Fatalf("after final aggregate: got %q, want %q (duplication regression)", last.Text, "Hello, world!")
	}
	// Exactly one root row should exist for this turn.
	roots := 0
	for _, msg := range mm.messages {
		if msg.Kind == MessageRoot {
			roots++
		}
	}
	if roots != 1 {
		t.Fatalf("expected exactly 1 root row, got %d", roots)
	}
}
