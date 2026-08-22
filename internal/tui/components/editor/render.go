// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// View implements tea.Model. The layout is:
//
//	┌─ {title} ─────────────────────────── {modified-marker} ─┐
//	│  1  first line of the buffer                            │
//	│  2  second line, with the cursor here →█                │
//	│ ...                                                     │
//	│     (rest scrolls in the viewport)                      │
//	│ ⚠ validation error 1                                   │  (optional)
//	│ ⚠ validation error 2                                   │
//	└─ ctrl+s save · esc cancel · ? help ─────────────────────┘
//
// The output is always EXACTLY m.height lines tall. Without this
// guarantee a paste that grows the buffer beyond the viewport can
// push the footer below the visible area on some terminals; we pad
// the body with empty lines to keep the chrome anchored.
func (m Model) View() string {
	m.vp.SetContentLines(m.renderLines())

	header := m.renderHeader()
	body := m.vp.View()
	footer := m.renderFooter()
	errBar := m.renderErrors()

	// Compute how many lines body actually occupies (the viewport may
	// return fewer than its configured height when content is short).
	// Pad to fill so header/errBar/footer stay glued to the bottom.
	wantBodyHeight := m.vp.Height()
	if wantBodyHeight < 1 {
		wantBodyHeight = 1
	}
	body = padToHeight(body, wantBodyHeight)

	parts := []string{header, body}
	if errBar != "" {
		parts = append(parts, errBar)
	}
	parts = append(parts, footer)

	if bar := m.renderSearchBar(); bar != "" {
		parts = append(parts, bar)
	}

	out := strings.Join(parts, "\n")

	if m.helpOpen {
		out = overlayModal(out, m.width, m.height, helpOverlay(m.styles))
	} else if m.confirmDiscard {
		out = overlayModal(out, m.width, m.height, discardPrompt(m.styles))
	} else if m.confirmSave {
		out = overlayModal(out, m.width, m.height, savePrompt(m.styles))
	} else if m.completer != nil {
		out = overlayCompleterNearCursor(out, m, m.completer.view())
	}
	return out
}

// padToHeight returns s with empty lines appended so it spans exactly
// n lines. Truncates if s already has more. Cheap to compute and the
// only place we touch the layout when content moves under the user.
func padToHeight(s string, n int) string {
	cur := strings.Count(s, "\n") + 1
	if s == "" {
		cur = 0
	}
	if cur == n {
		return s
	}
	if cur > n {
		lines := strings.Split(s, "\n")
		return strings.Join(lines[:n], "\n")
	}
	pad := strings.Repeat("\n", n-cur)
	return s + pad
}

// renderHeader paints the title bar. The right edge holds a dot when
// the buffer is dirty so users notice unsaved changes at a glance.
func (m Model) renderHeader() string {
	title := m.title
	mark := "  "
	if m.dirty {
		mark = " *"
	}
	width := m.width
	if width <= 0 {
		width = 40
	}
	// Pad the title to the editor width, then style the whole line.
	avail := width - lipgloss.Width(title) - lipgloss.Width(mark) - 2
	if avail < 0 {
		avail = 0
	}
	line := " " + title + strings.Repeat(" ", avail) + mark + " "
	return m.styles.Header.Width(width).Render(line)
}

// renderFooter paints the hotkey hints at the bottom. Kept short on
// purpose: full keymap lives behind '?' so terminals as narrow as
// 60 cols still see what matters.
func (m Model) renderFooter() string {
	hints := "[ctrl+s] save · [esc] quit · [ctrl+f] find · [ctrl+z] undo · [?] help"
	width := m.width
	if width <= 0 {
		width = 40
	}
	if lipgloss.Width(hints)+2 > width {
		hints = "[ctrl+s] save · [esc] quit · [?] help"
	}
	if pad := width - lipgloss.Width(hints) - 2; pad > 0 {
		hints = hints + strings.Repeat(" ", pad)
	}
	return m.styles.Header.Width(width).Render(" " + hints)
}

