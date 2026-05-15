// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package editor

import "strings"

// buffer is the rune-based text store backing the editor. A buffer is
// always non-empty: even an empty document has a single empty line so
// that the cursor has somewhere to live.
//
// We use [][]rune instead of [][]byte because cursor positions are
// expressed in glyph columns; with []byte every multibyte character
// would force the caller to convert ranges, which is exactly the
// bug surface we want to avoid.
//
// buffer methods do NOT touch the cursor or the selection; those are
// the editor's responsibility. The buffer just provides primitives.
type buffer struct {
	rows [][]rune
}

// newBuffer parses s into a buffer. CRLF is normalised to LF so users
// pasting from Windows do not end up with stray ^M markers in their
// YAML; LFs in the buffer always mean "newline".
func newBuffer(s string) *buffer {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return &buffer{rows: [][]rune{{}}}
	}
	parts := strings.Split(s, "\n")
	rows := make([][]rune, len(parts))
	for i, p := range parts {
		rows[i] = []rune(p)
	}
	return &buffer{rows: rows}
}

// lines returns one string per row. The slice is freshly allocated so
// the caller can mutate it without corrupting the buffer.
func (b *buffer) lines() []string {
	out := make([]string, len(b.rows))
	for i, r := range b.rows {
		out[i] = string(r)
	}
	return out
}

// lineCount returns the number of rows. Always >= 1.
func (b *buffer) lineCount() int { return len(b.rows) }

// line returns the runes of row i. The returned slice aliases the
// buffer; do not mutate.
func (b *buffer) line(i int) []rune {
	if i < 0 || i >= len(b.rows) {
		return nil
	}
	return b.rows[i]
}

// lineLen returns the rune count of row i, or 0 if out of range.
func (b *buffer) lineLen(i int) int {
	if i < 0 || i >= len(b.rows) {
		return 0
	}
	return len(b.rows[i])
}

// insertRune inserts r at (row, col). col is clamped to [0, lineLen].
// Returns the new column position (always col+1 except when the input
// is rejected).
func (b *buffer) insertRune(row, col int, r rune) int {
	if row < 0 || row >= len(b.rows) {
		return col
	}
	line := b.rows[row]
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	// Build a new slice instead of in-place: simpler, safer for tests.
	nl := make([]rune, len(line)+1)
	copy(nl, line[:col])
	nl[col] = r
	copy(nl[col+1:], line[col:])
	b.rows[row] = nl
	return col + 1
}

