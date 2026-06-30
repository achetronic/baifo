// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/achetronic/baifo/internal/facade"
)

// renderWorkers paints the /worker overlay using the shared
// chrome, composed as a centred modal over `back`. The
// kill/collect confirmations surface in the footer as destructive
// prompts (the overlay only allows one in flight at a time, so we
// render whichever is set; the dispatcher ensures they're
// mutually exclusive).
func renderWorkers(theme Theme, workers []facade.WorkerInfo, selected int, confirmKillID, confirmCollectID string, back string, width, height int) string {
	items := make([]listItem, 0, len(workers))
	for _, w := range workers {
		name := w.Name
		if name == "" {
			name = w.ID
		}
		marker, markerKind := workerMarker(w.Status)
		suffix := w.Status
		if w.Elapsed != "" {
			suffix += "  " + w.Elapsed
		}
		if w.LastEvent != "" {
			suffix += "  · " + w.LastEvent
		}
		items = append(items, listItem{
			Label:       name,
			EntityKind:  w.Kind,
			MarkerGlyph: marker,
			MarkerKind:  markerKind,
			Suffix:      suffix,
		})
	}

	content := renderList(theme, items, selected,
		"no workers running\n\nspawn one via the chat — \"crea un worker que...\"",
		listOverlayMinRows, listOverlayContentWidth)

	footer := keyNav + " select · " + keyEnter + " open worker chat · [k] kill · [c] collect · [esc] close"
	confirm := ""
	switch {
	case confirmKillID != "":
		confirm = "kill worker " + confirmKillID + "? this cannot be undone (y/N)"
	case confirmCollectID != "":
		confirm = "collect worker " + confirmCollectID + "? it leaves the live list and its sandbox is wiped (y/N)"
	}

	return renderModal(theme, overlayOpts{
		Title:         "Workers",
		Content:       content,
		Footer:        footer,
		ConfirmPrompt: confirm,
		MinWidth:      listOverlayMinWidth,
		MaxWidth:      listOverlayMaxWidth,
	}, back, width, height)
}

// workerMarker maps a worker status to the marker glyph + kind
// shown in the list's leading column. Running workers stand out
// with a warning-coloured pulse; failures and kills surface in red.
func workerMarker(status string) (glyph, kind string) {
	switch status {
	case "running":
		return "●", "warning"
	case "done":
		return "●", "ok"
	case "failed", "killed":
		return "●", "error"
	case "idle":
		return "○", ""
	}
	return "○", ""
}

// formatWorkerRow is gone — the row is now composed by renderList
// from a listItem. Kept the comment so anyone scrolling through
// git blame finds the migration note.

// renderSessions paints the /session overlay as a centred modal
// over `back` using the shared chrome. The list of sessions is
// built via renderList so the cursor glyph, marker column and
// suffix style match every other overlay in the TUI.
//
// The active session gets a "●" marker in the success colour; the
// title falls back to "(untitled)" when empty. confirmDeleteID,
// when non-empty, swaps the footer for a destructive prompt.
func renderSessions(theme Theme, sessions []facade.SessionInfo, activeID string, selected int, confirmDeleteID string, back string, width, height int) string {
	items := make([]listItem, 0, len(sessions))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		// Each session renders as title + two faint meta rows
		// (timestamp + id). The selection cursor still moves
		// one session at a time; the meta rows ride along with
		// their owner. Empty values are skipped so the list
		// doesn't grow blank rows on minimal entries.
		meta := make([]string, 0, 2)
		if s.LastAt != "" {
			meta = append(meta, s.LastAt)
		}
		if s.ID != "" {
			meta = append(meta, s.ID)
		}
		it := listItem{
			Label:      title,
			EntityKind: "session",
			MetaLines:  meta,
		}
		if s.ID == activeID {
			it.MarkerGlyph = "●"
			it.MarkerKind = "ok"
		}
		items = append(items, it)
	}

	content := renderList(theme, items, selected, "no sessions stored yet\n\npress n to start one",
		listOverlayMinRows, listOverlayContentWidth)

	footer := keyNav + " select · " + keyEnter + " resume · [n] new · [d] delete · [r] rename · [esc] close"
	confirm := ""
	if confirmDeleteID != "" {
		confirm = "delete session " + confirmDeleteID + "? this cannot be undone (y/N)"
	}

	return renderModal(theme, overlayOpts{
		Title:         "Sessions",
		Content:       content,
		Footer:        footer,
		ConfirmPrompt: confirm,
		MinWidth:      listOverlayMinWidth,
		MaxWidth:      listOverlayMaxWidth,
	}, back, width, height)
}

