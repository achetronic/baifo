// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package editor

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// completer is the auto-complete pop-up overlay. It is opened when
// the user types a string that matches one of the registered Triggers,
// shows the list of Completions returned by the provider, and inserts
// the selected one when the user presses Enter.
//
// Lifecycle: nil when closed. Construct via openCompleter; close by
// setting m.completer = nil. While non-nil, completer steals key
// events from the editor so the user can navigate the list.
type completer struct {
	// styles is the resolved editor palette, copied at construction
	// time so the completer renders consistently even if the editor
	// styles are swapped between frames (which never happens today,
	// but avoids a subtle trap).
	styles Styles

	// trigger is the substring that opened this completer (e.g.
	// "${secret:"). We remember it so cancellation can restore the
	// editor's exact pre-trigger state if needed; currently unused
	// but kept as a small piece of context for future polish.
	trigger string

	// prefix is what the user has typed AFTER the trigger so far.
	// The completer filters its items by this prefix on each
	// keystroke so the list narrows naturally.
	prefix string

	// items is the full list of suggestions from the provider.
	// filtered is the subset that matches the current prefix.
	items    []Completion
	filtered []Completion

	// selected is the index inside filtered. Bounded by Update so
	// it always points at a valid row (or -1 when filtered empty).
	selected int

	// triggerStart is the buffer position of the FIRST character of
	// the trigger string. On Insert we delete from triggerStart to
	// the current cursor and replace with the chosen completion's
	// Insert text. That way the trigger itself is consumed cleanly.
	triggerStart position
}

// openCompleter looks at the buffer to the left of the cursor and
// returns a *completer if one of the registered Triggers matches, or
// nil otherwise. The caller should set m.completer = openCompleter(m)
// after any keystroke that might have completed a trigger.
//
// Matching rule: for each registered trigger string, we check whether
// the buffer ends with it just before the cursor. The longest match
// wins so triggers can have overlapping prefixes safely.
func openCompleter(m *Model) *completer {
	if len(m.triggers) == 0 {
		return nil
	}
	// Take the current line up to the cursor.
	line := m.buf.line(m.cursor.line)
	if line == nil {
		return nil
	}
	upto := string(line[:m.cursor.col])

	var bestTrig string
	var bestProvider CompletionProvider
	for trig, prov := range m.triggers {
		if strings.HasSuffix(upto, trig) && len(trig) > len(bestTrig) {
			bestTrig = trig
			bestProvider = prov
		}
	}
	if bestTrig == "" {
		return nil
	}

	items := bestProvider("", CompletionContext{
		Line:  m.cursor.line,
		Col:   m.cursor.col,
		Lines: m.buf.lines(),
	})
	if len(items) == 0 {
		return nil
	}

	return &completer{
		styles:       m.styles,
		trigger:      bestTrig,
		items:        items,
		filtered:     items,
		selected:     0,
		triggerStart: position{line: m.cursor.line, col: m.cursor.col - len([]rune(bestTrig))},
	}
}

// filter rebuilds c.filtered from c.items using c.prefix as a
// case-insensitive substring match. selected is reset to 0 (or -1
// for empty) so the highlighted row stays valid.
func (c *completer) filter() {
	if c.prefix == "" {
		c.filtered = c.items
	} else {
		needle := strings.ToLower(c.prefix)
		// Build a fresh slice so we never share storage with c.items.
		// Reusing c.filtered[:0] aliases the backing array and would
		// silently overwrite c.items, breaking incremental filtering.
		out := make([]Completion, 0, len(c.items))
		for _, it := range c.items {
			if strings.Contains(strings.ToLower(it.View), needle) {
				out = append(out, it)
			}
		}
		c.filtered = out
	}
	if len(c.filtered) == 0 {
		c.selected = -1
	} else if c.selected >= len(c.filtered) {
		c.selected = len(c.filtered) - 1
	} else if c.selected < 0 {
		c.selected = 0
	}
}

// handleKey returns (keep, cmd, insert):
//   - keep:   true to keep the completer open, false to close it.
//   - cmd:    optional tea.Cmd the editor should run (we never emit
//     one today, but the signature keeps the door open).
//   - insert: when non-empty, the editor should replace the trigger
//     with this text. Empty means "no insertion this round".
//
// Returning keep=false with a non-empty insert is the "user picked
// an item with Enter" path; keep=false with empty insert is "Esc /
// cancel". The editor decides what to do with each combination.
func (c *completer) handleKey(msg tea.KeyMsg) (keep bool, cmd tea.Cmd, insert string) {
	switch msg.String() {
	case "up":
		if len(c.filtered) > 0 {
			c.selected = (c.selected - 1 + len(c.filtered)) % len(c.filtered)
		}
		return true, nil, ""
	case "down":
		if len(c.filtered) > 0 {
			c.selected = (c.selected + 1) % len(c.filtered)
		}
		return true, nil, ""
	case "enter", "tab":
		if c.selected >= 0 && c.selected < len(c.filtered) {
			return false, nil, c.filtered[c.selected].Insert
		}
		return false, nil, ""
	case "esc":
		return false, nil, ""
	case "backspace":
		if c.prefix == "" {
			// Backspace at empty prefix closes the completer; the
			// editor will treat the keystroke as a normal backspace
			// on the next dispatch.
			return false, nil, ""
		}
		c.prefix = c.prefix[:len(c.prefix)-1]
		c.filter()
		return true, nil, ""
	}
	// Printable rune: append to prefix and re-filter.
	if text := msg.Key().Text; text != "" {
		c.prefix += text
		c.filter()
		return true, nil, ""
	}
	// Anything else (motion keys, ctrl+something, ...) just keeps
	// the completer open without filtering.
	return true, nil, ""
}

