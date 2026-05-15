// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package overlays

import (
	"strings"
	"testing"

	"charm.land/catwalk/pkg/embedded"

	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// firstNonReasoningModelID returns the id of any catalogue model whose
// CanReason is false, skipping the test if (improbably) none exist.
func firstNonReasoningModelID(t *testing.T) string {
	t.Helper()
	for _, p := range embedded.GetAll() {
		for _, m := range p.Models {
			if !m.CanReason {
				return m.ID
			}
		}
	}
	t.Skip("no non-reasoning model in catalogue")
	return ""
}

// helper: collect the Insert values of a completion list.
func inserts(items []editor.Completion) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Insert)
	}
	return out
}

func hasInsert(items []editor.Completion, want string) bool {
	for _, it := range items {
		if it.Insert == want {
			return true
		}
	}
	return false
}

// TestReasoningCompletion_UnknownModelOffersFullSet: with no model
// line (or an unknown id), every baifo level plus off is offered.
func TestReasoningCompletion_UnknownModelOffersFullSet(t *testing.T) {
	p := ReasoningCompletionProvider()
	out := p("", editor.CompletionContext{Lines: []string{"reasoning: "}, Line: 0})
	for _, lvl := range []string{"off", "minimal", "low", "medium", "high"} {
		if !hasInsert(out, "reasoning: "+lvl) {
			t.Errorf("unknown model should offer %q; got %v", lvl, inserts(out))
		}
	}
}

// TestReasoningCompletion_KnownReasoningModelIntersects: gpt-5 lists
// minimal/low/medium/high in catwalk — all within the baifo vocab — so
// all four are offered. A non-baifo level like "xhigh" must never leak.
func TestReasoningCompletion_KnownReasoningModelIntersects(t *testing.T) {
	lines := []string{
		"llm:",
		"  model: gpt-5",
		"  reasoning: ",
	}
	p := ReasoningCompletionProvider()
	out := p("", editor.CompletionContext{Lines: lines, Line: 2})

	for _, lvl := range []string{"minimal", "low", "medium", "high"} {
		if !hasInsert(out, "reasoning: "+lvl) {
			t.Errorf("gpt-5 should offer %q; got %v", lvl, inserts(out))
		}
	}
	for _, bad := range inserts(out) {
		if strings.Contains(bad, "xhigh") || strings.Contains(bad, "max") {
			t.Errorf("must not offer out-of-vocabulary level: %q", bad)
		}
	}
}

// TestReasoningCompletion_ModelDefaultAnnotated: the model's default
// effort is flagged in the View so the user can see it.
func TestReasoningCompletion_ModelDefaultAnnotated(t *testing.T) {
	lines := []string{"  model: gpt-5", "  reasoning: "}
	p := ReasoningCompletionProvider()
	out := p("", editor.CompletionContext{Lines: lines, Line: 1})

	foundDefault := false
	for _, it := range out {
		if strings.Contains(it.View, "(model default)") {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Errorf("expected the model default effort to be annotated; got %+v", out)
	}
}

// TestReasoningCompletion_NonReasoningModelOnlyOff: a known model that
// cannot reason offers only "off", annotated to explain why.
func TestReasoningCompletion_NonReasoningModelOnlyOff(t *testing.T) {
	// Find a known non-reasoning model id from the catalogue so the
	// test stays valid as the catalogue evolves.
	id := firstNonReasoningModelID(t)
	lines := []string{"  model: " + id, "  reasoning: "}
	p := ReasoningCompletionProvider()
	out := p("", editor.CompletionContext{Lines: lines, Line: 1})

	if len(out) != 1 {
		t.Fatalf("non-reasoning model should offer only off, got %v", inserts(out))
	}
	if out[0].Insert != "reasoning: off" {
		t.Errorf("expected the single entry to be 'reasoning: off', got %q", out[0].Insert)
	}
	if !strings.Contains(out[0].View, "not a reasoning model") {
		t.Errorf("expected an explanatory annotation, got %q", out[0].View)
	}
}

// TestModelIDFromLines_PrefersNearestAbove confirms the parser picks
// the model line above the cursor and tolerates indentation, quotes
// and inline comments.
func TestModelIDFromLines_PrefersNearestAbove(t *testing.T) {
	lines := []string{
		`  model: "claude-sonnet-4-6"  # primary`,
		"  reasoning: ",
	}
	if got := modelIDFromLines(lines, 1); got != "claude-sonnet-4-6" {
		t.Errorf("modelIDFromLines = %q, want claude-sonnet-4-6", got)
	}
	if got := modelIDFromLines([]string{"name: x"}, 0); got != "" {
		t.Errorf("expected empty when no model line, got %q", got)
	}
}
