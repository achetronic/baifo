// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// overlay_chrome.go defines the shared visual language for every
// modal overlay in the TUI (sessions, workers, settings, help,
// future inspector). The two primitives here — renderOverlay and
// renderList — exist so adding a new overlay is just "fill in the
// content" and the chrome stays consistent.
//
// Anatomy of every overlay:
//
//	╭─ Title ────────────────────────────── (hint?) ─╮
//	│                                                │
//	│  <content lines, painted by the caller>        │
//	│                                                │
//	│  ──────────────────────────────────────────    │
//	│  ↑↓ navigate · ⏎ confirm · esc close           │
//	╰────────────────────────────────────────────────╯
//
// The whole frame uses the focused panel border (accent) because an
// overlay always has the focus. Title is the first line inside the
// frame, bold accent. An optional hint sits to the right of the
// title in faint text. A horizontal separator divides the content
// from the footer keybindings. Destructive confirmation prompts
// REPLACE the footer text — no nested overlay, same chrome.

// overlayOpts collects the inputs for renderOverlay. Title and
// content are mandatory; the rest are optional and degrade
// gracefully when empty.
type overlayOpts struct {
	// Title shows top-left, bold in the accent colour. The overlay
	// is unusable without a title — it's the operator's anchor.
	Title string

	// Hint is an optional one-liner painted faintly to the right of
	// the title (e.g. "(encrypted)" on /secret). Empty hides it.
	Hint string

	// Content is the body of the overlay as a multi-line string.
	// Width is pre-computed by the caller via the contentWidth() of
	// the overlay (see renderOverlay below); the renderer just
	// stamps it inside the frame.
	Content string

	// Footer is the keybindings hint shown below the content, after
	// a faint horizontal rule. Empty hides the rule too.
	Footer string

	// ConfirmPrompt, when non-empty, replaces the Footer with a
	// destructive prompt rendered in error colour. The y/N text is
	// the caller's responsibility — keeps the prompt close to the
	// action that triggered it.
	ConfirmPrompt string

	// MinWidth / MaxWidth bracket renderModal's content-driven
	// sizing. Zero means "use the defaults" (defaultModalMinWidth
	// / defaultModalMaxWidth). Use them when a modal has tighter
	// expectations than the defaults (e.g. a password prompt wants
	// at least 56 cols, never more than 64).
	MinWidth int
	MaxWidth int

	// MinHeight is a floor for vertically short content (single
	// confirmation prompts read better with a little air around
	// them). Zero defers to renderModal's default.
	MinHeight int
}

