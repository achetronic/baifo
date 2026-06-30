// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// search holds the state of the find-in-buffer overlay. Lives on the
// Model; nil means the search bar is closed. While non-nil, the
// editor still accepts editing keys but routes Enter, n, N, Esc and
// printable characters to the search handler so the user can refine
// the query.
//
// We keep the implementation deliberately minimal: case-insensitive
// substring match, no regex, no replace. The goal is "I can find the
// line I'm looking for in a 1000-line YAML" \u2014 anything fancier (vim-
// style search, structural search) can be a follow-up.
type search struct {
	// query is the substring we look for. Updated rune-by-rune as
	// the user types in the search bar.
	query string

	// matches holds the positions of every match in the buffer, in
	// document order. Rebuilt every time query changes so n/N can
	// step through deterministically.
	matches []position

	// current is the index into matches that the cursor is currently
	// sitting on. -1 means "no match selected"; otherwise it's a
	// valid index when len(matches) > 0.
	current int
}

// active reports whether the search overlay is up.
func (s *search) active() bool { return s != nil && s.query != "" }

// startSearch opens the search overlay with an empty query. Cursor
// stays where it was so the first Enter jumps to the first match
// from there forward.
func (m *Model) startSearch() {
	m.searchSt = &search{current: -1}
}

// closeSearch dismisses the overlay. The cursor stays on whatever
// match the user last landed on \u2014 closing the bar should not snap
// the user back to where they started.
func (m *Model) closeSearch() {
	m.searchSt = nil
}

// updateQuery sets the search query and recomputes matches. We
// rebuild from scratch on every keystroke; for the buffer sizes
// baifo edits (hundreds of lines tops) this is cheap and avoids
// incremental-index bookkeeping.
func (m *Model) updateQuery(q string) {
	if m.searchSt == nil {
		return
	}
	m.searchSt.query = q
	m.recomputeMatches()
	if len(m.searchSt.matches) > 0 {
		m.searchSt.current = 0
		m.cursor = m.searchSt.matches[0]
		m.syncViewport()
	} else {
		m.searchSt.current = -1
	}
}

// recomputeMatches walks every line and records the rune positions
// where query starts. Match positions are stored as the cursor
// (line, col) of the FIRST rune of each match.
func (m *Model) recomputeMatches() {
	if m.searchSt == nil || m.searchSt.query == "" {
		if m.searchSt != nil {
			m.searchSt.matches = nil
		}
		return
	}
	needle := strings.ToLower(m.searchSt.query)
	var out []position
	for i := 0; i < m.buf.lineCount(); i++ {
		line := strings.ToLower(string(m.buf.line(i)))
		from := 0
		for {
			idx := strings.Index(line[from:], needle)
			if idx < 0 {
				break
			}
			// idx is a byte offset inside line[from:]. We need a
			// rune offset against the original (lowercased) line.
			// strings.ToLower preserves byte length for ASCII; for
			// non-ASCII (Unicode case folding) this is approximate
			// and may shift matches by a column or two. Acceptable
			// for a YAML/markdown editor.
			byteOffset := from + idx
			runeCol := utf8RuneCount(line[:byteOffset])
			out = append(out, position{line: i, col: runeCol})
			from = byteOffset + len(needle)
		}
	}
	m.searchSt.matches = out
}

// nextMatch moves to the next match in document order, wrapping to
// the beginning when past the last one.
func (m *Model) nextMatch() {
	if m.searchSt == nil || len(m.searchSt.matches) == 0 {
		return
	}
	m.searchSt.current = (m.searchSt.current + 1) % len(m.searchSt.matches)
	m.cursor = m.searchSt.matches[m.searchSt.current]
	m.syncViewport()
}

// prevMatch is the mirror of nextMatch.
func (m *Model) prevMatch() {
	if m.searchSt == nil || len(m.searchSt.matches) == 0 {
		return
	}
	n := len(m.searchSt.matches)
	m.searchSt.current = (m.searchSt.current - 1 + n) % n
	m.cursor = m.searchSt.matches[m.searchSt.current]
	m.syncViewport()
}

// renderSearchBar paints the bottom-of-screen search input. Width
// matches the editor's width so it integrates visually with the
// header/footer chrome.
func (m Model) renderSearchBar() string {
	if m.searchSt == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 40
	}
	counter := ""
	if len(m.searchSt.matches) > 0 {
		counter = " " + matchCounter(m.searchSt.current, len(m.searchSt.matches))
	} else if m.searchSt.query != "" {
		counter = " no matches"
	}
	hint := " [enter/n] next  \u00b7  [N] prev  \u00b7  [esc] close"
	prompt := "find: " + m.searchSt.query + "\u2588"
	body := prompt + counter + "  " + hint
	if pad := width - lipgloss.Width(body) - 2; pad > 0 {
		body = body + strings.Repeat(" ", pad)
	}
	return m.styles.Header.Width(width).Render(" " + body)
}

// matchCounter renders the "[i/N]" indicator next to the query.
func matchCounter(current, total int) string {
	if total == 0 {
		return ""
	}
	// current is 0-based; users expect 1-based for display.
	return "[" + itoa(current+1) + "/" + itoa(total) + "]"
}

// itoa avoids strconv just to keep the imports list small.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// utf8RuneCount counts runes in s. Tiny helper to keep recompute
// readable without importing unicode/utf8 across multiple files.
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// searchMatchAt reports whether a rune index on a line falls inside
// any match. Used by render.go to colour matches; the current match
// is rendered in a stronger style than the rest.
func (m *Model) searchMatchAt(lineIdx, col int) (matched, isCurrent bool) {
	if m.searchSt == nil || len(m.searchSt.matches) == 0 || m.searchSt.query == "" {
		return false, false
	}
	width := utf8RuneCount(m.searchSt.query)
	for i, p := range m.searchSt.matches {
		if p.line != lineIdx {
			continue
		}
		if col >= p.col && col < p.col+width {
			return true, i == m.searchSt.current
		}
	}
	return false, false
}
