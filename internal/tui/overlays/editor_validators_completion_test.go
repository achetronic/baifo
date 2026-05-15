// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package overlays

import (
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// fakeFacade implements a minimal subset of facade.Facade for testing.
type fakeFacade struct {
	facade.Facade
	details []facade.ProviderDetail
}

func (f fakeFacade) ProviderDetails() []facade.ProviderDetail {
	return f.details
}

// TestModelCompletionProvider_ReturnsManyModels is a coarse
// smoke test: catwalk's vendored catalogue is ~1100+ entries
// today; even with future additions/removals the list should
// stay safely above 50.
func TestModelCompletionProvider_ReturnsManyModels(t *testing.T) {
	provider := ModelCompletionProvider(nil)
	out := provider("", editor.CompletionContext{})
	if len(out) < 50 {
		t.Fatalf("expected at least 50 catwalk models, got %d", len(out))
	}
}

// TestModelCompletionProvider_IncludesGemini verifies the path
// the user is most likely to hit: typing `model: gem` and
// expecting Gemini suggestions. The editor's substring filter
// runs on Completion.View, so we assert that "gemini" appears
// inside at least one View.
func TestModelCompletionProvider_IncludesGemini(t *testing.T) {
	provider := ModelCompletionProvider(nil)
	out := provider("", editor.CompletionContext{})
	found := false
	for _, c := range out {
		if strings.Contains(strings.ToLower(c.View), "gemini") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one entry with 'gemini' in its View")
	}
}

// TestModelCompletionProvider_ViewIsProviderSlashModel pins the
// View shape so the editor's substring filter works the way the
// docs promise: typing "gemini" narrows to gemini-prefixed
// entries. We check that every entry contains at least one
// "/" (which both `gemini/gemini-pro` and the multi-segment
// `groq/moonshotai/kimi-...` shapes satisfy).
func TestModelCompletionProvider_ViewIsProviderSlashModel(t *testing.T) {
	provider := ModelCompletionProvider(nil)
	out := provider("", editor.CompletionContext{})
	for _, c := range out {
		if !strings.Contains(c.View, "/") {
			t.Errorf("View %q should contain '/' (shape provider/model_id)", c.View)
			return
		}
	}
}

// TestModelCompletionProvider_InsertReEmitsKey confirms what
// lands in the buffer carries the whole `model: <id>` line, not
// just the bare id. The editor's completer replaces the trigger
// (`model: `) verbatim on accept, so a bare-id Insert would
// erase the key and leave the user with only `<id>`. Slashes
// inside the id are intentionally allowed (Groq's
// "moonshotai/kimi-k2-..." is a real catwalk entry).
func TestModelCompletionProvider_InsertReEmitsKey(t *testing.T) {
	provider := ModelCompletionProvider(nil)
	out := provider("", editor.CompletionContext{})
	for _, c := range out {
		if !strings.HasPrefix(c.Insert, "model: ") {
			t.Errorf("Insert %q must start with 'model: ' (got View=%q)",
				c.Insert, c.View)
			return
		}
		// The trailing portion must not contain a newline; YAML
		// scalars are single-line for these fields.
		if strings.Contains(c.Insert, "\n") {
			t.Errorf("Insert %q must not contain a newline", c.Insert)
			return
		}
	}
}

// TestModelCompletionProvider_WithMatchedProvider checks that when a sibling
// provider line is parsed and matched via the facade to a custom URL,
// only the matched provider's models are suggested.
func TestModelCompletionProvider_WithMatchedProvider(t *testing.T) {
	fake := fakeFacade{
		details: []facade.ProviderDetail{
			{
				Name: "router",
				Type: "openai",
				URL:  "https://openrouter.ai/api/v1",
			},
		},
	}

	provider := ModelCompletionProvider(fake)
	ctx := editor.CompletionContext{
		Lines: []string{
			"provider: router",
			"model: ",
		},
		Line: 1, // cursor on "model: "
	}

	out := provider("", ctx)
	if len(out) == 0 {
		t.Fatalf("expected some completions, got 0")
	}

	for _, c := range out {
		if !strings.HasPrefix(c.View, "openrouter/") {
			t.Errorf("expected View to start with 'openrouter/', got %q", c.View)
		}
	}
}

// TestProviderTypeCompletionProvider_HasKnownProviders pins a
// handful of well-known catwalk provider IDs so a sudden empty
// list (catwalk rename, broken import, ...) shows up in CI.
func TestProviderTypeCompletionProvider_HasKnownProviders(t *testing.T) {
	provider := ProviderTypeCompletionProvider()
	out := provider("", editor.CompletionContext{})
	ids := make(map[string]struct{}, len(out))
	for _, c := range out {
		// Insert is of shape "type: <id>"; strip the prefix to
		// recover the bare id we want to check membership on.
		id := strings.TrimPrefix(c.Insert, "type: ")
		ids[id] = struct{}{}
	}
	// "gemini", "anthropic", "openai" are the three majors baifo
	// ships providers for today. If catwalk ever drops one of
	// them this test fails loudly (exactly what we want).
	for _, want := range []string{"gemini", "anthropic", "openai"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("provider list missing %q (got %d entries)", want, len(ids))
		}
	}
}

// TestProviderTypeCompletionProvider_InsertReEmitsKey mirrors
// the model case: Insert must start with `type: ` because the
// completer wipes the trigger and we'd lose the key otherwise.
func TestProviderTypeCompletionProvider_InsertReEmitsKey(t *testing.T) {
	provider := ProviderTypeCompletionProvider()
	out := provider("", editor.CompletionContext{})
	for _, c := range out {
		if !strings.HasPrefix(c.Insert, "type: ") {
			t.Errorf("Insert %q must start with 'type: '", c.Insert)
		}
		if strings.Contains(strings.TrimPrefix(c.Insert, "type: "), " ") {
			t.Errorf("Insert tail (after 'type: ') %q must not contain spaces", c.Insert)
		}
	}
}
