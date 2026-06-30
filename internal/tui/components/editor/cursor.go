// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import "unicode"

// position is a (line, col) coordinate in the buffer. col is measured
// in runes, not bytes, to keep multibyte handling correct.
type position struct {
	line int
	col  int
}

// selection holds an active text selection. anchor is the fixed end
// (where the user started selecting); head is the moving end that
// follows the cursor as the user presses Shift+arrow. The two are
// equal exactly when the selection is collapsed, in which case the
// editor stores nil instead of a *selection.
type selection struct {
	anchor position
	head   position
}

// active reports whether s describes a non-empty selection.
func (s *selection) active() bool {
	if s == nil {
		return false
	}
	return s.anchor != s.head
}

// rng returns the selection bounds in document order.
func (s *selection) rng() (position, position) {
	if s == nil {
		return position{}, position{}
	}
	return normaliseRange(s.anchor, s.head)
}

// --- cursor movement helpers ---
//
// Each helper returns the new position. They never touch the buffer
// directly; the editor decides whether to commit the move and what
// to do with the selection (extend or clear) based on whether the
// key was Shift+something or not.

// moveLeft moves one rune to the left. At col 0, wraps to the end of
// the previous line. At document start, stays put.
func moveLeft(b *buffer, p position) position {
	if p.col > 0 {
		return position{line: p.line, col: p.col - 1}
	}
	if p.line == 0 {
		return p
	}
	return position{line: p.line - 1, col: b.lineLen(p.line - 1)}
}

// moveRight is the mirror of moveLeft.
func moveRight(b *buffer, p position) position {
	if p.col < b.lineLen(p.line) {
		return position{line: p.line, col: p.col + 1}
	}
	if p.line == b.lineCount()-1 {
		return p
	}
	return position{line: p.line + 1, col: 0}
}

// moveUp moves the cursor one line up, keeping the column when the
// upper line is long enough or clamping to its end otherwise. At the
// first line, stays put.
func moveUp(b *buffer, p position) position {
	if p.line == 0 {
		return p
	}
	col := p.col
	if max := b.lineLen(p.line - 1); col > max {
		col = max
	}
	return position{line: p.line - 1, col: col}
}

// moveDown is the mirror of moveUp.
func moveDown(b *buffer, p position) position {
	if p.line >= b.lineCount()-1 {
		return p
	}
	col := p.col
	if max := b.lineLen(p.line + 1); col > max {
		col = max
	}
	return position{line: p.line + 1, col: col}
}

// moveHome jumps to column 0 of the current line.
func moveHome(_ *buffer, p position) position {
	return position{line: p.line, col: 0}
}

// moveEnd jumps to the end-of-line of the current line.
func moveEnd(b *buffer, p position) position {
	return position{line: p.line, col: b.lineLen(p.line)}
}

// moveDocStart and moveDocEnd are Ctrl+Home / Ctrl+End.
func moveDocStart(*buffer, position) position {
	return position{}
}
func moveDocEnd(b *buffer, _ position) position {
	last := b.lineCount() - 1
	return position{line: last, col: b.lineLen(last)}
}

// movePageUp / movePageDown jump by n lines, where n is the editor's
// viewport height (PgUp/PgDn keys feel right when they move a screen
// at a time). The column is clamped on the destination line.
func movePageUp(b *buffer, p position, n int) position {
	target := p.line - n
	if target < 0 {
		target = 0
	}
	col := p.col
	if max := b.lineLen(target); col > max {
		col = max
	}
	return position{line: target, col: col}
}
func movePageDown(b *buffer, p position, n int) position {
	target := p.line + n
	if last := b.lineCount() - 1; target > last {
		target = last
	}
	col := p.col
	if max := b.lineLen(target); col > max {
		col = max
	}
	return position{line: target, col: col}
}

// moveWordLeft / moveWordRight jump by whole words, where "word" is a
// run of letters or digits separated by anything else. The exact
// definition matches what most terminal editors do (good enough for
// YAML; we are not building a code editor with language-specific
// word boundaries).
func moveWordLeft(b *buffer, p position) position {
	// Walk left until we leave whitespace, then walk through the
	// word until we hit a non-word rune.
	for {
		if p.col == 0 {
			if p.line == 0 {
				return p
			}
			p = position{line: p.line - 1, col: b.lineLen(p.line - 1)}
			continue
		}
		// Look at the rune immediately to the left of the cursor.
		r := b.line(p.line)[p.col-1]
		if isWordRune(r) {
			break
		}
		p.col--
	}
	for p.col > 0 {
		r := b.line(p.line)[p.col-1]
		if !isWordRune(r) {
			break
		}
		p.col--
	}
	return p
}
func moveWordRight(b *buffer, p position) position {
	for {
		max := b.lineLen(p.line)
		if p.col >= max {
			if p.line == b.lineCount()-1 {
				return p
			}
			p = position{line: p.line + 1, col: 0}
			continue
		}
		r := b.line(p.line)[p.col]
		if isWordRune(r) {
			break
		}
		p.col++
	}
	for {
		max := b.lineLen(p.line)
		if p.col >= max {
			break
		}
		r := b.line(p.line)[p.col]
		if !isWordRune(r) {
			break
		}
		p.col++
	}
	return p
}

// isWordRune is the word-membership predicate for moveWordLeft/Right.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// clampPosition snaps p to the valid range of b. Used after edits
// that may have shortened the document under the cursor.
func clampPosition(b *buffer, p position) position {
	if p.line < 0 {
		p.line = 0
	}
	if max := b.lineCount() - 1; p.line > max {
		p.line = max
	}
	if p.col < 0 {
		p.col = 0
	}
	if max := b.lineLen(p.line); p.col > max {
		p.col = max
	}
	return p
}