// renderModal is THE primitive every modal in baifo goes through.
// See .agents/OVERLAY_STYLE.md for the design rules — same look,
// same centring, no exceptions.
//
// Behaviour:
//
//  1. Compute the modal's outer width from the widest line in
//     Title / Hint / Content / Footer / ConfirmPrompt, clamped to
//     [opts.MinWidth, opts.MaxWidth] and the available screen width.
//  2. Compute height from the line count of those same fields plus
//     the chrome (title band + 1 blank + separator + footer + 4
//     for border/padding), floored at opts.MinHeight.
//  3. Render the chrome via renderOverlay at that exact size.
//  4. Compose the chrome over `body` using lipgloss v2's
//     Compositor + Layer at the screen's centre. The body stays
//     visible underneath (no whitespace fill).
//
// Important: we use `lipgloss.NewCompositor(...)` rather than
// `Canvas.Compose(...)` — the latter ignores Layer X/Y/Z and
// renders everything at (0, 0). The audit that found this lives
// in the commit history; do not "simplify" back to Canvas.
//
// Callers pass the rendered `body` (chat + header + composer +
// status; or whatever the lower layer produced) and renderModal
// returns the composed string ready to hand back from Model.View().
func renderModal(theme Theme, opts overlayOpts, body string, screenW, screenH int) string {
	minW := opts.MinWidth
	if minW <= 0 {
		minW = defaultModalMinWidth
	}
	maxW := opts.MaxWidth
	if maxW <= 0 {
		maxW = defaultModalMaxWidth
	}
	minH := opts.MinHeight
	if minH <= 0 {
		minH = defaultModalMinHeight
	}

	// Cap to what the screen can actually hold — a modal must not
	// overflow the canvas or the Layer math falls off the edges.
	screenCapW := screenW - 4 // leave a 2-col gutter on each side
	if maxW > screenCapW {
		maxW = screenCapW
	}
	if minW > maxW {
		minW = maxW
	}
	screenCapH := screenH - 2
	if minH > screenCapH {
		minH = screenCapH
	}

	// Content-driven width: the longest visible line in any of the
	// chrome fields, plus the frame's 4-col overhead.
	contentW := widestVisibleLine(opts.Title, opts.Hint, opts.Content,
		opts.Footer, opts.ConfirmPrompt)
	w := contentW + 4
	if w < minW {
		w = minW
	}
	if w > maxW {
		w = maxW
	}

	// Content-driven height: title (1) + blank (1) + content lines
	// + optional separator+footer (2) + frame overhead (4).
	contentLines := lipgloss.Height(opts.Content)
	if contentLines < 1 && opts.Content != "" {
		contentLines = strings.Count(opts.Content, "\n") + 1
	}
	footerRows := 0
	if opts.Footer != "" || opts.ConfirmPrompt != "" {
		footerRows = 2 // separator + text
	}
	h := contentLines + 2 /*title+blank*/ + footerRows + 4 /*frame*/
	if h < minH {
		h = minH
	}
	if h > screenCapH {
		h = screenCapH
	}

	chrome := renderOverlay(theme, opts, w, h)

	// Compose over body, centred. Canvas+Layer leaves the body's
	// cells intact outside the modal's rectangle — no whitespace
	// fill that would erase the chat behind.
	chromeW := lipgloss.Width(chrome)
	chromeH := lipgloss.Height(chrome)
	x := (screenW - chromeW) / 2
	if x < 0 {
		x = 0
	}
	y := (screenH - chromeH) / 2
	if y < 0 {
		y = 0
	}
	// Compose over body, centred. We use lipgloss v2's
	// Compositor (NOT Canvas.Compose, which ignores Layer X/Y/Z)
	// so the background stays put and the modal lands at the
	// computed centre.
	background := lipgloss.NewLayer(body)
	modal := lipgloss.NewLayer(chrome).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(background, modal).Render()
}

// widestVisibleLine returns the maximum lipgloss.Width across any
// line of any of the given strings. Used by renderModal to size
// the frame to its content.
func widestVisibleLine(parts ...string) int {
	max := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		for _, line := range strings.Split(p, "\n") {
			if w := lipgloss.Width(line); w > max {
				max = w
			}
		}
	}
	return max
}

// Default sizing brackets for renderModal. Callers override via
// overlayOpts.MinWidth / MaxWidth / MinHeight when a particular
// modal wants a tighter range (e.g. a single-line confirmation
// reads cramped under MinWidth=28; a settings overlay wants more
// room than the default cap).
const (
	defaultModalMinWidth  = 32
	defaultModalMaxWidth  = 96
	defaultModalMinHeight = 6
)

// Homogeneous sizing for the list-based overlays (Settings,
// Sessions, Workers). Pinning the three to identical
// dimensions means they all open into the same eye-friendly
// rectangle regardless of how many items are in them — empty
// lists pad with blank rows, long lists scroll inside the
// window. Use these as the MinWidth/MinHeight values on
// overlayOpts so the modal frame can still GROW when content
// genuinely needs more (e.g. a long agent prompt preview).
const (
	listOverlayMinWidth = 90
	listOverlayMaxWidth = 110
	listOverlayMinRows  = 18 // visible list rows; modal height derives from this
)

// listOverlayContentWidth returns the column budget renderList
// gets for its labels/suffixes/meta when the host modal is sized
// at listOverlayMinWidth: 4 cols eaten by the frame (border +
// padding) and another 4 by the cursor/marker rail. Used by the
// list-based overlays to feed renderList a sane truncation
// budget without having to reach into the modal sizing math.
const listOverlayContentWidth = listOverlayMinWidth - 4 - 4