// renderFacts paints the /fact overlay as a centred modal over
// `back` using the shared chrome. One item per stored long-term
// memory entry: the label is the first line of the content
// (truncated by renderList) and the meta row carries the ID,
// category, author and timestamp so the user can identify entries
// at a glance. confirmDeleteID, when non-zero, swaps the footer
// for the destructive y/N prompt — same pattern as Sessions.
func renderFacts(theme Theme, facts []facade.FactDetail, selected int, confirmDeleteID uint64, back string, width, height int) string {
	items := make([]listItem, 0, len(facts))
	for _, f := range facts {
		label := strings.TrimSpace(f.Content)
		if i := strings.IndexByte(label, '\n'); i >= 0 {
			label = label[:i]
		}
		if label == "" {
			label = "(empty)"
		}

		metaParts := make([]string, 0, 4)
		metaParts = append(metaParts, fmt.Sprintf("#%d", f.ID))
		if f.Category != "" {
			metaParts = append(metaParts, f.Category)
		}
		if f.Author != "" {
			metaParts = append(metaParts, f.Author)
		}
		if !f.Timestamp.IsZero() {
			metaParts = append(metaParts, f.Timestamp.Format("2006-01-02 15:04"))
		}

		items = append(items, listItem{
			Label:      label,
			EntityKind: "fact",
			MetaLines:  []string{strings.Join(metaParts, " · ")},
		})
	}

	content := renderList(theme, items, selected,
		"no facts stored yet\n\npress n to add one — or just tell the agent something worth remembering",
		listOverlayMinRows, listOverlayContentWidth)

	footer := keyNav + " select · " + keyEnter + " edit · [n] new · [d] delete · [esc] close"
	confirm := ""
	if confirmDeleteID != 0 {
		confirm = fmt.Sprintf("delete fact #%d? this cannot be undone (y/N)", confirmDeleteID)
	}

	return renderModal(theme, overlayOpts{
		Title:         "Facts",
		Content:       content,
		Footer:        footer,
		ConfirmPrompt: confirm,
		MinWidth:      listOverlayMinWidth,
		MaxWidth:      listOverlayMaxWidth,
	}, back, width, height)
}

// renderWorkersSidebar paints the right-hand sidebar for LayoutWide.
// One row per worker with the status glyph and the short name; that
// is enough for the operator to see at a glance whether anything is
// running while they chat with the root.
func renderWorkersSidebar(theme Theme, workers []facade.WorkerInfo, width, height int) string {
	border := theme.PanelBorder()
	innerW := width - 2
	innerH := height - 2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}
	if len(workers) == 0 {
		return border.Width(innerW).Height(innerH).Render(theme.FaintText().Render("no workers"))
	}
	var b strings.Builder
	for _, w := range workers {
		glyph := theme.Glyph("idle")
		switch w.Status {
		case "running":
			glyph = theme.Glyph("running")
		case "done":
			glyph = theme.Glyph("done")
		case "failed", "killed":
			glyph = theme.Glyph("failed")
		}
		fmt.Fprintf(&b, "%s %s\n",
			theme.EntityText(w.Kind).Render(glyph),
			theme.PrimaryText().Render(w.Name),
		)
	}
	return border.Width(innerW).Height(innerH).Render(strings.TrimRight(b.String(), "\n"))
}

// Compile-time guard so changes to context.Background-only handlers
// surface here rather than at link time.
var _ = context.Background
