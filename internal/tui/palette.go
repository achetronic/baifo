// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// palette.go owns the visual side of the slash-command autocomplete:
// the popup that floats above the writing box while the user is
// typing a `/...` command. The data flow is:
//
//	composer text  ──►  paletteSuggest()  ──►  paletteState
//	                                              │
//	                              renderPalette() └──► popup lines
//	                                              │
//	                            view.go splices these lines into
//	                            the rendered body just above the
//	                            composer's top border.
//
// The popup is intentionally narrow (~half the terminal at most),
// borderless on the sides, and anchored to the left under the
// composer's left padding so the eye flows naturally from the typed
// character to the suggestion list.

// paletteState is what the Model carries to remember the popup's
// current snapshot between key presses. It's recomputed on every
// keystroke from the composer text — we don't try to mutate it
// incrementally because the cost is trivial and a stateless
// recompute makes the popup robust to multi-character paste, undo,
// cursor moves, anything.
type paletteState struct {
	// Visible is set by paletteSuggest based on the composer
	// content. When false the Model skips render entirely.
	Visible bool

	// Items is the filtered, ordered set of suggestions for the
	// current input. Empty when Visible == false.
	Items []paletteItem

	// Selected is the index of the highlighted row in Items.
	// Always 0 when Items just changed shape; only Up/Down keys
	// move it.
	Selected int

	// ReplaceLen is how many bytes at the end of the composer
	// text the accept action should overwrite. See
	// paletteSuggest's contract for the semantics.
	ReplaceLen int

	// Query is the token currently being typed (the trailing
	// ReplaceLen bytes of the composer line). The renderer uses it
	// to highlight the matched prefix inside each suggestion so the
	// eye instantly sees why a row matched. Empty at a fresh level
	// (nothing typed yet).
	Query string
}

// refresh recomputes the suggestion set for the given composer
// text and clamps the selection into the new range. Callers
// (the Model) invoke this after EVERY keystroke that may have
// touched the composer.
func (p *paletteState) refresh(line string) {
	visible, items, replaceLen := paletteSuggest(line)
	p.Visible = visible
	p.Items = items
	p.ReplaceLen = replaceLen
	p.Query = ""
	if replaceLen > 0 && replaceLen <= len(line) {
		p.Query = line[len(line)-replaceLen:]
	}
	if p.Selected >= len(items) {
		p.Selected = 0
	}
	if p.Selected < 0 {
		p.Selected = 0
	}
}

// move adjusts the highlighted row by delta, wrapping around so
// Up at the top jumps to the bottom and Down at the bottom jumps
// to the top — the popup is short enough that wrap-around feels
// natural and saves the user from getting stuck.
func (p *paletteState) move(delta int) {
	if len(p.Items) == 0 {
		return
	}
	p.Selected = (p.Selected + delta + len(p.Items)) % len(p.Items)
}

// accept returns the new composer text after the user confirms the
// highlighted suggestion. The replacement strategy:
//
//   - Take everything in `line` except the trailing ReplaceLen
//     bytes (which were the prefix being typed).
//   - Append the selected item's Name.
//   - Append a trailing space when the item has further structured
//     children, so the popup immediately cascades into the next
//     level. Leaf nodes don't get the space because what comes
//     next is free-form data; the user can type it themselves.
//
// Returns ok == false when there's nothing to accept (popup not
// visible or empty), so the caller can fall back to whatever the
// pressed key normally does.
func (p paletteState) accept(line string) (newLine string, ok bool) {
	if !p.Visible || len(p.Items) == 0 {
		return line, false
	}
	if p.Selected < 0 || p.Selected >= len(p.Items) {
		return line, false
	}

	item := p.Items[p.Selected]
	cut := len(line) - p.ReplaceLen
	if cut < 0 {
		cut = 0
	}
	base := line[:cut]
	out := base + item.Name
	if !item.IsLeaf {
		// Trailing space cascades into the next level by giving
		// paletteSuggest the "previous token committed" signal.
		out += " "
	}
	return out, true
}

// paletteMaxRows caps how many suggestions the popup can show at
// once. We pick a small number because the writing box already
// eats four rows and we don't want the popup to swallow the chat.
// When there are more matches than this we still navigate through
// them all — only the rendering window slides.
const paletteMaxRows = 8

// paletteMinWidth is the smallest column count the popup will ever
// render at, even if every visible suggestion is shorter than this.
// Keeping a floor stops the popup from flickering between widths
// as the user types and the longest entry shrinks.
const paletteMinWidth = 28

// paletteMaxWidth caps the popup at roughly half a standard
// terminal so it doesn't dominate the writing-box row. Anything
// past this width the long fields (Summary, Usage) just get
// truncated with an ellipsis.
const paletteMaxWidth = 64