// renderOverlay paints a modal-style box at the given outer width
// and height. The frame eats 2 cols of border + 2 cols of padding
// horizontally (and 2 rows vertically), so the content area is
// (w-4) × (h-4) — overlayContentSize returns that.
//
// Most callers should use renderModal instead — it handles
// centring + sizing automatically. Use renderOverlay directly
// only when you genuinely want a fixed-size frame embedded in
// some larger layout (rare).
func renderOverlay(theme Theme, opts overlayOpts, w, h int) string {
	innerW, innerH := overlayContentSize(w, h)
	if innerW < 20 {
		innerW = 20
	}
	if innerH < 4 {
		innerH = 4
	}

	var b strings.Builder

	// Title bar. The whole row gets the accent's subtle tone as
	// background so the title reads as a heading band, not just
	// inline text. Title and hint live on top of the band in
	// accent and faint respectively. The band stretches to the
	// inner content width so the colour reaches the right edge.
	titleBand := lipgloss.NewStyle().
		Background(theme.Accent.Subtle).
		Foreground(theme.Accent.Primary).
		Bold(true).
		Width(innerW).
		Padding(0, 1)

	inner := opts.Title
	if opts.Hint != "" {
		// Place the hint on the right side of the band, in a
		// dim-text tone that survives on top of the subtle
		// background. We compute the gap from the visible width
		// of the styled-as-plain parts, then let titleBand wrap
		// the whole thing with the band background.
		hintInner := lipgloss.NewStyle().
			Foreground(colorTextDim).
			Render(opts.Hint)
		// Inner width = innerW - 2 (padding 0,1 eats 2 cols).
		gap := innerW - 2 - lipgloss.Width(opts.Title) - lipgloss.Width(opts.Hint)
		if gap < 1 {
			gap = 1
		}
		inner = opts.Title + strings.Repeat(" ", gap) + hintInner
	}
	b.WriteString(titleBand.Render(inner))
	b.WriteString("\n\n")

	// Content. Lipgloss wraps to innerW but most callers already
	// wrap; we still cap to make sure no line escapes the frame.
	if opts.Content != "" {
		b.WriteString(opts.Content)
	}

	// Footer. The separator is only drawn when there's something
	// to put underneath (footer or confirm). That way an overlay
	// without keybindings doesn't grow a dangling rule.
	footer := opts.Footer
	if opts.ConfirmPrompt != "" {
		footer = theme.StatusError().Bold(true).Render(opts.ConfirmPrompt)
	} else if footer != "" {
		footer = theme.FaintText().Render(footer)
	}
	if footer != "" {
		// Pad the content area so the separator sits at the bottom
		// of the inner box. We compute current rows, then add
		// blank rows until the separator lands on row (innerH-2).
		curRows := strings.Count(b.String(), "\n") + 1
		// Reserve 2 rows for separator + footer.
		want := innerH - 2
		for curRows < want {
			b.WriteByte('\n')
			curRows++
		}
		b.WriteByte('\n')
		b.WriteString(theme.FaintText().Render(strings.Repeat("─", innerW)))
		b.WriteByte('\n')
		b.WriteString(footer)
	}

	border := theme.PanelBorderFocused()
	return border.Width(w).Height(h).Render(strings.TrimRight(b.String(), "\n"))
}

// overlayContentSize returns the inner (content) width and height
// for an overlay drawn at outer dimensions (w, h). The frame
// (border + padding) eats 4 cols and 4 rows total.
func overlayContentSize(w, h int) (int, int) {
	return w - 4, h - 4
}

// ─── List primitive ──────────────────────────────────────────────

// listItem is one row in a renderList block. Label is the
// human-readable text; EntityKind drives the colour through
// Theme.EntityText (pass "" for neutral text). MarkerGlyph and
// MarkerKind paint a leading status indicator (e.g. "●" + "ok"
// for an active session). Suffix is faint text appended after the
// label (timestamps, ids, brief counts).
//
// MetaLines, when set, renders one extra faint row per entry below
// the label — indented to align with the label, no cursor or
// marker. Selection still moves by item (the cursor and marker
// occupy the first row only). Use this when an item has more
// fields than fit on one line (e.g. session = title + timestamp
// + id). Each meta line is truncated independently if it
// overflows the available content width.
type listItem struct {
	Label       string
	EntityKind  string // "root" | "static" | "dynamic" | "skill" | ... | ""
	MarkerGlyph string // "" hides the marker column
	MarkerKind  string // severity key: "ok" | "warning" | "error" | "info" | ""
	Suffix      string
	MetaLines   []string
}

