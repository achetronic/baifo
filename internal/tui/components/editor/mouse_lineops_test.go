// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// --- line operations -------------------------------------------------

func TestIndentOutdentSingleLine(t *testing.T) {
	m := newTestModel(t, "key: value")
	m, _ = m.Update(press("tab", ""))
	if got := m.Value(); got != "  key: value" {
		t.Fatalf("tab at col 0: got %q", got)
	}
	if m.cursor.col != 2 {
		t.Fatalf("cursor should follow the indent, got col %d", m.cursor.col)
	}
	m, _ = m.Update(press("shift+tab", ""))
	if got := m.Value(); got != "key: value" {
		t.Fatalf("shift+tab: got %q", got)
	}
	if m.cursor.col != 0 {
		t.Fatalf("cursor should follow the outdent, got col %d", m.cursor.col)
	}
}

func TestIndentSelectionCoversAllLines(t *testing.T) {
	m := newTestModel(t, "a\nb\nc")
	// Select from (0,0) into line 2 (shift+right so col > 0 — a
	// selection ending at col 0 does not cover that line, the same
	// convention VS Code uses).
	m, _ = m.Update(press("shift+down", ""))
	m, _ = m.Update(press("shift+down", ""))
	m, _ = m.Update(press("shift+right", ""))
	m, _ = m.Update(press("tab", ""))
	if got := m.Value(); got != "  a\n  b\n  c" {
		t.Fatalf("indent selection: got %q", got)
	}
	if !m.sel.active() {
		t.Fatal("selection should survive an indent so Tab can repeat")
	}
	m, _ = m.Update(press("shift+tab", ""))
	if got := m.Value(); got != "a\nb\nc" {
		t.Fatalf("outdent selection: got %q", got)
	}
}

func TestIndentSelectionEndingAtColZeroSkipsThatLine(t *testing.T) {
	m := newTestModel(t, "a\nb\nc")
	m, _ = m.Update(press("shift+down", ""))
	m, _ = m.Update(press("shift+down", "")) // head at (2,0)
	m, _ = m.Update(press("tab", ""))
	if got := m.Value(); got != "  a\n  b\nc" {
		t.Fatalf("line under col-0 selection end must not indent: got %q", got)
	}
}

func TestOutdentNeverEatsContent(t *testing.T) {
	m := newTestModel(t, "x")
	m, _ = m.Update(press("shift+tab", ""))
	if got := m.Value(); got != "x" {
		t.Fatalf("outdent on unindented line must be a no-op, got %q", got)
	}
}

func TestDuplicateLine(t *testing.T) {
	m := newTestModel(t, "one\ntwo")
	m, _ = m.Update(press("ctrl+d", ""))
	if got := m.Value(); got != "one\none\ntwo" {
		t.Fatalf("ctrl+d: got %q", got)
	}
	if m.cursor.line != 1 {
		t.Fatalf("cursor should land on the copy, got line %d", m.cursor.line)
	}
}

func TestDeleteLine(t *testing.T) {
	m := newTestModel(t, "one\ntwo\nthree")
	m, _ = m.Update(press("down", ""))
	m, _ = m.Update(press("ctrl+k", ""))
	if got := m.Value(); got != "one\nthree" {
		t.Fatalf("ctrl+k: got %q", got)
	}
	// Deleting the only line resets to one empty line.
	m2 := newTestModel(t, "solo")
	m2, _ = m2.Update(press("ctrl+k", ""))
	if got := m2.Value(); got != "" {
		t.Fatalf("ctrl+k on single line: got %q", got)
	}
}

