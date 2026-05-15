// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
)

// The adka2a executor (OutputArtifactPerRun, baifo's mode) streams the
// assistant text as incremental deltas tagged adk_partial=true and then
// one final, fully-aggregated artifact tagged adk_partial=false. The
// Append flag is NOT a reliable "is this the full text" signal in this
// mode, so eventFromA2A keys off the adk_partial metadata instead. These
// tests pin that contract.

const partialMetaKey = "adk_partial"

// TestEventFromA2A_PartialDeltaAppends verifies a streaming delta
// (adk_partial=true) is surfaced as an append (Replace=false) so the TUI
// concatenates the deltas into one reply.
func TestEventFromA2A_PartialDeltaAppends(t *testing.T) {
	ev := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		// Append=false on a partial delta is exactly the case that
		// used to be misread as "replace" by the old !Append rule.
		Append:   false,
		Metadata: map[string]any{partialMetaKey: true},
		Artifact: &a2a.Artifact{
			ID:    "a1",
			Parts: []a2a.Part{a2a.TextPart{Text: "Hello"}},
		},
	}
	out := eventFromA2A(ev)
	if out == nil {
		t.Fatal("partial delta was dropped")
	}
	if out.Replace {
		t.Fatal("partial delta (adk_partial=true) must append, not replace")
	}
	if out.Text != "Hello" {
		t.Fatalf("unexpected text %q", out.Text)
	}
}

// TestEventFromA2A_FinalAggregateReplaces is the regression guard for the
// duplicated-reply bug ("[tail of reply] + [whole reply]"). The final,
// non-partial aggregated artifact (adk_partial=false) carries the COMPLETE
// turn text and must map to Replace=true so the TUI swaps the accumulated
// deltas for the authoritative full text instead of appending a copy.
// Append=true here mimics the real executor behaviour that broke the old
// !Append heuristic.
func TestEventFromA2A_FinalAggregateReplaces(t *testing.T) {
	ev := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		Append:    true,
		Metadata:  map[string]any{partialMetaKey: false},
		Artifact: &a2a.Artifact{
			ID:    "a1",
			Parts: []a2a.Part{a2a.TextPart{Text: "Hello, world!"}},
		},
	}
	out := eventFromA2A(ev)
	if out == nil {
		t.Fatal("final aggregate was dropped")
	}
	if !out.Replace {
		t.Fatal("final aggregate (adk_partial=false) must replace so the reply is not duplicated")
	}
	if out.Text != "Hello, world!" {
		t.Fatalf("unexpected text %q", out.Text)
	}
}

// TestEventFromA2A_PartialMetaOnArtifact confirms the flag is honoured
// when it rides on the Artifact metadata rather than the event metadata
// (the legacy maker stamps both, but we must not depend on which).
func TestEventFromA2A_PartialMetaOnArtifact(t *testing.T) {
	ev := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		Append:    true,
		Artifact: &a2a.Artifact{
			ID:       "a1",
			Metadata: map[string]any{partialMetaKey: false},
			Parts:    []a2a.Part{a2a.TextPart{Text: "full"}},
		},
	}
	out := eventFromA2A(ev)
	if out == nil {
		t.Fatal("event was dropped")
	}
	if !out.Replace {
		t.Fatal("artifact-level adk_partial=false must replace")
	}
}

// TestEventFromA2A_NoPartialMetaFallsBackToAppend verifies that when no
// adk_partial metadata is present (a non-adka2a / pure A2A peer), we fall
// back to the historical !Append heuristic so external interop is
// unchanged: Append=true → append, Append=false → replace.
func TestEventFromA2A_NoPartialMetaFallsBackToAppend(t *testing.T) {
	appendCase := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1", TaskID: "t1", Append: true,
		Artifact: &a2a.Artifact{ID: "a1", Parts: []a2a.Part{a2a.TextPart{Text: " mundo"}}},
	}
	if out := eventFromA2A(appendCase); out == nil || out.Replace {
		t.Fatalf("no-meta Append=true must append; got %+v", out)
	}

	replaceCase := &a2a.TaskArtifactUpdateEvent{
		ContextID: "s1", TaskID: "t1", Append: false, LastChunk: true,
		Artifact: &a2a.Artifact{ID: "a1", Parts: []a2a.Part{a2a.TextPart{Text: "hola mundo"}}},
	}
	if out := eventFromA2A(replaceCase); out == nil || !out.Replace {
		t.Fatalf("no-meta Append=false must replace; got %+v", out)
	}
}

// TestEventFromA2A_NilArtifactDropped confirms an artifact-update event
// with no artifact yields no chat event.
func TestEventFromA2A_NilArtifactDropped(t *testing.T) {
	ev := &a2a.TaskArtifactUpdateEvent{ContextID: "s1", TaskID: "t1"}
	if out := eventFromA2A(ev); out != nil {
		t.Fatalf("nil-artifact event should be dropped, got %+v", out)
	}
}
