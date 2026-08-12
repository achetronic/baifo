// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Reasoning summaries (Gemini thinking et al.) arrive as text parts
// flagged Thought=true — on the A2A stream via the adk_thought part
// metadata, and in persisted session events via genai.Part.Thought.
// They must never be concatenated into the visible reply text: the
// final aggregated artifact repeats them, which used to render as a
// garbled "reasoning + answer" blob in the chat.

const thoughtMetaKey = "adk_thought"

// TestEventFromA2AParts_ThoughtPartsSkipped verifies a streamed chunk
// whose parts mix a thought part and a reply part surfaces only the
// reply text.
func TestEventFromA2AParts_ThoughtPartsSkipped(t *testing.T) {
	ev := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		Metadata:  map[string]any{partialMetaKey: true},
		Artifact: &a2a.Artifact{
			ID: "a1",
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Let me reason about this...", Metadata: map[string]any{thoughtMetaKey: true}},
				a2a.TextPart{Text: "The answer is 4."},
			},
		},
	}
	out := eventFromA2A(ev)
	if out == nil {
		t.Fatal("event with reply text was dropped")
	}
	if out.Text != "The answer is 4." {
		t.Fatalf("thought text leaked into reply: %q", out.Text)
	}
}

// TestEventFromA2AParts_OnlyThoughtsDropped verifies a chunk carrying
// nothing but reasoning produces no chat event at all (instead of an
// empty or reasoning-only bubble).
func TestEventFromA2AParts_OnlyThoughtsDropped(t *testing.T) {
	ev := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		Metadata:  map[string]any{partialMetaKey: true},
		Artifact: &a2a.Artifact{
			ID: "a1",
			Parts: []a2a.Part{
				a2a.TextPart{Text: "thinking...", Metadata: map[string]any{thoughtMetaKey: true}},
			},
		},
	}
	if out := eventFromA2A(ev); out != nil {
		t.Fatalf("thought-only event should be dropped, got %+v", out)
	}
}

// TestEventFromSessionEvent_ThoughtPartsSkipped pins the same rule on
// the session-replay path so a resumed conversation shows the same
// transcript the live stream produced.
func TestEventFromSessionEvent_ThoughtPartsSkipped(t *testing.T) {
	ev := &session.Event{Author: "root"}
	ev.Content = &genai.Content{
		Role: "model",
		Parts: []*genai.Part{
			{Text: "internal reasoning", Thought: true},
			{Text: "visible answer"},
		},
	}
	out := eventFromSessionEvent(ev)
	if out == nil {
		t.Fatal("event with visible text was dropped")
	}
	if out.Text != "visible answer" {
		t.Fatalf("thought text leaked into replayed transcript: %q", out.Text)
	}

	onlyThought := &session.Event{Author: "root"}
	onlyThought.Content = &genai.Content{
		Role:  "model",
		Parts: []*genai.Part{{Text: "only reasoning", Thought: true}},
	}
	if out := eventFromSessionEvent(onlyThought); out != nil {
		t.Fatalf("thought-only session event should be dropped, got %+v", out)
	}
}