// renderErrors paints one line per validation error, or "" when
// there are no errors.
func (m Model) renderErrors() string {
	if len(m.validationErrors) == 0 {
		return ""
	}
	var b strings.Builder
	for i, err := range m.validationErrors {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.styles.ErrorLine.Render("! " + err.Error()))
	}
	return b.String()
}

// renderLines returns one styled string per buffer row, with line
// numbers in the gutter, selection highlighting and the virtual
// cursor. The slice is what we hand to viewport.SetContentLines.
func (m Model) renderLines() []string {
	lines := m.buf.lines()
	digits := len(strconv.Itoa(len(lines)))
	out := make([]string, len(lines))

	selStart, selEnd := position{}, position{}
	hasSel := m.sel.active()
	if hasSel {
		selStart, selEnd = m.sel.rng()
	}

	for i, raw := range lines {
		gutter := m.styles.Gutter.Render(fmt.Sprintf("%*d", digits, i+1))

		// Render the line content as a sequence of segments, applying
		// the styler on plain regions and the selection/cursor style on
		// the parts they cover. This way highlighting and selection
		// compose correctly: selected text is uniformly inverted (so
		// the user can read it), unselected text keeps its YAML colours.
		body := m.renderLineContent(i, raw, hasSel, selStart, selEnd)

		out[i] = gutter + body
	}
	return out
}

// renderLineContent assembles the styled body for one line, merging
// the LineStyler spans with selection and cursor highlights. Cursor
// wins over selection wins over syntax highlight, so the user always
// sees their position and what they have selected.
func (m Model) renderLineContent(
	lineIdx int,
	raw string,
	hasSel bool,
	selStart, selEnd position,
) string {
	rs := []rune(raw)

	// Compute syntax spans once for the whole line. Spans are byte
	// ranges; we map them to rune indices on the fly because
	// selection/cursor positions are rune-based.
	var spans []StyledSpan
	if m.styler != nil {
		spans = m.styler(lineIdx, raw)
	}
	rune2byte := runeOffsets(raw)

	// For each rune index, find the style coming from the styler (or
	// nil if none). Linear scan once per rune is O(runes * spans),
	// fine for typical YAML lines.
	spanStyle := func(ri int) *lipgloss.Style {
		if ri >= len(rune2byte) {
			return nil
		}
		bi := rune2byte[ri]
		for i := range spans {
			if bi >= spans[i].From && bi < spans[i].To {
				return &spans[i].Style
			}
		}
		return nil
	}

	onCursorLine := m.focused && !m.confirmDiscard && lineIdx == m.cursor.line

	inSelection := func(ri int) bool {
		if !hasSel {
			return false
		}
		if lineIdx < selStart.line || lineIdx > selEnd.line {
			return false
		}
		from, to := 0, len(rs)
		if lineIdx == selStart.line {
			from = selStart.col
		}
		if lineIdx == selEnd.line {
			to = selEnd.col
		}
		return ri >= from && ri < to
	}

	isCursorPos := func(ri int) bool {
		return onCursorLine && ri == m.cursor.col
	}

	selStyle := m.styles.Selection
	cursorStyle := m.styles.Cursor
	matchStyle := m.styles.SearchMatch
	currentMatchStyle := m.styles.SearchCurrentMatch

	// Rune-by-rune render. We do NOT batch into segments here because
	// each rune might fall in a different span; the cost is one ANSI
	// reset per rune in the worst case (acceptable for the sizes we
	// target). If profiling shows this hurts, we can add batching
	// later by recognising consecutive runes with identical effective
	// style.
	var out strings.Builder
	n := len(rs)

	// Horizontal scroll: render only the visible slice of runes.
	// xOffset comes from the Model (kept in sync by syncViewport)
	// and represents the leftmost rune column on screen. width is
	// how many runes fit after the line-number gutter.
	start := m.xOffset
	if start < 0 {
		start = 0
	}
	width := m.contentWidth()
	end := n
	if width > 0 && start+width < n {
		end = start + width
	}
	if start > n {
		start = n
	}

	for i := start; i < end; i++ {
		ch := string(rs[i])
		matched, isCurrent := m.searchMatchAt(lineIdx, i)
		switch {
		case isCursorPos(i):
			out.WriteString(cursorStyle.Render(ch))
		case matched && isCurrent:
			out.WriteString(currentMatchStyle.Render(ch))
		case matched:
			out.WriteString(matchStyle.Render(ch))
		case inSelection(i):
			out.WriteString(selStyle.Render(ch))
		default:
			if s := spanStyle(i); s != nil {
				out.WriteString(s.Render(ch))
			} else {
				out.WriteString(ch)
			}
		}
	}

	// Cursor at or past end-of-line: render the block cursor only
	// when the cursor column falls inside the visible window.
	if onCursorLine && m.cursor.col >= n && m.cursor.col >= start && m.cursor.col-start < width {
		out.WriteString(cursorStyle.Render(" "))
	}
	return out.String()
}

