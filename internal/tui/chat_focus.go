// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "charm.land/bubbletea/v2"
)

// chat_focus.go owns the focus model of the Chat tab: where the
// keystrokes go (composer vs chat transcript) and the bookkeeping
// that comes with it. The model is intentionally tiny — focus is
// a single enum on Model, switched only on mouse click, and a row
// index when the chat side has focus.
//
// The chat has *focus* (not a mode). When the user clicks on the
// transcript, arrows navigate messages and Enter toggles tool
// expansion. When the user clicks on the writing box, arrows move
// the text cursor and Enter sends. Tabs reset to composerFocus on
// activation so the user always lands ready to type.

// focusComposer sets the focus on the writing box. The composer is
// re-focused so the textarea cursor blinks; the chat selection is
// cleared so the marker disappears and any expanded tool row
// folds back. Idempotent.
func (m *Model) focusComposer() {
	if m.focus == composerFocus {
		return
	}
	m.focus = composerFocus
	m.collapseSelectedTool()
	m.chatSel = -1
	m.chat.SetSelected(-1)
	m.chat.SetMessages(m.messages)
	m.composer.ta.Focus()
}

// focusChat sets the focus on the transcript. seedIndex is the
// initial selection, clamped to a valid row; pass -1 to seed at
// the last visible message (the natural place to start browsing).
// The composer is blurred so its cursor blink stops competing with
// the chat marker. Idempotent on focus, NOT idempotent on the
// selection: every call re-seeds chatSel.
//
// When the seed lands on a consumed tool-result index (a hidden
// row folded into the paired call's card), we slide it to the
// nearest visible neighbour so the keyboard navigation never
// starts on a row the user can't see.
func (m *Model) focusChat(seedIndex int) {
	if seedIndex < 0 {
		seedIndex = m.lastVisibleIndex()
	}
	if seedIndex < 0 {
		// Empty chat: nothing to focus on, leave the composer alone.
		return
	}
	if seedIndex >= len(m.messages) {
		seedIndex = len(m.messages) - 1
	}
	if !m.indexIsVisible(seedIndex) {
		// Snap to the previous visible row, then forward as a
		// last resort. Either way the user lands on a row that
		// is actually painted on screen.
		if p := m.previousVisibleIndex(seedIndex); p != seedIndex {
			seedIndex = p
		} else if n := m.nextVisibleIndex(seedIndex); n != seedIndex {
			seedIndex = n
		}
	}
	if m.focus != chatFocus {
		m.composer.ta.Blur()
		m.focus = chatFocus
	}
	if m.chatSel >= 0 && m.chatSel != seedIndex {
		m.collapseSelectedTool()
	}
	m.chatSel = seedIndex
	m.chat.SetSelected(m.chatSel)
	m.chat.SetMessages(m.messages)
}

// moveChatSelection shifts chatSel by the semantic of key. Used by
// the arrow / page / home / end handlers when the chat has focus.
// Out-of-range moves clamp at the boundaries; same-row moves are
// a no-op. Crossing rows collapses whichever tool row was open
// before — the user "went elsewhere", so we honour the intent.
//
// Hidden indices (MessageToolResult entries that are consumed into
// the paired call's card) are skipped so a single arrow press maps
// to a single visible row. Without this skip, the user had to
// press ↓ twice to cross every paired tool: once to land on the
// (invisible) result, once more to reach the next real row.
func (m Model) moveChatSelection(key string) Model {
	if len(m.messages) == 0 {
		return m
	}
	var target int
	switch key {
	case "up":
		target = m.previousVisibleIndex(m.chatSel)
	case "down":
		target = m.nextVisibleIndex(m.chatSel)
	case "pgup":
		target = m.chatSel
		for i := 0; i < 5; i++ {
			target = m.previousVisibleIndex(target)
		}
	case "pgdown":
		target = m.chatSel
		for i := 0; i < 5; i++ {
			target = m.nextVisibleIndex(target)
		}
	case "home":
		target = m.firstVisibleIndex()
	case "end":
		target = m.lastVisibleIndex()
	default:
		return m
	}
	if target == m.chatSel || target < 0 {
		return m
	}
	m.collapseSelectedTool()
	m.chatSel = target
	m.chat.SetSelected(m.chatSel)
	m.chat.SetMessages(m.messages)
	// Nudge the viewport so the new selection is on screen.
	// SetMessages only auto-scrolls when the chat was stuck at
	// the bottom; keyboard navigation crossing the window
	// boundary needs this explicit scroll or the marker would
	// "disappear" off the top/bottom of the panel.
	switch key {
	case "home":
		m.chat.GotoTop()
	case "end":
		m.chat.GotoBottom()
	default:
		m.chat.EnsureSelectedVisible()
	}
	return m
}