// renderPalette paints the popup as a multi-row string: the
// suggestion rows inside a rounded border (the same chrome every
// other floating surface uses, per the design system) plus a footer
// hint row that teaches the two-key interaction:
//
//	╭──────────────────────────────────╮
//	│ ▍ /mcp      manage MCP servers      │
//	│   /memory    long-term memory        │
//	│ 2/14 · ↹ complete · ⏎ send · ↑↓     │
//	╰──────────────────────────────────╯
//
// Every output row is exactly the same width so the splice in
// view.go just has to overwrite N background rows; no per-row
// width math downstream.
//
// The selected row gets a coloured left rail and a slightly lifted
// background; the part of each command name matching the typed
// prefix is painted in the accent colour so the user sees WHY a row
// matched. When more matches exist than fit, the footer's counter
// ("n/N") says so and the window slides with the selection.
func renderPalette(theme Theme, st paletteState, availableWidth int) string {
	if !st.Visible || len(st.Items) == 0 {
		return ""
	}

	// Inner width: reserve 2 cols for the border.
	innerAvail := availableWidth - 2
	width := computePaletteWidth(st.Items, innerAvail)

	// Slide the visible window so the selected row is always on
	// screen. We center it lazily: if it fits within the first
	// paletteMaxRows entries we don't scroll at all; only when
	// the selection moves past row paletteMaxRows-1 do we shift.
	start := 0
	if st.Selected >= paletteMaxRows {
		start = st.Selected - paletteMaxRows + 1
	}
	end := start + paletteMaxRows
	if end > len(st.Items) {
		end = len(st.Items)
	}
	window := st.Items[start:end]

	var rows []string
	for i, item := range window {
		absoluteIdx := start + i
		rows = append(rows, renderPaletteRow(theme, item, st.Query, absoluteIdx == st.Selected, width))
	}
	rows = append(rows, renderPaletteFooter(theme, st, width))

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		BorderBackground(colorBGAlt).
		Background(colorBGAlt)
	return border.Render(strings.Join(rows, "\n"))
}

// renderPaletteFooter paints the hint row at the bottom of the
// popup: a position counter when the list overflows the window,
// plus the two keys that matter. Dim on the popup background so it
// reads as chrome, not content.
func renderPaletteFooter(theme Theme, st paletteState, width int) string {
	hint := keyTab + " complete · " + keyEnter + " send · " + keyNav
	if len(st.Items) > paletteMaxRows {
		hint = fmt.Sprintf("%d/%d · %s", st.Selected+1, len(st.Items), hint)
	}
	hint = " " + truncateRunes(hint, width-2)
	style := lipgloss.NewStyle().Foreground(colorTextFaint).Background(colorBGAlt)
	row := style.Render(hint)
	if w := lipgloss.Width(row); w < width {
		row += style.Render(strings.Repeat(" ", width-w))
	}
	return row
}

// computePaletteWidth picks a width for the popup that fits the
// longest visible entry but stays within the configured caps and
// inside the available terminal width.
//
// We measure with lipgloss.Width on the would-be rendered text so
// CJK / emoji widths are accounted for the same way the rest of
// the layout measures things.
func computePaletteWidth(items []paletteItem, available int) int {
	w := paletteMinWidth
	for _, item := range items {
		// 2 for the left rail/gutter, 2 between name and summary,
		// 2 right padding.
		line := item.DisplayName + "  " + item.Summary
		if c := lipgloss.Width(line) + 4; c > w {
			w = c
		}
	}
	if w > paletteMaxWidth {
		w = paletteMaxWidth
	}
	if available > 0 && w > available {
		w = available
	}
	if w < 1 {
		w = 1
	}
	return w
}