// insertString inserts a multi-rune string at (row, col), splitting
// on '\n' so pastes that contain newlines produce new rows. Returns
// the final (row, col) after the insert.
func (b *buffer) insertString(row, col int, s string) (int, int) {
	if s == "" {
		return row, col
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	parts := strings.Split(s, "\n")
	if len(parts) == 1 {
		for _, r := range parts[0] {
			col = b.insertRune(row, col, r)
		}
		return row, col
	}
	line := b.rows[row]
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	head := append([]rune{}, line[:col]...)
	tail := append([]rune{}, line[col:]...)

	first := append(head, []rune(parts[0])...)
	last := append([]rune(parts[len(parts)-1]), tail...)

	middle := make([][]rune, len(parts)-2)
	for i := 1; i < len(parts)-1; i++ {
		middle[i-1] = []rune(parts[i])
	}

	newRows := make([][]rune, 0, len(b.rows)+len(parts)-1)
	newRows = append(newRows, b.rows[:row]...)
	newRows = append(newRows, first)
	newRows = append(newRows, middle...)
	newRows = append(newRows, last)
	newRows = append(newRows, b.rows[row+1:]...)
	b.rows = newRows

	endRow := row + len(parts) - 1
	endCol := len([]rune(parts[len(parts)-1]))
	return endRow, endCol
}

// deleteRune removes the rune to the LEFT of (row, col) — the
// backspace operation. If col == 0 and row > 0, the current row is
// merged with the previous one. Returns the new (row, col) after the
// delete.
func (b *buffer) deleteRune(row, col int) (int, int) {
	if row < 0 || row >= len(b.rows) {
		return row, col
	}
	if col > 0 {
		line := b.rows[row]
		if col > len(line) {
			col = len(line)
		}
		nl := make([]rune, 0, len(line)-1)
		nl = append(nl, line[:col-1]...)
		nl = append(nl, line[col:]...)
		b.rows[row] = nl
		return row, col - 1
	}
	if row == 0 {
		return row, col
	}
	prev := b.rows[row-1]
	merged := append(append([]rune{}, prev...), b.rows[row]...)
	newRows := make([][]rune, 0, len(b.rows)-1)
	newRows = append(newRows, b.rows[:row-1]...)
	newRows = append(newRows, merged)
	newRows = append(newRows, b.rows[row+1:]...)
	b.rows = newRows
	return row - 1, len(prev)
}

// deleteForward removes the rune at (row, col) — the Delete key
// operation. If at end of line, joins with the next row. Returns
// (row, col) unchanged: forward delete never moves the cursor.
func (b *buffer) deleteForward(row, col int) (int, int) {
	if row < 0 || row >= len(b.rows) {
		return row, col
	}
	line := b.rows[row]
	if col < 0 {
		col = 0
	}
	if col < len(line) {
		nl := make([]rune, 0, len(line)-1)
		nl = append(nl, line[:col]...)
		nl = append(nl, line[col+1:]...)
		b.rows[row] = nl
		return row, col
	}
	if row == len(b.rows)-1 {
		return row, col
	}
	merged := append(append([]rune{}, line...), b.rows[row+1]...)
	newRows := make([][]rune, 0, len(b.rows)-1)
	newRows = append(newRows, b.rows[:row]...)
	newRows = append(newRows, merged)
	newRows = append(newRows, b.rows[row+2:]...)
	b.rows = newRows
	return row, col
}

// splitLine breaks row at col, pushing the right half down as a new
// row. Returns the new (row, col), which is always (row+1, 0).
func (b *buffer) splitLine(row, col int) (int, int) {
	if row < 0 || row >= len(b.rows) {
		return row, col
	}
	line := b.rows[row]
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	left := append([]rune{}, line[:col]...)
	right := append([]rune{}, line[col:]...)
	newRows := make([][]rune, 0, len(b.rows)+1)
	newRows = append(newRows, b.rows[:row]...)
	newRows = append(newRows, left, right)
	newRows = append(newRows, b.rows[row+1:]...)
	b.rows = newRows
	return row + 1, 0
}

// deleteRange removes the runes in [start, end) (positions inclusive
// of start, exclusive of end). The text between is dropped and lines
// across the range are merged. Returns the new (row, col), which is
// always start.
func (b *buffer) deleteRange(start, end position) position {
	start, end = normaliseRange(start, end)
	if start == end {
		return start
	}
	if start.line == end.line {
		line := b.rows[start.line]
		nl := make([]rune, 0, len(line)-(end.col-start.col))
		nl = append(nl, line[:start.col]...)
		nl = append(nl, line[end.col:]...)
		b.rows[start.line] = nl
		return start
	}
	startLine := b.rows[start.line]
	endLine := b.rows[end.line]
	if start.col > len(startLine) {
		start.col = len(startLine)
	}
	if end.col > len(endLine) {
		end.col = len(endLine)
	}
	merged := make([]rune, 0, start.col+(len(endLine)-end.col))
	merged = append(merged, startLine[:start.col]...)
	merged = append(merged, endLine[end.col:]...)

	newRows := make([][]rune, 0, len(b.rows)-(end.line-start.line))
	newRows = append(newRows, b.rows[:start.line]...)
	newRows = append(newRows, merged)
	newRows = append(newRows, b.rows[end.line+1:]...)
	b.rows = newRows
	return start
}

// textInRange returns the substring inside [start, end). Used for
// copy and cut.
func (b *buffer) textInRange(start, end position) string {
	start, end = normaliseRange(start, end)
	if start == end {
		return ""
	}
	if start.line == end.line {
		line := b.rows[start.line]
		if start.col > len(line) {
			start.col = len(line)
		}
		if end.col > len(line) {
			end.col = len(line)
		}
		return string(line[start.col:end.col])
	}
	var sb strings.Builder
	first := b.rows[start.line]
	if start.col > len(first) {
		start.col = len(first)
	}
	sb.WriteString(string(first[start.col:]))
	sb.WriteByte('\n')
	for i := start.line + 1; i < end.line; i++ {
		sb.WriteString(string(b.rows[i]))
		sb.WriteByte('\n')
	}
	last := b.rows[end.line]
	if end.col > len(last) {
		end.col = len(last)
	}
	sb.WriteString(string(last[:end.col]))
	return sb.String()
}

// normaliseRange returns (a, b) ordered so that a precedes b in
// document order.
func normaliseRange(a, b position) (position, position) {
	if a.line < b.line || (a.line == b.line && a.col <= b.col) {
		return a, b
	}
	return b, a
}

// deleteLine removes row i entirely (including its newline). A buffer
// is never left empty: deleting the last remaining row resets it to a
// single empty line. No-op when i is out of range.
func (b *buffer) deleteLine(i int) {
	if i < 0 || i >= len(b.rows) {
		return
	}
	if len(b.rows) == 1 {
		b.rows[0] = []rune{}
		return
	}
	b.rows = append(b.rows[:i], b.rows[i+1:]...)
}

// duplicateLine inserts a copy of row i directly below it. No-op when
// i is out of range.
func (b *buffer) duplicateLine(i int) {
	if i < 0 || i >= len(b.rows) {
		return
	}
	dup := append([]rune{}, b.rows[i]...)
	newRows := make([][]rune, 0, len(b.rows)+1)
	newRows = append(newRows, b.rows[:i+1]...)
	newRows = append(newRows, dup)
	newRows = append(newRows, b.rows[i+1:]...)
	b.rows = newRows
}

// swapLines exchanges rows i and j. No-op when either index is out of
// range. Used by the move-line-up/down actions.
func (b *buffer) swapLines(i, j int) {
	if i < 0 || i >= len(b.rows) || j < 0 || j >= len(b.rows) {
		return
	}
	b.rows[i], b.rows[j] = b.rows[j], b.rows[i]
}