// runeOffsets returns, for each rune index in s, the byte offset of
// its first byte. Length is len([]rune(s)). Used to map between the
// rune-indexed cursor/selection world and the byte-indexed span
// world that the styler returns.
func runeOffsets(s string) []int {
	if s == "" {
		return nil
	}
	out := make([]int, 0, len(s))
	for i := range s {
		out = append(out, i)
	}
	return out
}

// overlayCompleterNearCursor places the completion popup just
// below the cursor row when there's room, or above it when the
// cursor is near the bottom of the visible area. Horizontally,
// the modal is nudged so its left edge sits at the gutter end;
// this way it never hides the line numbers nor scrolls off the
// right edge.
//
// The composition uses lipgloss v2's Compositor + Layer model so the
// modal lands on a real cell grid (preserving ANSI sequences and
// CJK widths) instead of the splice-and-pray manual approach we
// had before. The background is drawn at z=0; the modal sits at
// z=1 on top.
func overlayCompleterNearCursor(back string, m Model, modal string) string {
	mh := lipgloss.Height(modal)
	mw := lipgloss.Width(modal)

	// The cursor's screen row = 1 (header) + (cursor.line - vp.YOffset).
	// One extra to drop the popup below the cursor; if that overflows,
	// flip to above.
	screenRow := 1 + (m.cursor.line - m.vp.YOffset())
	anchorY := screenRow + 1
	if anchorY+mh > m.height {
		anchorY = screenRow - mh
	}
	if anchorY < 0 {
		anchorY = 0
	}

	// Horizontal: place just past the gutter so the popup doesn't
	// cover the line numbers. Clamp to fit width.
	anchorX := digitsOf(m.buf.lineCount()) + 2
	if anchorX+mw > m.width {
		anchorX = m.width - mw
		if anchorX < 0 {
			anchorX = 0
		}
	}

	background := lipgloss.NewLayer(back)
	popup := lipgloss.NewLayer(modal).X(anchorX).Y(anchorY).Z(1)
	return lipgloss.NewCompositor(background, popup).Render()
}

// overlayModal centres a modal block over the background canvas,
// composed via lipgloss v2's Compositor + Layer. The background sits
// at z=0; the modal lands at z=1 with X/Y computed so its centre
// matches the screen centre.
//
// Unlike the previous line-splice implementation this preserves
// ANSI escapes in the background and never overwrites a single
// cell on rows the modal doesn't cover — so the editor below
// stays visually intact behind the popup.
func overlayModal(back string, w, h int, modal string) string {
	if w <= 0 {
		w = 40
	}
	if h <= 0 {
		h = 10
	}
	mw := lipgloss.Width(modal)
	mh := lipgloss.Height(modal)
	x := (w - mw) / 2
	if x < 0 {
		x = 0
	}
	y := (h - mh) / 2
	if y < 0 {
		y = 0
	}
	background := lipgloss.NewLayer(back)
	popup := lipgloss.NewLayer(modal).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(background, popup).Render()
}