// renderPaletteRow paints a single suggestion row at exactly
// `width` columns. The left two cells are the selection rail:
// a vertical bar in accent colour for the selected row, blanks
// for the rest. After that comes the bold command name — with the
// portion matching the typed query lifted in the accent colour —
// and the dim summary, truncated with `…` if they exceed the
// budget.
func renderPaletteRow(theme Theme, item paletteItem, query string, selected bool, width int) string {
	bg := colorBGAlt
	rail := "  "
	if selected {
		bg = colorBGFocus
		// "▍" + space — same selection marker used in the chat
		// list. Keeps the visual language consistent.
		rail = lipgloss.NewStyle().
			Foreground(theme.Accent.Primary).
			Background(bg).
			Render("▍") + lipgloss.NewStyle().Background(bg).Render(" ")
	} else {
		rail = lipgloss.NewStyle().Background(bg).Render(rail)
	}

	// Content budget = width - rail (2) - right pad (1).
	contentBudget := width - 3
	if contentBudget < 1 {
		contentBudget = 1
	}

	// Reserve roughly a third of the budget for the name; the
	// summary takes the rest. Names that overflow get truncated
	// before the summary does because the summary is the more
	// expendable bit.
	nameBudget := contentBudget / 3
	if nameBudget < 8 {
		nameBudget = 8
	}
	if nameBudget > contentBudget {
		nameBudget = contentBudget
	}
	name := truncateRunes(item.DisplayName, nameBudget)
	namePadded := padRightRunes(name, nameBudget)

	summaryBudget := contentBudget - nameBudget - 2 // 2 cells between name and summary
	if summaryBudget < 0 {
		summaryBudget = 0
	}
	summary := truncateRunes(item.Summary, summaryBudget)
	summaryPadded := padRightRunes(summary, summaryBudget)

	var nameStyle lipgloss.Style
	if !strings.HasPrefix(item.DisplayName, "/") {
		nameStyle = lipgloss.NewStyle().Foreground(colorTextDim).Background(bg)
	} else {
		// Every slash command is painted with the same accent so the
		// popup reads as one coherent list. (There used to be a
		// special case painting /help and /root with a brighter tone,
		// which clashed with the rest — see issue #5.)
		nameStyle = lipgloss.NewStyle().Foreground(colorInfo).Bold(true).Background(bg)
	}
	summaryStyle := lipgloss.NewStyle().Foreground(colorTextDim).Background(bg)
	gapStyle := lipgloss.NewStyle().Background(bg)

	// Highlight the typed query inside the name so the user sees the
	// match at a glance. The query matches the item NAME (no slash);
	// DisplayName may carry a leading "/" at the top level, so the
	// highlight window is offset by the display prefix length.
	renderedName := nameStyle.Render(namePadded)
	if query != "" {
		prefixLen := len([]rune(item.DisplayName)) - len([]rune(item.Name))
		qLen := len([]rune(query))
		nameRunes := []rune(namePadded)
		if prefixLen >= 0 && prefixLen+qLen <= len(nameRunes) &&
			strings.HasPrefix(strings.ToLower(item.Name), strings.ToLower(query)) {
			matchStyle := lipgloss.NewStyle().
				Foreground(theme.Accent.Focus).
				Bold(true).
				Underline(true).
				Background(bg)
			renderedName = nameStyle.Render(string(nameRunes[:prefixLen])) +
				matchStyle.Render(string(nameRunes[prefixLen:prefixLen+qLen])) +
				nameStyle.Render(string(nameRunes[prefixLen+qLen:]))
		}
	}

	row := rail +
		renderedName +
		gapStyle.Render("  ") +
		summaryStyle.Render(summaryPadded) +
		gapStyle.Render(" ")

	// Pad to exactly `width` so splicing into the body doesn't
	// shift the layout right of the popup.
	if w := lipgloss.Width(row); w < width {
		row += gapStyle.Render(strings.Repeat(" ", width-w))
	}
	return row
}

// truncateRunes shortens s to at most n runes, replacing the tail
// with an ellipsis when it actually had to cut something. We work
// on runes (not bytes) so multibyte characters survive at the
// boundary; the popup may contain CJK in user-defined commands
// once those exist.
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

// padRightRunes right-pads s with spaces so its visible width is
// exactly w cells (assuming non-zero-width runes — good enough for
// the latin-script commands we ship).
func padRightRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// overlayPaletteAboveComposer takes the fully-rendered body and
// splices the popup into the rows directly above the composer's
// top border. Returns the body unchanged when the popup is empty.
//
// The body is laid out by render() as a stack of newline-separated
// rows. From the BOTTOM (so the popup floats correctly above any
// streaming bar that may grow / shrink between renders):
//
//	... status (footerHeight rows)
//	... composer box + bottom margin (5 rows)
//	... streaming bar (1 row)   ← popup lands HERE, growing upwards
//	... main chat
//	... header
//
// We compute the splice row by counting from the bottom: the last
// `footerHeight` rows are the status bar; the five rows above are
// the composer (bordered box + bottom margin); the row above THAT
// is the streaming bar. The popup overwrites the streaming-bar row
// AND as many chat rows above it as it needs. Overwriting (rather
// than inserting) keeps the total row count constant so the
// terminal doesn't have to reflow.
func overlayPaletteAboveComposer(body, popup string) string {
	if popup == "" {
		return body
	}
	popupRows := strings.Split(popup, "\n")
	bodyRows := strings.Split(body, "\n")

	// Counted from the end: 1 status row + 5 composer rows = 6
	// rows reserved at the bottom. The popup's bottom row should
	// land just above the composer's box, i.e. on the streaming-bar
	// row. Using len-reservedBottomRows as the END (exclusive)
	// makes the arithmetic obvious.
	const reservedBottomRows = 1 + 5 // status + composer box + bottom margin
	end := len(bodyRows) - reservedBottomRows
	start := end - len(popupRows)
	if start < 0 {
		// Popup taller than the room above the composer — clip
		// from the top so the user still sees the highlighted
		// row at the bottom.
		popupRows = popupRows[-start:]
		start = 0
	}
	for i, row := range popupRows {
		bodyRows[start+i] = row
	}
	return strings.Join(bodyRows, "\n")
}