func TestMoveLineUpDown(t *testing.T) {
	m := newTestModel(t, "a\nb\nc")
	m, _ = m.Update(press("down", ""))
	m, _ = m.Update(press("alt+up", ""))
	if got := m.Value(); got != "b\na\nc" {
		t.Fatalf("alt+up: got %q", got)
	}
	if m.cursor.line != 0 {
		t.Fatalf("cursor should ride the moved line, got %d", m.cursor.line)
	}
	m, _ = m.Update(press("alt+down", ""))
	if got := m.Value(); got != "a\nb\nc" {
		t.Fatalf("alt+down: got %q", got)
	}
	// At boundaries it's a no-op.
	m, _ = m.Update(press("alt+up", ""))
	m, _ = m.Update(press("alt+up", ""))
	if got := m.Value(); got != "b\na\nc" {
		t.Fatalf("boundary move mangled the buffer: %q", got)
	}
}

func TestCutLineWithoutSelection(t *testing.T) {
	m := newTestModel(t, "one\ntwo")
	m, cmd := m.Update(press("ctrl+x", ""))
	if got := m.Value(); got != "two" {
		t.Fatalf("ctrl+x without selection should cut the whole line, got %q", got)
	}
	if cmd == nil {
		t.Fatal("cut must schedule a SetClipboard cmd")
	}
	if !m.Dirty() {
		t.Fatal("cut marks the buffer dirty")
	}
}

func TestCopyLineWithoutSelection(t *testing.T) {
	m := newTestModel(t, "one\ntwo")
	_, cmd := m.Update(press("ctrl+c", ""))
	if cmd == nil {
		t.Fatal("ctrl+c without selection should copy the line (cmd expected)")
	}
}

func TestCtrlQQuitsLikeEsc(t *testing.T) {
	m := newTestModel(t, "clean")
	_, cmd := m.Update(press("ctrl+q", ""))
	if cmd == nil {
		t.Fatal("ctrl+q on a clean buffer should emit CancelMsg")
	}
	if msg := cmd(); msg != (CancelMsg{}) {
		t.Fatalf("expected CancelMsg, got %#v", msg)
	}

	dirty := newTestModel(t, "")
	dirty, _ = dirty.Update(press("a", "a"))
	dirty, _ = dirty.Update(press("ctrl+q", ""))
	if !dirty.confirmDiscard {
		t.Fatal("ctrl+q on a dirty buffer should open the discard prompt")
	}
}

func TestCtrlShiftZRedoes(t *testing.T) {
	m := newTestModel(t, "")
	m, _ = m.Update(press("a", "a"))
	m, _ = m.Update(press("ctrl+z", ""))
	if got := m.Value(); got != "" {
		t.Fatalf("undo: got %q", got)
	}
	m, _ = m.Update(press("ctrl+shift+z", ""))
	if got := m.Value(); got != "a" {
		t.Fatalf("ctrl+shift+z redo: got %q", got)
	}
}

// --- mouse -----------------------------------------------------------

// manyLines builds an n-line buffer "line 0".."line n-1".
func manyLines(n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = "line " + strings.Repeat("x", i%5)
	}
	return strings.Join(out, "\n")
}

func TestMouseWheelMovesCursorAndScrolls(t *testing.T) {
	m := newTestModel(t, manyLines(100))
	wheel := tea.MouseWheelMsg{Button: tea.MouseWheelDown}
	m, _ = m.Update(wheel)
	if m.cursor.line != mouseWheelStep {
		t.Fatalf("one wheel notch should move the cursor %d lines, got %d", mouseWheelStep, m.cursor.line)
	}
	// Keep wheeling until the cursor passes the viewport height: the
	// view must follow (syncViewport drags it).
	for i := 0; i < 10; i++ {
		m, _ = m.Update(wheel)
	}
	if m.cursor.line != 33 {
		t.Fatalf("cursor should be at line 33 after 11 notches, got %d", m.cursor.line)
	}
	if off := m.vp.YOffset(); off == 0 {
		t.Fatal("viewport should scroll once the cursor passes the bottom edge")
	}
	// Wheel up returns toward the top.
	for i := 0; i < 11; i++ {
		m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	}
	if m.cursor.line != 0 {
		t.Fatalf("wheel up should return the cursor to line 0, got %d", m.cursor.line)
	}
	if off := m.vp.YOffset(); off != 0 {
		t.Fatalf("viewport should be back at the top, got offset %d", off)
	}
}