// view renders the completer as a bordered floating panel. The
// host editor positions it via overlayCompleterNearCursor; this
// method only produces the box body.
//
// Visual idiom (consistent with the rest of baifo's overlays):
//   - Rounded border in a dim accent tone.
//   - Width tracks the longest visible row, capped so a very long
//     model id doesn't blow past the editor's gutter.
//   - Selected row carries a "▍" accent rail + lifted background;
//     unselected rows sit on the panel's own background. No
//     Reverse(true), which looked broken on some terminals when
//     the panel and editor backgrounds disagreed.
//   - Footer with "n / N" so the user knows there's more below
//     when the list scrolls.
func (c *completer) view() string {
	// Width: longest visible row + side padding, clamped to the
	// completerMinWidth / completerMaxWidth window so the popup
	// stays a comfortable reading size regardless of catalogue
	// breadth.
	width := completerMinWidth
	for _, it := range c.filtered {
		if w := lipgloss.Width(it.View) + 4; w > width {
			width = w
		}
	}
	if width > completerMaxWidth {
		width = completerMaxWidth
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c.styles.CompleterBorder).
		Background(c.styles.CompleterBg).
		Padding(0, 1).
		Width(width)

	contentWidth := width - 2 // padding 1 each side

	if len(c.filtered) == 0 {
		empty := lipgloss.NewStyle().
			Foreground(c.styles.CompleterDim).
			Background(c.styles.CompleterBg).
			Italic(true).
			Width(contentWidth).
			Render("no matches")
		return box.Render(empty)
	}

	maxRows := completerMaxRows
	if len(c.filtered) < maxRows {
		maxRows = len(c.filtered)
	}
	// Slide the visible window so the selected row is always in
	// view. We only need to scroll once selected falls off the
	// bottom of the initial window.
	start := 0
	if c.selected >= maxRows {
		start = c.selected - maxRows + 1
	}
	end := start + maxRows
	if end > len(c.filtered) {
		end = len(c.filtered)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(renderCompleterRow(c.filtered[i].View, i == c.selected, contentWidth, c.styles))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}

	// Footer with "n / N" position. Only shown when the list
	// actually scrolls; otherwise the bare list reads cleaner.
	if len(c.filtered) > maxRows {
		footer := lipgloss.NewStyle().
			Foreground(c.styles.CompleterDim).
			Background(c.styles.CompleterBg).
			Italic(true).
			Width(contentWidth).
			Align(lipgloss.Right).
			Render(fmt.Sprintf("%d / %d", c.selected+1, len(c.filtered)))
		b.WriteByte('\n')
		b.WriteString(footer)
	}

	return box.Render(b.String())
}

// renderCompleterRow paints one entry inside the popup at exactly
// the given width. Selected rows get the accent rail and lifted
// background; the rest sit on the panel background unchanged.
func renderCompleterRow(text string, selected bool, width int, st Styles) string {
	bg := st.CompleterBg
	if selected {
		bg = st.CompleterSelBg
	}
	rowStyle := lipgloss.NewStyle().
		Background(bg).
		Foreground(st.CompleterFg).
		Width(width)
	rail := lipgloss.NewStyle().Background(bg).Render("  ")
	if selected {
		rail = lipgloss.NewStyle().
			Foreground(st.CompleterAccent).
			Background(bg).
			Bold(true).
			Render("▍ ")
		rowStyle = rowStyle.Bold(true)
	}
	// Manual truncation so the row width matches `width` even
	// for very long entries (a long model id with the friendly
	// name appended is the common case).
	body := truncateRunes(text, width-2)
	return rail + rowStyle.Width(width-2).Render(body)
}

// truncateRunes shortens s to at most n runes, replacing the
// tail with an ellipsis when something was actually cut. Rune-
// aware so multibyte glyphs (CJK, emoji) survive at the boundary.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(rs[:n-1]) + "…"
}

// completer popup sizing. Kept as package constants so the look stays
// homogeneous across triggers.
const (
	completerMinWidth = 28
	completerMaxWidth = 80
	completerMaxRows  = 8
)
