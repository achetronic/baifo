// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestPaletteSuggest_TopLevel exercises the "user just hit slash"
// state: every top-level command should surface, with their
// display name prepended by a slash.
func TestPaletteSuggest_TopLevel(t *testing.T) {
	visible, items, repl := paletteSuggest("/")
	if !visible {
		t.Fatalf("popup must be visible after lone '/'")
	}
	if repl != 0 {
		t.Errorf("replaceLen at '/' want 0, got %d", repl)
	}
	if len(items) == 0 {
		t.Fatal("expected top-level commands, got none")
	}
	for _, it := range items {
		if !strings.HasPrefix(it.DisplayName, "/") {
			t.Errorf("top-level item %q must display with leading slash, got %q",
				it.Name, it.DisplayName)
		}
	}
}

// TestPaletteSuggest_HiddenWithoutSlash verifies that any non-slash
// input keeps the popup invisible. The popup must never spam the
// user during normal chat.
func TestPaletteSuggest_HiddenWithoutSlash(t *testing.T) {
	for _, in := range []string{"", "hello", " /session", "no/slash/start"} {
		visible, items, _ := paletteSuggest(in)
		if visible || len(items) != 0 {
			t.Errorf("expected hidden popup for %q, got visible=%v items=%d", in, visible, len(items))
		}
	}
}

// TestPaletteSuggest_PrefixFilter verifies that the user typing
// "/se" narrows the top-level list to commands whose name starts
// with "se" (sessions, secrets, settings).
func TestPaletteSuggest_PrefixFilter(t *testing.T) {
	visible, items, repl := paletteSuggest("/se")
	if !visible {
		t.Fatal("popup must be visible for /se")
	}
	if repl != 2 {
		t.Errorf("replaceLen for /se want 2, got %d", repl)
	}
	found := map[string]bool{}
	for _, it := range items {
		found[it.Name] = true
	}
	for _, want := range []string{"session", "secret", "settings"} {
		if !found[want] {
			t.Errorf("expected /se to suggest %q, missing", want)
		}
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Name, "se") {
			t.Errorf("filtered result %q does not start with prefix 'se'", it.Name)
		}
	}
}

// TestPaletteSuggest_DescendsAfterSpace checks the cascade: once
// the user commits a verb and types a space, the popup must list
// the sub-verbs of that node and stop offering siblings.
func TestPaletteSuggest_DescendsAfterSpace(t *testing.T) {
	visible, items, repl := paletteSuggest("/mcp ")
	if !visible {
		t.Fatal("popup must be visible after /mcp + space")
	}
	if repl != 0 {
		t.Errorf("replaceLen for /mcp want 0, got %d", repl)
	}
	want := map[string]bool{
		"list":   true,
		"add":    true,
		"edit":   true,
		"delete": true,
		"auth":   true,
		"test":   true,
		"logout": true,
	}
	if len(items) != len(want) {
		t.Errorf("got %d sub-verbs, want %d", len(items), len(want))
	}
	for _, it := range items {
		if !want[it.Name] {
			t.Errorf("unexpected sub-verb %q in /mcp level", it.Name)
		}
		if strings.HasPrefix(it.DisplayName, "/") {
			t.Errorf("sub-verb %q must NOT show leading slash, got %q", it.Name, it.DisplayName)
		}
	}
}

// TestPaletteSuggest_DescendsAndFilters combines the two prior
// behaviours: cascading into /mcp and then narrowing by prefix.
func TestPaletteSuggest_DescendsAndFilters(t *testing.T) {
	visible, items, repl := paletteSuggest("/mcp ad")
	if !visible {
		t.Fatal("popup must be visible for /mcp ad")
	}
	if repl != 2 {
		t.Errorf("replaceLen want 2, got %d", repl)
	}
	if len(items) != 1 || items[0].Name != "add" {
		t.Fatalf("expected just 'add', got %v", items)
	}
}

// TestPaletteSuggest_HidesOnFreeformArgs ensures that once the
// user is typing data the popup gets out of the way. After
// "/mcp add my-name" there are no structured suggestions left
// — the next token is a NAME the user owns.
func TestPaletteSuggest_HidesOnFreeformArgs(t *testing.T) {
	for _, in := range []string{"/mcp add my-name", "/mcp add my-name ", "/session switch abc def"} {
		visible, items, _ := paletteSuggest(in)
		if visible || len(items) != 0 {
			t.Errorf("popup must hide on free-form arg for %q, got visible=%v items=%d",
				in, visible, len(items))
		}
	}
}

// TestPaletteSuggest_UnknownVerb checks that typing a top-level
// verb we don't know still keeps the popup hidden (rather than
// silently descending into something arbitrary).
func TestPaletteSuggest_UnknownVerb(t *testing.T) {
	visible, _, _ := paletteSuggest("/nope ")
	if visible {
		t.Error("popup must hide after unknown verb")
	}
}

// TestPaletteState_AcceptLeafVsBranch verifies the trailing-space
// behaviour: accepting a branch verb appends a space so the
// cascade continues; accepting a leaf does not.
func TestPaletteState_AcceptLeafVsBranch(t *testing.T) {
	st := paletteState{}
	st.refresh("/mc")
	if !st.Visible || len(st.Items) == 0 {
		t.Fatalf("refresh('/mc') failed: %+v", st)
	}
	// /mcp is a branch — should append a space.
	out, ok := st.accept("/mc")
	if !ok {
		t.Fatal("accept must succeed on visible popup")
	}
	if out != "/mcp " {
		t.Errorf("accept('/mc') = %q, want '/mcp '", out)
	}

	// Now do a leaf. /help is a top-level leaf.
	st = paletteState{}
	st.refresh("/he")
	out, ok = st.accept("/he")
	if !ok {
		t.Fatal("accept must succeed for /he")
	}
	if out != "/help" {
		t.Errorf("accept('/he') = %q, want '/help' (no trailing space for a leaf)", out)
	}
}