// indexIsVisible reports whether the message at i actually renders
// as its own block. Tool results that the renderer folded into the
// paired call's card get an empty rowSpan ([0, 0]) by convention,
// so we treat any zero-width span as hidden.
func (m Model) indexIsVisible(i int) bool {
	if i < 0 || i >= len(m.messages) {
		return false
	}
	if i >= len(m.chat.rowSpans) {
		return true // spans not yet populated: assume visible
	}
	span := m.chat.rowSpans[i]
	return span[0] != span[1]
}

// nextVisibleIndex returns the closest visible index strictly
// after i. When no such index exists, returns i unchanged so the
// caller's "did we move?" check kicks in and the press becomes a
// no-op.
func (m Model) nextVisibleIndex(i int) int {
	for j := i + 1; j < len(m.messages); j++ {
		if m.indexIsVisible(j) {
			return j
		}
	}
	return i
}

// previousVisibleIndex is the symmetric helper for upward motion.
func (m Model) previousVisibleIndex(i int) int {
	for j := i - 1; j >= 0; j-- {
		if m.indexIsVisible(j) {
			return j
		}
	}
	return i
}

// firstVisibleIndex returns the lowest visible index, or -1 when
// the chat is genuinely empty.
func (m Model) firstVisibleIndex() int {
	for j := 0; j < len(m.messages); j++ {
		if m.indexIsVisible(j) {
			return j
		}
	}
	return -1
}

// lastVisibleIndex returns the highest visible index, or -1 when
// the chat is genuinely empty.
func (m Model) lastVisibleIndex() int {
	for j := len(m.messages) - 1; j >= 0; j-- {
		if m.indexIsVisible(j) {
			return j
		}
	}
	return -1
}

// toggleSelectedTool flips Message.Expanded on the row currently
// under chatSel when it's a tool. No-op for plain user/root rows
// and when the chat has no selection. Used by Enter under chatFocus
// and by the mouse-click handler for tools.
func (m Model) toggleSelectedTool() Model {
	if m.chatSel < 0 || m.chatSel >= len(m.messages) {
		return m
	}
	msg := m.messages[m.chatSel]
	if msg.Kind != MessageToolCall && msg.Kind != MessageToolResult && msg.Kind != MessageNotice && msg.Kind != MessageAgentError {
		return m
	}
	m.messages[m.chatSel].Expanded = !m.messages[m.chatSel].Expanded
	m.chat.SetMessages(m.messages)
	return m
}

// collapseSelectedTool sets Expanded=false on the row at chatSel
// when it's a tool. Used right before moving the selection or
// switching focus: leaving an expanded row should not leave it
// expanded behind us. Cheap no-op when the selection isn't on a
// tool.
func (m *Model) collapseSelectedTool() {
	if m.keepToolsExpanded {
		return
	}
	if m.chatSel < 0 || m.chatSel >= len(m.messages) {
		return
	}
	if k := m.messages[m.chatSel].Kind; k != MessageToolCall && k != MessageToolResult && k != MessageNotice && k != MessageAgentError {
		return
	}
	m.messages[m.chatSel].Expanded = false
}