func TestMouseWheelClampsAtBoundaries(t *testing.T) {
	m := newTestModel(t, "one\ntwo")
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.cursor.line != 0 {
		t.Fatalf("wheel up at top must clamp, got line %d", m.cursor.line)
	}
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.cursor.line != 1 {
		t.Fatalf("wheel down on a 2-line buffer should land on the last line, got %d", m.cursor.line)
	}
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.cursor.line != 1 {
		t.Fatalf("wheel down at bottom must clamp, got line %d", m.cursor.line)
	}
}

func TestMouseClickPlacesCursor(t *testing.T) {
	m := newTestModel(t, "hello\nworld\nthird")
	// Gutter for 3 lines = 1 digit + 1 pad = 2 cols. Header = 1 row.
	// Click at screen (x=4, y=2) → line 1, col 2.
	click := tea.MouseClickMsg{X: 4, Y: 2, Button: tea.MouseLeft}
	m, _ = m.Update(click)
	if m.cursor.line != 1 || m.cursor.col != 2 {
		t.Fatalf("click should land at (1,2), got (%d,%d)", m.cursor.line, m.cursor.col)
	}
	if m.sel.active() {
		t.Fatal("plain click must clear any selection")
	}
}

func TestMouseClickClampsToLineEnd(t *testing.T) {
	m := newTestModel(t, "ab\ncdef")
	// Click far right of line 0 (len 2).
	m, _ = m.Update(tea.MouseClickMsg{X: 70, Y: 1, Button: tea.MouseLeft})
	if m.cursor.line != 0 || m.cursor.col != 2 {
		t.Fatalf("click past EOL should clamp to (0,2), got (%d,%d)", m.cursor.line, m.cursor.col)
	}
	// Click below the last line lands at the end of the buffer.
	m, _ = m.Update(tea.MouseClickMsg{X: 0, Y: 15, Button: tea.MouseLeft})
	if m.cursor.line != 1 || m.cursor.col != 4 {
		t.Fatalf("click below buffer should land at (1,4), got (%d,%d)", m.cursor.line, m.cursor.col)
	}
}

func TestMouseClickOnHeaderIgnored(t *testing.T) {
	m := newTestModel(t, "hello")
	m, _ = m.Update(press("right", ""))
	before := m.cursor
	m, _ = m.Update(tea.MouseClickMsg{X: 3, Y: 0, Button: tea.MouseLeft})
	if m.cursor != before {
		t.Fatal("clicks on the header row must not move the cursor")
	}
}

func TestMouseDragSelects(t *testing.T) {
	m := newTestModel(t, "hello world")
	// Press at col 0, drag to col 5: selects "hello".
	m, _ = m.Update(tea.MouseClickMsg{X: 2, Y: 1, Button: tea.MouseLeft})
	if !m.dragging {
		t.Fatal("left press should arm dragging")
	}
	m, _ = m.Update(tea.MouseMotionMsg{X: 7, Y: 1, Button: tea.MouseLeft})
	if !m.sel.active() {
		t.Fatal("drag should create a selection")
	}
	a, b := m.sel.rng()
	if got := m.buf.textInRange(a, b); got != "hello" {
		t.Fatalf("drag selection should cover %q, got %q", "hello", got)
	}
	m, _ = m.Update(tea.MouseReleaseMsg{X: 7, Y: 1, Button: tea.MouseLeft})
	if m.dragging {
		t.Fatal("release should disarm dragging")
	}
	if !m.sel.active() {
		t.Fatal("selection must survive the release (so ctrl+c works)")
	}
}

func TestMouseIgnoredWhileModalOpen(t *testing.T) {
	m := newTestModel(t, manyLines(50))
	m.helpOpen = true
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.cursor.line != 0 {
		t.Fatal("wheel must be inert while a modal is open")
	}
	m, _ = m.Update(tea.MouseClickMsg{X: 4, Y: 3, Button: tea.MouseLeft})
	if m.cursor.line != 0 {
		t.Fatal("click must be inert while a modal is open")
	}
}