// editorModalFrame is the shared chrome for the in-editor modals
// (discard / save confirmation, help). Colors come from the injected
// Styles so hosts can match their own visual language; see Options.Styles.
func editorModalFrame(title, body string, minW, maxW int, st Styles) string {
	if minW <= 0 {
		minW = 36
	}
	if maxW <= 0 {
		maxW = 64
	}

	// Width: longest visible line in title or body, plus the
	// frame's overhead (border 2 + padding 4).
	w := lipgloss.Width(title) + 4
	for _, line := range strings.Split(body, "\n") {
		if lw := lipgloss.Width(line); lw+4 > w {
			w = lw + 4
		}
	}
	if w < minW {
		w = minW
	}
	if w > maxW {
		w = maxW
	}

	innerW := w - 4

	titleBand := lipgloss.NewStyle().
		Background(st.ModalTitleBg).
		Foreground(st.ModalTitleFg).
		Bold(true).
		Width(innerW).
		Padding(0, 1).
		Render(title)

	frame := lipgloss.NewStyle().
		Border(frameBorders).
		BorderForeground(st.ModalBorder).
		Padding(0, 2).
		Width(w)
	return frame.Render(titleBand + "\n\n" + body)
}

// discardPrompt is the modal rendered when the user hits Esc with a
// dirty buffer.
func discardPrompt(st Styles) string {
	body := lipgloss.NewStyle().
		Foreground(st.ModalText).
		Render("Discard unsaved changes?\n\n[y] yes      [n] no")
	return editorModalFrame("Discard?", body, 40, 56, st)
}

// savePrompt is the modal rendered when Ctrl+S is pressed on an
// editor with RequireSaveConfirm=true. Confirms the user really
// wants to apply the buffer to disk, since saves trigger a reload.
func savePrompt(st Styles) string {
	body := lipgloss.NewStyle().
		Foreground(st.ModalText).
		Render("Apply changes and reload?\n\n[y] yes      [n] cancel")
	return editorModalFrame("Save?", body, 40, 56, st)
}

// helpOverlay lists every editor keybinding. Triggered by '?' from
// the editing surface; dismissed with any key. Kept terse on purpose
// (terminal is not the place for prose).
func helpOverlay(st Styles) string {
	lines := []string{
		"Navigation",
		"  ← → ↑ ↓           move cursor",
		"  home / end        line start / end",
		"  ctrl+home/end     buffer start / end",
		"  pgup / pgdn       scroll a page",
		"  ctrl+←/→, alt+←/→ jump by word",
		"",
		"Selection",
		"  shift + motion    extend selection",
		"  ctrl+a            select all",
		"",
		"Editing",
		"  tab / shift+tab   indent / outdent (2 spaces)",
		"  ctrl+d            duplicate line",
		"  ctrl+k            delete line",
		"  alt+↑ / alt+↓     move line up / down",
		"  ctrl+z            undo",
		"  ctrl+shift+z, ctrl+y  redo",
		"",
		"Find",
		"  ctrl+f            open find bar",
		"  enter / n         next match",
		"  N                 previous match",
		"  esc               close find bar",
		"",
		"Clipboard",
		"  ctrl+c / x / v    copy / cut / paste",
		"  (no selection: ctrl+c/x act on the whole line)",
		"",
		"Mouse",
		"  wheel             scroll",
		"  click             place cursor",
		"  drag              select text",
		"  shift+click       extend selection",
		"",
		"Save & quit",
		"  ctrl+s            save (may confirm)",
		"  esc / ctrl+q      quit (confirms if unsaved)",
		"",
		lipgloss.NewStyle().Foreground(st.ModalDim).Italic(true).
			Render("Press any key to close."),
	}
	body := lipgloss.NewStyle().
		Foreground(st.ModalText).
		Render(strings.Join(lines, "\n"))
	return editorModalFrame("Editor keybindings", body, 56, 72, st)
}