// TestPaletteState_AcceptCascade walks the popup end-to-end:
// type '/', accept top, type 'ad', accept sub, end up with the
// fully-typed command + space.
func TestPaletteState_AcceptCascade(t *testing.T) {
	line := "/mc"
	st := paletteState{}
	st.refresh(line)
	line, ok := st.accept(line)
	if !ok || line != "/mcp " {
		t.Fatalf("first accept produced %q, want '/mcp '", line)
	}
	st.refresh(line)
	if !st.Visible {
		t.Fatalf("after cascade popup must remain visible: %+v", st)
	}
	// Type "ad" — simulate by appending; the textarea would do
	// this for us in real life.
	line += "ad"
	st.refresh(line)
	if len(st.Items) != 1 || st.Items[0].Name != "add" {
		t.Fatalf("after typing 'ad' want only 'add', got %+v", st.Items)
	}
	line, ok = st.accept(line)
	if !ok {
		t.Fatal("accept failed at /mcp add")
	}
	// 'add' is a leaf (it takes a free-form NAME arg) — no trailing space.
	if line != "/mcp add" {
		t.Errorf("final line %q, want '/mcp add'", line)
	}
	// Once the user moves past the leaf verb (typing the next
	// space or any free-form token), the popup must hide.
	st.refresh(line + " ")
	if st.Visible {
		t.Errorf("popup must hide once we're past a leaf and into free-form args")
	}
}

// TestPaletteState_MoveWraps confirms that Up at the top jumps to
// the bottom and Down at the bottom jumps to the top. The popup
// is short enough that wrapping is the friendliest choice.
func TestPaletteState_MoveWraps(t *testing.T) {
	st := paletteState{}
	st.refresh("/")
	if len(st.Items) < 2 {
		t.Fatalf("need at least 2 items for wrap test, got %d", len(st.Items))
	}
	last := len(st.Items) - 1
	st.move(-1)
	if st.Selected != last {
		t.Errorf("Up at top: got %d, want %d", st.Selected, last)
	}
	st.move(+1)
	if st.Selected != 0 {
		t.Errorf("Down at bottom: got %d, want 0", st.Selected)
	}
}

// TestRenderPalette_NonEmpty is a smoke test: hand renderPalette a
// realistic state and check that the output is non-empty and has
// the expected number of rows: ≤ paletteMaxRows suggestions + 1
// footer hint row + 2 border rows (rounded frame).
func TestRenderPalette_NonEmpty(t *testing.T) {
	st := paletteState{}
	st.refresh("/")
	theme := NewTheme(false)
	out := renderPalette(theme, st, 120)
	if out == "" {
		t.Fatal("renderPalette returned empty string for visible popup")
	}
	rows := strings.Split(out, "\n")
	if len(rows) == 0 || len(rows) > paletteMaxRows+3 {
		t.Errorf("rendered %d rows, want 1..%d", len(rows), paletteMaxRows+3)
	}
	// Every row must be the same width so the splice into the body
	// doesn't shift columns.
	w := lipgloss.Width(rows[0])
	for i, r := range rows {
		if lipgloss.Width(r) != w {
			t.Errorf("row %d width %d != row 0 width %d", i, lipgloss.Width(r), w)
		}
	}
}

// TestOverlayPaletteAboveComposer_PreservesShape ensures the
// splice keeps the body's total row count constant so the
// layout downstream doesn't shift.
func TestOverlayPaletteAboveComposer_PreservesShape(t *testing.T) {
	body := strings.Repeat("line\n", 20)
	body = strings.TrimRight(body, "\n")
	rowsBefore := len(strings.Split(body, "\n"))

	popup := "popup row 1\npopup row 2\npopup row 3"
	out := overlayPaletteAboveComposer(body, popup)
	rowsAfter := len(strings.Split(out, "\n"))
	if rowsAfter != rowsBefore {
		t.Errorf("row count changed: before=%d after=%d", rowsBefore, rowsAfter)
	}
	if !strings.Contains(out, "popup row 1") {
		t.Error("expected popup content to be spliced into the body")
	}
}

// TestOverlayPaletteAboveComposer_EmptyPopupIsNoOp confirms that
// passing an empty popup string returns the body verbatim, so the
// caller can use the overlay unconditionally.
func TestOverlayPaletteAboveComposer_EmptyPopupIsNoOp(t *testing.T) {
	body := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"
	if got := overlayPaletteAboveComposer(body, ""); got != body {
		t.Errorf("empty popup must be a no-op; got %q", got)
	}
}

// TestPaletteSuggest_AlphabeticalOrder verifies that every item in the
// top-level popup is sorted alphabetically (case-insensitive), with no
// view/command grouping — they are all commands now.
func TestPaletteSuggest_AlphabeticalOrder(t *testing.T) {
	visible, items, _ := paletteSuggest("/")
	if !visible {
		t.Fatal("expected visible popup for '/'")
	}

	var last string
	for _, it := range items {
		cur := strings.ToLower(it.Name)
		if last != "" && cur < last {
			t.Errorf("items not in alphabetical order: %q came after %q", it.Name, last)
		}
		last = cur
	}
}