// renderList paints a vertical, navigable list with a uniform
// cursor glyph (› accent), an optional left marker column (for
// "active session" / "running worker" cues), the label coloured
// by entity, and an optional dim suffix.
//
// Items render with a sliding window centred on `selected`, sized
// so the rendered block fits within `maxRows` terminal rows.
// Items with `MetaLines` set occupy multiple rows each (label
// + N faint meta rows indented under it); selection still moves
// by item, and the cursor + marker glyphs appear only on the
// first row of each item. selected is the index of the focused
// item; -1 means "no selection" and the cursor column stays
// blank on every row. emptyHint is shown when items is empty so
// the user gets a useful state ("no sessions yet" etc.).
//
// `contentWidth` is the number of terminal columns the list has
// to play with (before the rail). Labels, suffixes and meta
// lines that would overflow get truncated rune-aware with a
// trailing "…". Pass <= 0 to disable truncation (legacy
// behaviour, useful for tests).
//
// When the list overflows the window, a final dim row shows the
// position ("n / N · ↑/↓ scroll") so the user knows there's more
// content above or below.
//
// The function returns the fully styled string ready to drop
// into renderOverlay's Content field. The output always has
// exactly `maxRows` lines when maxRows > 0 (padded with blanks
// at the bottom if the list is shorter) so the modal's footer
// keeps a stable position regardless of how full the list is.
func renderList(theme Theme, items []listItem, selected int, emptyHint string, maxRows, contentWidth int) string {
	if len(items) == 0 {
		out := theme.FaintText().Render(emptyHint)
		if maxRows > 0 {
			emptyLines := strings.Count(out, "\n") + 1
			for emptyLines < maxRows {
				out += "\n"
				emptyLines++
			}
		}
		return out
	}

	// itemRows is the height in terminal rows of items[i] —
	// always 1 + len(MetaLines).
	itemRows := func(i int) int {
		return 1 + len(items[i].MetaLines)
	}

	// Window: pick the largest contiguous range of items around
	// `selected` whose total rendered height + the scroll
	// indicator (1 row when overflow) fits in maxRows. When
	// maxRows is zero we show everything.
	start, end := 0, len(items)
	overflow := false
	if maxRows > 0 {
		// We always reserve 1 row for the indicator when
		// overflow is detected; provisionally compute totals
		// without it and re-check at the end.
		budget := maxRows
		totalRows := 0
		for i := range items {
			totalRows += itemRows(i)
		}
		if totalRows > budget {
			overflow = true
			budget = maxRows - 1
			if budget < 1 {
				budget = 1
			}
			// Centre the window on `selected`: start at
			// selected and grow outwards, alternating below
			// and above, until budget is full.
			start, end = selected, selected+1
			used := itemRows(selected)
			above := selected - 1
			below := selected + 1
			for used < budget {
				grew := false
				if below < len(items) && used+itemRows(below) <= budget {
					end = below + 1
					used += itemRows(below)
					below++
					grew = true
				}
				if used >= budget {
					break
				}
				if above >= 0 && used+itemRows(above) <= budget {
					start = above
					used += itemRows(above)
					above--
					grew = true
				}
				if !grew {
					break
				}
			}
		}
	}

	// Truncation budget for label / suffix / meta lines. The
	// row prefix is rail (2) + marker (2), so labels & meta
	// have contentWidth - 4 cols to play with.
	textBudget := 0
	if contentWidth > 0 {
		textBudget = contentWidth - 4
		if textBudget < 8 {
			textBudget = 8
		}
	}

	var b strings.Builder
	rowsWritten := 0
	for i := start; i < end; i++ {
		it := items[i]

		// Cursor column on the first row only.
		cursor := "  "
		if i == selected {
			cursor = theme.AccentText().Render("› ")
		}

		// Marker column on the first row only.
		marker := "  "
		if it.MarkerGlyph != "" {
			style := listMarkerStyle(theme, it.MarkerKind)
			marker = style.Render(it.MarkerGlyph) + " "
		}

		// Label + optional suffix on the same row.
		labelStyle := theme.PrimaryText()
		if it.EntityKind != "" {
			labelStyle = theme.EntityText(it.EntityKind)
		}
		labelText := it.Label
		suffixText := it.Suffix
		// Budget split: label gets at least 2/3 of the row;
		// suffix takes the rest. Both truncate with ellipsis
		// independently when they overflow.
		if textBudget > 0 {
			labelBudget := textBudget
			if suffixText != "" {
				// Reserve 2 cols for the gap between label
				// and suffix.
				suffixBudget := textBudget / 3
				if suffixBudget < 8 {
					suffixBudget = 8
				}
				labelBudget = textBudget - suffixBudget - 2
				if labelBudget < 8 {
					labelBudget = 8
				}
				suffixText = truncateRunesWithEllipsis(suffixText, suffixBudget)
			}
			labelText = truncateRunesWithEllipsis(labelText, labelBudget)
		}
		label := labelStyle.Render(labelText)
		suffix := ""
		if suffixText != "" {
			suffix = "  " + theme.FaintText().Render(suffixText)
		}
		b.WriteString(cursor + marker + label + suffix)
		b.WriteByte('\n')
		rowsWritten++

		// Meta rows: indented under the label, faint text.
		// Same rail-width prefix (2 cols cursor + 2 cols
		// marker) so the meta line starts where the label
		// starts.
		for _, meta := range it.MetaLines {
			text := meta
			if textBudget > 0 {
				text = truncateRunesWithEllipsis(text, textBudget)
			}
			b.WriteString("    ") // 2 (cursor) + 2 (marker)
			b.WriteString(theme.FaintText().Render(text))
			b.WriteByte('\n')
			rowsWritten++
		}
	}

	rendered := strings.TrimRight(b.String(), "\n")
	if maxRows > 0 {
		if overflow {
			indicator := theme.FaintText().Italic(true).
				Render(fmt.Sprintf("  %d / %d · %s scroll", selected+1, len(items), keyNav))
			rendered += "\n" + indicator
			rowsWritten++
		}
		for rowsWritten < maxRows {
			rendered += "\n"
			rowsWritten++
		}
	}
	return rendered
}

