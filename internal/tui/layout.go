// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

// Layout is the responsive break-point classifier used across every
// component. The TUI top model calls Classify(width, height) on every
// tea.WindowSizeMsg and passes the resulting LayoutMode down so each
// component can adapt without computing its own thresholds.
//
// See TUI_DESIGN.md > "Responsive" for the canonical definitions.

// LayoutMode enumerates the responsive break-points.
type LayoutMode int

const (
	// LayoutTooSmall means the terminal is unusable (height too low).
	// The TUI shows a single-line "terminal too small" message and no
	// other content.
	LayoutTooSmall LayoutMode = iota

	// LayoutNarrow is < 80 cols. Tabs collapse into a single-tab view,
	// sidebars hide, and panes stack vertically.
	LayoutNarrow

	// LayoutNormal is 80..119 cols. The right sidebar is hidden by default.
	LayoutNormal

	// LayoutWide is >= 120 cols. Full layout with a permanent right
	// sidebar on the Chat tab.
	LayoutWide
)

// Layout-mode thresholds. Exposed as constants so tests can reference
// them without hardcoding numbers and so future tuning lands in one
// place.
const (
	minRows       = 24
	narrowMaxCols = 79
	normalMaxCols = 119
)

// Classify maps a terminal size to a LayoutMode following the rules in
// TUI_DESIGN.md. Height is checked first because it gates the entire
// rendering pipeline.
func Classify(width, height int) LayoutMode {
	if height < minRows {
		return LayoutTooSmall
	}
	switch {
	case width <= narrowMaxCols:
		return LayoutNarrow
	case width <= normalMaxCols:
		return LayoutNormal
	default:
		return LayoutWide
	}
}

// SidebarVisible returns true when the responsive mode should show
// the right-hand workers sidebar by default. Only LayoutWide.
func (m LayoutMode) SidebarVisible() bool {
	return m == LayoutWide
}

// TabsCollapsed returns true when the responsive mode should hide the
// tab strip. Only LayoutNarrow.
func (m LayoutMode) TabsCollapsed() bool {
	return m == LayoutNarrow
}

// Zones is the four-band vertical decomposition of the screen
// described in TUI_DESIGN.md > Layout. Heights are absolute row
// counts; widths are inherited from the terminal.
//
// Zone heights add up to the terminal height. The Main zone absorbs
// any leftover rows because it is the only one whose contents scale.
type Zones struct {
	Tabs         int // zone 1: tab strip
	Main         int // zone 2: tab-specific content
	StreamingBar int // zone 2.5: thin one-line bar that shows the spinner during LLM streaming
	Composer     int // zone 3: composer (zero on tabs that hide it)
	Status       int // zone 4: status bar
}

// streamingBarHeight is the (constant) number of rows reserved
// between the chat and the composer for the streaming-bar zone.
// One row is plenty: spinner glyph + a short status string. The
// row is reserved even when no stream is in flight so the layout
// doesn't jump when a stream starts; renderStreamingBar paints a
// blank line in that case.
const streamingBarHeight = 1

// ZonesFor computes Zones for a given terminal height and whether the
// composer should be visible. The composer is shown only on Chat and
// on the Worker spy view.
//
// The Tabs zone is sized to fit the new header (logo + tab strip +
// rule). The Status zone is sized to fit the chip-based footer
// (rounded-border chips are 3 rows tall). When either constant
// changes (header.go, chips.go), this is the only place that needs
// updating.
func ZonesFor(height int, composerVisible bool) Zones {
	z := Zones{
		Tabs:   headerHeight,
		Status: footerHeight,
	}
	if composerVisible {
		// The composer is a bordered box (border 2 + 2 text rows = 4)
		// plus one bottom-margin row separating it from the footer
		// pills = 5 rows. Keep in sync with composer.View() and
		// overlayPaletteAboveComposer's reservedBottomRows.
		z.Composer = 5
		// StreamingBar lives between chat and composer, only when
		// the composer (and chat) are visible.
		z.StreamingBar = streamingBarHeight
	}
	z.Main = height - z.Tabs - z.Status - z.Composer - z.StreamingBar
	if z.Main < 1 {
		z.Main = 1
	}
	return z
}
