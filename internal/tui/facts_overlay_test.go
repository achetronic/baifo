// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/facade"
)

// factsFacade extends recordingFacade with a mutable facts store so
// the overlay tests can exercise list / delete round-trips.
type factsFacade struct {
	recordingFacade
	facts []facade.FactDetail
}

func (f *factsFacade) FactDetails() []facade.FactDetail { return f.facts }
func (f *factsFacade) DeleteFact(_ context.Context, id uint64) error {
	out := f.facts[:0]
	for _, d := range f.facts {
		if d.ID != id {
			out = append(out, d)
		}
	}
	f.facts = out
	return nil
}
func (f *factsFacade) FactContent(id uint64) (string, string, error) {
	for _, d := range f.facts {
		if d.ID == id {
			return d.Content, d.Category, nil
		}
	}
	return "", "", nil
}

func twoFacts() []facade.FactDetail {
	return []facade.FactDetail{
		{ID: 2, Content: "newest fact", Category: "personal", Author: "user", Timestamp: time.Now()},
		{ID: 1, Content: "older fact\nwith a second line", Author: "agent", Timestamp: time.Now()},
	}
}

// TestFactListOpensOverlay pins the new behaviour: /fact (and
// /fact list) open the navigable overlay instead of dumping an
// inline system message.
func TestFactListOpensOverlay(t *testing.T) {
	f := &factsFacade{facts: twoFacts()}
	m := newModelWith(t, f)
	for _, cmd := range []string{"/fact", "/fact list"} {
		res := m.handleSlashCommand(cmd)
		if !res.openFactsOverlay {
			t.Errorf("%s must set openFactsOverlay, got %+v", cmd, res)
		}
		mm, _ := m.applySlashResult(res)
		got := mm.(Model)
		if !got.factsOpen {
			t.Errorf("%s: overlay flag not set after applySlashResult", cmd)
		}
		if len(got.facts) != 2 {
			t.Errorf("%s: facts not refreshed on open, got %d", cmd, len(got.facts))
		}
	}
}

// TestFactsOverlayNavigationAndClose drives the overlay keymap:
// down moves the selection, esc closes.
func TestFactsOverlayNavigationAndClose(t *testing.T) {
	f := &factsFacade{facts: twoFacts()}
	m := newModelWith(t, f)
	mm, _ := m.applySlashResult(slashResult{openFactsOverlay: true})
	cur := mm.(Model)

	mm, _ = cur.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	cur = mm.(Model)
	if cur.factsSel != 1 {
		t.Fatalf("down should move selection to 1, got %d", cur.factsSel)
	}

	mm, _ = cur.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cur = mm.(Model)
	if cur.factsOpen {
		t.Fatal("esc should close the facts overlay")
	}
}

// TestFactsOverlayDeleteFlow exercises the d → y confirmation:
// the fact is removed from the facade AND the overlay's list
// refreshes in place (the overlay stays open).
func TestFactsOverlayDeleteFlow(t *testing.T) {
	f := &factsFacade{facts: twoFacts()}
	m := newModelWith(t, f)
	mm, _ := m.applySlashResult(slashResult{openFactsOverlay: true})
	cur := mm.(Model)

	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'd'})
	cur = mm.(Model)
	if cur.factsConfirmDelete != 2 {
		t.Fatalf("d should arm confirm for fact #2, got %d", cur.factsConfirmDelete)
	}

	// n cancels.
	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'n'})
	cur = mm.(Model)
	if cur.factsConfirmDelete != 0 {
		t.Fatal("n should cancel the delete confirmation")
	}
	if len(f.facts) != 2 {
		t.Fatal("cancelled delete must not touch the store")
	}

	// d → y actually deletes.
	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'd'})
	cur = mm.(Model)
	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'y'})
	cur = mm.(Model)
	if len(f.facts) != 1 || f.facts[0].ID != 1 {
		t.Fatalf("fact #2 should be deleted, remaining %+v", f.facts)
	}
	if len(cur.facts) != 1 {
		t.Fatalf("overlay list should refresh after delete, got %d", len(cur.facts))
	}
	if !cur.factsOpen {
		t.Fatal("overlay should stay open after a delete")
	}
}

// TestFactsOverlayEnterOpensEditor verifies Enter on a row opens
// the embedded editor seeded with the fact's content and closes
// the overlay underneath.
func TestFactsOverlayEnterOpensEditor(t *testing.T) {
	f := &factsFacade{facts: twoFacts()}
	m := newModelWith(t, f)
	mm, _ := m.applySlashResult(slashResult{openFactsOverlay: true})
	cur := mm.(Model)

	mm, _ = cur.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cur = mm.(Model)
	if cur.factsOpen {
		t.Fatal("overlay should close when the editor opens")
	}
	if cur.editor == nil {
		t.Fatal("enter should open the embedded editor")
	}
	if cur.editorFactTargetID != 2 {
		t.Fatalf("editor should target fact #2, got %d", cur.editorFactTargetID)
	}
}

// TestRenderFactsShowsEntriesAndConfirm smoke-tests the renderer:
// content first line as label, meta row with id, and the
// destructive prompt when a delete is pending.
func TestRenderFactsShowsEntriesAndConfirm(t *testing.T) {
	theme := NewTheme(false)
	back := strings.Repeat(strings.Repeat(" ", 100)+"\n", 39) + strings.Repeat(" ", 100)

	out := renderFacts(theme, twoFacts(), 0, 0, back, 100, 40)
	for _, want := range []string{"Facts", "newest fact", "older fact", "#1", "#2"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered overlay missing %q", want)
		}
	}
	if strings.Contains(out, "with a second line") {
		t.Error("only the first content line should be the label")
	}

	confirm := renderFacts(theme, twoFacts(), 0, 2, back, 100, 40)
	if !strings.Contains(confirm, "delete fact #2?") {
		t.Error("pending delete must surface the y/N prompt")
	}
}

// TestEnterSubmitsWithPaletteOpen pins the new composer semantics:
// Enter with the suggestion popup visible SENDS the typed text
// as-is (it must not accept the highlighted suggestion). Tab is
// the completion key.
func TestEnterSubmitsWithPaletteOpen(t *testing.T) {
	f := &factsFacade{}
	m := newModelWith(t, f)
	m.composer.ta.SetValue("/help")
	m.palette.refresh("/help")
	if !m.palette.Visible {
		t.Fatal("precondition: palette should be visible for /help")
	}

	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cur := mm.(Model)
	if !cur.helpOpen {
		t.Fatal("enter should have submitted /help directly (help overlay open)")
	}
	if cur.palette.Visible {
		t.Fatal("palette should be dismissed after submit")
	}
}

// TestTabAcceptsSuggestion confirms Tab still completes the
// highlighted suggestion without submitting.
func TestTabAcceptsSuggestion(t *testing.T) {
	f := &factsFacade{}
	m := newModelWith(t, f)
	m.composer.ta.SetValue("/he")
	m.palette.refresh("/he")
	if !m.palette.Visible {
		t.Fatal("precondition: palette should be visible for /he")
	}

	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	cur := mm.(Model)
	if got := cur.composer.ta.Value(); got != "/help" {
		t.Fatalf("tab should complete to /help, got %q", got)
	}
	if cur.helpOpen {
		t.Fatal("tab must not submit the command")
	}
}