// handleMouse is the entry point for every mouse event on the Chat
// tab. It dispatches click and wheel events; motion (which we don't
// subscribe to via MouseModeCellMotion anyway) and release fall
// through as no-ops.
//
// Click semantics:
//   - left click inside the chat panel: focus the chat AND, when
//     the click lands on a tool row, toggle its expansion.
//   - left click inside the writing box: focus the composer.
//
// Wheel semantics:
//   - wheel up/down inside the chat panel: scroll the chat
//     viewport regardless of which side has focus. Wheel does NOT
//     change focus.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	region := m.regionAt(mouse.X, mouse.Y)

	switch e := msg.(type) {
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		switch region {
		case regionChat:
			rowInPanel := m.chatRowAt(mouse.Y)
			idx := m.messageIndexAtRow(rowInPanel)
			if idx >= 0 {
				m.focusChat(idx)
				if k := m.messages[idx].Kind; k == MessageToolCall || k == MessageToolResult || k == MessageNotice || k == MessageAgentError {
					return m.toggleSelectedTool(), nil
				}
				return m, nil
			}
			// Click on chat panel but on an empty / inter-row
			// area: focus the chat, don't toggle anything.
			m.focusChat(-1)
			return m, nil
		case regionComposer:
			m.focusComposer()
			return m, nil
		}
		return m, nil
	case tea.MouseWheelMsg:
		// Wheel always scrolls the chat, no matter where the
		// pointer is — it's the most expected behaviour from a
		// chat-style interface. We don't change focus on wheel.
		if region == regionChat || region == regionComposer {
			cmd := m.chat.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// region enumerates the broad zones of the screen the mouse can
// land on while the Chat tab is active. Header, footer and tab
// strip are intentionally lumped together as regionOther — they
// don't react to clicks today.
type region int

const (
	regionOther region = iota
	regionChat
	regionComposer
)

// regionAt returns which broad region of the screen the (x, y)
// coordinate lands on. It uses the same Zones the renderer uses
// so the click math stays in sync with the layout.
//
// Layout (top-to-bottom):
//
//	rows 0..headerHeight-1            → header (regionOther)
//	rows headerHeight..mainEnd-1      → main area (chat)
//	rows mainEnd..composerEnd-1       → composer
//	last footerHeight rows            → footer (regionOther)
func (m Model) regionAt(x, y int) region {
	// Clicks on the chat are ignored when an overlay is open;
	// the overlay's own handler owns the keyboard at that point
	// and we don't want a stray click to switch focus underneath
	// a modal view.
	if m.sessionsOpen || m.workersOpen || m.factsOpen || m.catalogOpen ||
		m.editor != nil || m.secretPrompt != nil {
		return regionOther
	}
	zones := ZonesFor(m.height, true)
	mainStart := zones.Tabs
	mainEnd := mainStart + zones.Main
	composerEnd := mainEnd + zones.Composer
	switch {
	case y < mainStart:
		return regionOther
	case y < mainEnd:
		return regionChat
	case y < composerEnd:
		return regionComposer
	}
	return regionOther
}

// chatRowAt converts a terminal-Y coordinate that landed inside the
// chat region into a zero-based row index *inside the chat panel's
// viewport*, accounting for the panel's top border. Negative result
// means "above the viewport content area" (the border itself).
func (m Model) chatRowAt(y int) int {
	zones := ZonesFor(m.height, true)
	// The chat panel border eats 1 row at the top of the main zone.
	contentTop := zones.Tabs + 1
	return y - contentTop
}

// messageIndexAtRow maps a row index inside the chat viewport's
// rendered content to the index of the Message that owns that row.
// Returns -1 when the row falls in a gap between messages, on an
// expanded-body line that belongs to the row above (not the tool
// header itself), or outside the rendered range.
//
// The mapping is built lazily by rendering the messages once and
// recording the start/end row of each entry. We call it from the
// mouse-click handler only, so the cost is bounded.
func (m Model) messageIndexAtRow(rowInPanel int) int {
	if rowInPanel < 0 {
		return -1
	}
	// Account for the viewport's vertical scroll offset: the row
	// the user clicked is at content offset = scrollOffset + rowInPanel.
	target := m.chat.vp.YOffset() + rowInPanel
	idx, ok := m.chat.rowForMessage(m.messages, target)
	if !ok {
		return -1
	}
	return idx
}
