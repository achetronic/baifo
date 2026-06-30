// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package editor

import "testing"

func TestHorizontalScroll_TracksCursorRight(t *testing.T) {
	// 100-char single line, narrow viewport.
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	m := New(Options{InitialValue: long})
	m.SetSize(30, 10) // viewport width 30; gutter is small for this small line count

	// Move cursor to col 80; xOffset must shift so it stays visible.
	m, _ = m.Update(press("end", ""))
	if m.cursor.col != 100 {
		t.Fatalf("setup: cursor expected at 100, got %d", m.cursor.col)
	}
	if m.xOffset == 0 {
		t.Errorf("xOffset should have advanced for long line, got %d", m.xOffset)
	}
	// xOffset should not overshoot — keep cursor on screen.
	width := m.contentWidth()
	if m.cursor.col < m.xOffset || m.cursor.col >= m.xOffset+width+1 {
		t.Errorf("cursor %d outside visible range [%d, %d)", m.cursor.col, m.xOffset, m.xOffset+width+1)
	}
}

func TestHorizontalScroll_ReturnsToZeroWhenCursorMovesLeft(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "y"
	}
	m := New(Options{InitialValue: long})
	m.SetSize(30, 10)

	// Push cursor to the right, then back to start.
	m, _ = m.Update(press("end", ""))
	if m.xOffset == 0 {
		t.Fatalf("setup: expected xOffset > 0 after end")
	}
	m, _ = m.Update(press("home", ""))
	if m.xOffset != 0 {
		t.Errorf("xOffset should be 0 after home, got %d", m.xOffset)
	}
}

func TestContentWidth_AccountsForGutter(t *testing.T) {
	m := New(Options{InitialValue: "a\nb\nc"})
	m.SetSize(20, 10)
	// 3 lines -> 1-digit gutter -> gutter width is 3 ("N + space + space").
	// So contentWidth() should be 20 - 3 = 17.
	if got := m.contentWidth(); got != 17 {
		t.Errorf("contentWidth: got %d, want 17", got)
	}
}
