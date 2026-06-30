// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"strings"
	"testing"
)

// makeLines returns n numbered lines joined with newlines, so the
// buffer is comfortably taller than any test viewport.
func makeLines(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = "line " + strings.Repeat("x", 3)
	}
	return strings.Join(rows, "\n")
}

// TestVerticalScroll_FollowsCursorToBottom is the regression for the
// "can't scroll past what fit on first paint" bug. A buffer far taller
// than the viewport must scroll so the last line is visible once the
// cursor jumps to the document end.
func TestVerticalScroll_FollowsCursorToBottom(t *testing.T) {
	m := New(Options{InitialValue: makeLines(100)})
	m.SetSize(40, 10) // small viewport: ~8 content rows after chrome

	// Jump to the end of the document.
	m, _ = m.Update(press("ctrl+end", ""))

	if m.cursor.line != 99 {
		t.Fatalf("setup: cursor expected on last line 99, got %d", m.cursor.line)
	}

	off := m.vp.YOffset()
	height := m.vp.Height()
	// The cursor line must be within the visible window [off, off+height).
	if m.cursor.line < off || m.cursor.line >= off+height {
		t.Fatalf("cursor line %d not visible: offset=%d height=%d", m.cursor.line, off, height)
	}
	// And the viewport must actually have scrolled (offset > 0), proving
	// it didn't get clamped to a stale max from the pre-render content.
	if off == 0 {
		t.Errorf("viewport did not scroll: YOffset still 0 with a 100-line buffer in a %d-row window", height)
	}
}

// TestVerticalScroll_FollowsCursorWhenTypingGrowsBuffer reproduces the
// exact reported flow: keep adding newlines until the buffer outgrows
// the viewport, and confirm the view tracks the cursor down instead of
// freezing at the bottom of what first fit.
func TestVerticalScroll_FollowsCursorWhenTypingGrowsBuffer(t *testing.T) {
	m := New(Options{InitialValue: ""})
	m.SetSize(40, 8)

	// Insert 40 newlines (Enter), each pushing the cursor one row down.
	for i := 0; i < 40; i++ {
		m, _ = m.Update(press("enter", ""))
	}

	off := m.vp.YOffset()
	height := m.vp.Height()
	if m.cursor.line < off || m.cursor.line >= off+height {
		t.Fatalf("cursor line %d not visible after growth: offset=%d height=%d", m.cursor.line, off, height)
	}
	if off == 0 {
		t.Errorf("viewport never scrolled while the buffer grew to %d lines", m.cursor.line+1)
	}
}

// TestVerticalScroll_BackToTop confirms scrolling up to the document
// start brings the offset back to 0.
func TestVerticalScroll_BackToTop(t *testing.T) {
	m := New(Options{InitialValue: makeLines(100)})
	m.SetSize(40, 10)

	m, _ = m.Update(press("ctrl+end", ""))
	if m.vp.YOffset() == 0 {
		t.Fatalf("setup: expected a scrolled viewport after ctrl+end")
	}
	m, _ = m.Update(press("ctrl+home", ""))
	if m.cursor.line != 0 {
		t.Fatalf("ctrl+home should land on line 0, got %d", m.cursor.line)
	}
	if m.vp.YOffset() != 0 {
		t.Errorf("YOffset should be 0 at document top, got %d", m.vp.YOffset())
	}
}