// truncateRunesWithEllipsis shortens s so its on-screen width is
// at most n columns, replacing the tail with "…" when something
// was actually cut. Width-aware (uses lipgloss.Width) so:
//
//   - Multibyte glyphs (CJK, emoji) survive at the boundary.
//   - Strings that already carry ANSI escapes (rendered keycaps,
//     coloured spans) aren't accidentally cut through the
//     middle of an escape sequence — if the string contains any
//     escape we trust the caller to have sized it appropriately
//     and return it intact. The placeholder rows from
//     placeholderForSection are the canonical example: their
//     visible width is ~46 cols but their rune count is ~80,
//     and a naive rune-based truncation would chop them at "[".
//
// n<=0 leaves s untouched (the caller opts out of truncation by
// passing a non-positive budget).
func truncateRunesWithEllipsis(s string, n int) string {
	if n <= 0 {
		return s
	}
	visible := lipgloss.Width(s)
	if visible <= n {
		return s
	}
	// Pre-styled strings (ANSI escapes present) get a pass: we
	// don't have a safe rune-by-rune cutter that respects the
	// escape boundaries. The caller is expected to size the
	// content properly; if it still overflows, render-time
	// clipping in the modal frame deals with the worst case.
	if strings.ContainsRune(s, 0x1b) {
		return s
	}
	rs := []rune(s)
	if n == 1 {
		return "…"
	}
	return string(rs[:n-1]) + "…"
}

// listMarkerStyle maps a severity-ish kind to a lipgloss style for
// the marker column. Falls back to dim text on unknown kinds so a
// typo isn't catastrophic.
func listMarkerStyle(theme Theme, kind string) lipgloss.Style {
	switch kind {
	case "ok", "success":
		return theme.StatusOK()
	case "warning":
		return theme.StatusWarning()
	case "error", "failed":
		return theme.StatusError()
	case "info":
		return theme.StatusInfo()
	}
	return theme.DimText()
}

// keycap renders a key reference inline in body text, framed by
// square brackets ("[n]") with the key char itself in the active
// accent colour. Use it any time prose mentions a literal key the
// user is meant to press, so prose and chrome agree on what "this
// is a keystroke" looks like.
//
// Brackets stay in the faint tone of the surrounding text so only
// the letter itself draws the eye.
func keycap(theme Theme, key string) string {
	open := theme.FaintText().Render("[")
	closing := theme.FaintText().Render("]")
	return open + theme.AccentText().Render(key) + closing
}
