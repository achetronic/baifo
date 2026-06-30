// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"strings"
	"testing"
)

func TestBuffer_NewSplitsLines(t *testing.T) {
	b := newBuffer("a\nb\nc")
	if b.lineCount() != 3 {
		t.Fatalf("lineCount: got %d, want 3", b.lineCount())
	}
	got := strings.Join(b.lines(), "|")
	if got != "a|b|c" {
		t.Errorf("lines: got %q, want a|b|c", got)
	}
}

func TestBuffer_NewEmpty(t *testing.T) {
	b := newBuffer("")
	if b.lineCount() != 1 {
		t.Fatalf("empty buffer must have 1 row, got %d", b.lineCount())
	}
	if b.lineLen(0) != 0 {
		t.Errorf("empty buffer row should be empty, got %d runes", b.lineLen(0))
	}
}

func TestBuffer_NewNormalisesCRLF(t *testing.T) {
	b := newBuffer("a\r\nb\rc")
	if b.lineCount() != 3 {
		t.Errorf("CRLF and lone CR should both split lines, got %d", b.lineCount())
	}
}

func TestBuffer_InsertRune(t *testing.T) {
	b := newBuffer("hello")
	col := b.insertRune(0, 5, '!')
	if col != 6 {
		t.Errorf("new col: got %d, want 6", col)
	}
	if got := b.lines()[0]; got != "hello!" {
		t.Errorf("line: got %q, want hello!", got)
	}
}

func TestBuffer_InsertStringMultiline(t *testing.T) {
	b := newBuffer("abc")
	row, col := b.insertString(0, 1, "1\n2\n3")
	// Resulting buffer: ["a1", "2", "3bc"], cursor at (2, 1).
	want := []string{"a1", "2", "3bc"}
	got := b.lines()
	if len(got) != 3 {
		t.Fatalf("lineCount: got %d, want 3", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
	if row != 2 || col != 1 {
		t.Errorf("end pos: got (%d,%d), want (2,1)", row, col)
	}
}

func TestBuffer_DeleteRuneInLine(t *testing.T) {
	b := newBuffer("abcd")
	row, col := b.deleteRune(0, 3)
	if row != 0 || col != 2 {
		t.Errorf("pos: got (%d,%d), want (0,2)", row, col)
	}
	if got := b.lines()[0]; got != "abd" {
		t.Errorf("line: got %q, want abd", got)
	}
}

func TestBuffer_DeleteRuneMergesLines(t *testing.T) {
	b := newBuffer("ab\ncd")
	row, col := b.deleteRune(1, 0)
	if row != 0 || col != 2 {
		t.Errorf("pos: got (%d,%d), want (0,2)", row, col)
	}
	if got := b.lines(); len(got) != 1 || got[0] != "abcd" {
		t.Errorf("lines: got %v, want [abcd]", got)
	}
}

func TestBuffer_SplitLine(t *testing.T) {
	b := newBuffer("hello")
	row, col := b.splitLine(0, 3)
	if row != 1 || col != 0 {
		t.Errorf("pos: got (%d,%d), want (1,0)", row, col)
	}
	got := b.lines()
	if len(got) != 2 || got[0] != "hel" || got[1] != "lo" {
		t.Errorf("lines: got %v, want [hel lo]", got)
	}
}

func TestBuffer_DeleteRangeSameLine(t *testing.T) {
	b := newBuffer("hello world")
	p := b.deleteRange(position{0, 5}, position{0, 11})
	if p.line != 0 || p.col != 5 {
		t.Errorf("pos: got %v, want (0,5)", p)
	}
	if got := b.lines()[0]; got != "hello" {
		t.Errorf("line: got %q, want hello", got)
	}
}

func TestBuffer_DeleteRangeMultiLine(t *testing.T) {
	b := newBuffer("hello\nbig\nworld")
	p := b.deleteRange(position{0, 5}, position{2, 0})
	if p.line != 0 || p.col != 5 {
		t.Errorf("pos: got %v, want (0,5)", p)
	}
	if got := b.lines(); len(got) != 1 || got[0] != "helloworld" {
		t.Errorf("lines: got %v, want [helloworld]", got)
	}
}

func TestBuffer_TextInRangeMultiLine(t *testing.T) {
	b := newBuffer("hello\nbig\nworld")
	got := b.textInRange(position{0, 5}, position{2, 0})
	want := "\nbig\n"
	if got != want {
		t.Errorf("text: got %q, want %q", got, want)
	}
}

func TestBuffer_UnicodeRunes(t *testing.T) {
	b := newBuffer("héllo")
	// 'é' is a single rune; the e-acute sits at col 1.
	col := b.insertRune(0, 2, '!')
	if col != 3 {
		t.Errorf("col after insert: got %d, want 3", col)
	}
	if got := b.lines()[0]; got != "hé!llo" {
		t.Errorf("line: got %q, want hé!llo", got)
	}
}

func TestCursor_MoveLeftWrapsToPrevLine(t *testing.T) {
	b := newBuffer("ab\ncd")
	p := moveLeft(b, position{1, 0})
	if p.line != 0 || p.col != 2 {
		t.Errorf("pos: got %v, want (0,2)", p)
	}
}

func TestCursor_MoveRightWrapsToNextLine(t *testing.T) {
	b := newBuffer("ab\ncd")
	p := moveRight(b, position{0, 2})
	if p.line != 1 || p.col != 0 {
		t.Errorf("pos: got %v, want (1,0)", p)
	}
}

func TestCursor_MoveUpClampsCol(t *testing.T) {
	b := newBuffer("ab\nlonger line")
	p := moveUp(b, position{1, 10})
	if p.line != 0 || p.col != 2 {
		t.Errorf("pos: got %v, want (0,2)", p)
	}
}

func TestCursor_WordRightSkipsWhitespace(t *testing.T) {
	b := newBuffer("aaa  bbb")
	p := moveWordRight(b, position{0, 0})
	// "aaa" → end of word at col 3, then keep moving to next word start.
	// Our implementation moves to the END of the next word; so from
	// the middle of aaa we land at 3 (end of "aaa"), from 3 we land
	// at end of "bbb" = 8.
	if p.line != 0 || p.col != 3 {
		t.Errorf("pos: got %v, want (0,3)", p)
	}
	p = moveWordRight(b, p)
	if p.line != 0 || p.col != 8 {
		t.Errorf("pos2: got %v, want (0,8)", p)
	}
}

func TestCursor_WordLeftSkipsWhitespace(t *testing.T) {
	b := newBuffer("aaa  bbb")
	p := moveWordLeft(b, position{0, 8})
	if p.line != 0 || p.col != 5 {
		t.Errorf("pos: got %v, want (0,5)", p)
	}
}

func TestCursor_DocStartEnd(t *testing.T) {
	b := newBuffer("a\nbb\nccc")
	if p := moveDocStart(b, position{2, 2}); p != (position{0, 0}) {
		t.Errorf("docStart: got %v", p)
	}
	if p := moveDocEnd(b, position{0, 0}); p != (position{2, 3}) {
		t.Errorf("docEnd: got %v", p)
	}
}

func TestSelection_RngIsOrdered(t *testing.T) {
	s := &selection{anchor: position{3, 2}, head: position{1, 0}}
	a, b := s.rng()
	if a != (position{1, 0}) || b != (position{3, 2}) {
		t.Errorf("rng: got (%v,%v)", a, b)
	}
}

func TestSelection_ActiveReportsCollapsedAsFalse(t *testing.T) {
	if (&selection{anchor: position{1, 1}, head: position{1, 1}}).active() {
		t.Errorf("collapsed selection should not be active")
	}
	if !(&selection{anchor: position{0, 0}, head: position{0, 1}}).active() {
		t.Errorf("non-collapsed selection should be active")
	}
}
