// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Update implements tea.Model. The editor handles every keystroke
// itself when focused; an unfocused editor ignores keys but still
// receives clipboard and resize messages so View renders correctly.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		// PasteMsg arrives when bracketed paste is enabled in the
		// host. We accept it regardless of focus because pasting is
		// always intentional from the user's side.
		return m.handlePaste(msg.String())

	case tea.ClipboardMsg:
		// Reply to a previous ReadClipboard() request. The editor
		// only issues ReadClipboard from actPaste (Ctrl+V); when the
		// reply arrives we insert the value at the cursor.
		return m.handlePaste(msg.String())

	case tea.MouseWheelMsg:
		return m.handleWheel(tea.Mouse(msg)), nil

	case tea.MouseClickMsg:
		return m.handleMouseClick(tea.Mouse(msg)), nil

	case tea.MouseMotionMsg:
		return m.handleMouseMotion(tea.Mouse(msg)), nil

	case tea.MouseReleaseMsg:
		m.dragging = false
		return m, nil
	}
	return m, nil
}

// mouseWheelStep is how many lines one wheel notch moves. Three is
// the de-facto terminal standard (same as the chat viewport).
const mouseWheelStep = 3

// handleWheel moves the cursor up/down by mouseWheelStep lines and
// lets syncViewport drag the view along when the cursor hits an edge.
//
// Why cursor-and-not-just-view: an earlier version scrolled only the
// viewport, vim Ctrl+E style. That felt broken in practice — entity
// scaffolds often fit on one screen (so nothing visibly happened) and
// the first arrow key snapped the view straight back to wherever the
// cursor had stayed, undoing the scroll. Moving the cursor gives
// visible feedback on every notch and keeps keyboard and mouse in
// agreement about where the action is.
func (m Model) handleWheel(ev tea.Mouse) Model {
	if m.modalOpen() {
		return m
	}
	var motion func(*buffer, position) position
	switch ev.Button {
	case tea.MouseWheelUp:
		motion = moveUp
	case tea.MouseWheelDown:
		motion = moveDown
	default:
		return m
	}
	for i := 0; i < mouseWheelStep; i++ {
		m.cursor = motion(m.buf, m.cursor)
	}
	m.sel = nil
	m.syncViewport()
	return m
}

// handleMouseClick places the cursor at the clicked cell (left button
// only) and arms drag-selection. Clicks on the chrome (header,
// footer, error bar) and on modal overlays are ignored.
func (m Model) handleMouseClick(ev tea.Mouse) Model {
	if ev.Button != tea.MouseLeft || m.modalOpen() {
		return m
	}
	pos, ok := m.positionAtCell(ev.X, ev.Y)
	if !ok {
		return m
	}
	m.cursor = pos
	if ev.Mod&tea.ModShift != 0 && m.sel != nil {
		// Shift+click extends the existing selection to the click.
		m.sel.head = pos
	} else {
		m.sel = nil
	}
	m.dragging = true
	m.syncViewport()
	return m
}

// handleMouseMotion extends the selection while the left button is
// held (drag). CellMotion mouse mode only reports motion with a
// button down, so every motion event during a drag lands here.
func (m Model) handleMouseMotion(ev tea.Mouse) Model {
	if !m.dragging || m.modalOpen() {
		return m
	}
	pos, ok := m.positionAtCell(ev.X, ev.Y)
	if !ok {
		return m
	}
	if m.sel == nil {
		m.sel = &selection{anchor: m.cursor, head: pos}
	} else {
		m.sel.head = pos
	}
	m.cursor = pos
	m.syncViewport()
	return m
}

// positionAtCell maps a screen cell (x, y) to a buffer position,
// accounting for the one-row header, the viewport scroll offset, the
// line-number gutter and the horizontal scroll. Returns ok=false when
// the cell falls outside the text area (header, footer, beyond the
// last line is clamped to it instead).
func (m Model) positionAtCell(x, y int) (position, bool) {
	const headerRows = 1
	row := y - headerRows
	if row < 0 || row >= m.vp.Height() {
		return position{}, false
	}
	line := row + m.vp.YOffset()
	if line >= m.buf.lineCount() {
		// Click below the last line: land at its end, like every
		// editor does.
		last := m.buf.lineCount() - 1
		return position{line: last, col: m.buf.lineLen(last)}, true
	}
	// The rendered gutter is the line number right-padded by one
	// space (styleGutter Padding(0,1,0,0)).
	gutter := digitsOf(m.buf.lineCount()) + 1
	col := x - gutter + m.xOffset
	if col < 0 {
		col = 0
	}
	if maxCol := m.buf.lineLen(line); col > maxCol {
		col = maxCol
	}
	return position{line: line, col: col}, true
}

// modalOpen reports whether a modal (help, discard/save confirm) is
// capturing input, in which case mouse events should not reach the
// buffer underneath.
func (m Model) modalOpen() bool {
	return m.helpOpen || m.confirmDiscard || m.confirmSave
}

// handleKey routes a KeyMsg through the keymap.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if !m.focused {
		return m, nil
	}
	keyStr := msg.String()

	// Confirmation modal short-circuits everything else: while it is
	// up, only y/n/esc are meaningful.
	if m.confirmDiscard {
		switch keyStr {
		case "y", "Y", "enter":
			return m, emitCancel()
		case "n", "N", "esc":
			m.confirmDiscard = false
			return m, nil
		}
		return m, nil
	}

	// Save confirmation: y commits, n/esc cancels.
	if m.confirmSave {
		switch keyStr {
		case "y", "Y", "enter":
			m.confirmSave = false
			return m.runSaveNow()
		case "n", "N", "esc":
			m.confirmSave = false
			return m, nil
		}
		return m, nil
	}

	// Help overlay: any key closes it.
	if m.helpOpen {
		m.helpOpen = false
		return m, nil
	}

	// Search bar: while up, most keys feed the query or step matches.
	// Editing motions (arrows, etc.) still flow to the buffer so the
	// user can scroll a result into context without closing the bar.
	if m.searchSt != nil {
		return m.handleSearchKey(msg)
	}

	// Completer steals keys while open. If handleKey returns insert,
	// we splice it into the buffer. If keep is false the completer
	// closes; the same keystroke may then need to flow through the
	// normal editor path (see handleCompleterClose).
	if m.completer != nil {
		return m.handleCompleterKey(msg)
	}

	act, ok := m.keymap[keyStr]
	if ok {
		return m.runActionAndMaybeOpenCompleter(act)
	}

	// '?' opens the help overlay regardless of any other state. We
	// check it after the keymap so a user that has rebound '?' to
	// something else still wins, and before printable-character
	// dispatch so '?' is not inserted in the buffer.
	if keyStr == "?" {
		m.helpOpen = true
		return m, nil
	}

	// Not in the keymap: treat as a literal character if it is
	// printable. In bubbletea v2, KeyMsg is an interface; the
	// printable representation of the key lives on Key().Text.
	// Text is empty for non-printables (Tab, Enter, Esc, ...) so
	// we never accidentally insert their byte form.
	if text := msg.Key().Text; text != "" {
		m = m.insertRunes([]rune(text))
		m.completer = openCompleter(&m)
		return m, nil
	}
	return m, nil
}

// handleCompleterKey dispatches a key to the active completer and
// applies its decision. Returns the updated model + any cmd.
func (m Model) handleCompleterKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	keep, cmd, insert := m.completer.handleKey(msg)
	if !keep {
		// Close the completer first; if we have an insertion, do it
		// AFTER closing so the buffer state reflects the final text.
		start := m.completer.triggerStart
		end := m.cursor
		m.completer = nil
		if insert != "" {
			m.pushHistory()
			m.cursor = m.buf.deleteRange(start, end)
			m.sel = nil
			m.cursor.line, m.cursor.col = m.buf.insertString(
				m.cursor.line, m.cursor.col, insert)
			m.dirty = true
			m.validationErrors = nil
			m.syncViewport()
		}
		return m, cmd
	}
	return m, cmd
}

// runActionAndMaybeOpenCompleter wraps runAction so that any mutating
// action gets a chance to open the completer afterwards. This is the
// only path through which the completer is opened.
func (m Model) runActionAndMaybeOpenCompleter(act action) (Model, tea.Cmd) {
	m2, cmd := m.runAction(act)
	if m2.completer == nil && actMaybeOpensCompleter(act) {
		m2.completer = openCompleter(&m2)
	}
	return m2, cmd
}

// actMaybeOpensCompleter reports whether act could have just ended
// typing a trigger. We restrict the check to actions that change the
// text under the cursor; pure motion never opens the completer.
func actMaybeOpensCompleter(act action) bool {
	switch act {
	case actBackspace, actDeleteForward, actEnter, actCut, actPaste:
		return true
	}
	return false
}

// indentUnit is what Tab inserts and Shift+Tab removes: two spaces,
// the YAML indentation step every baifo config file uses.
const indentUnit = "  "

// leadingIndent returns how many runes of indentation (up to one
// indentUnit, or a single tab) sit at the start of line. Used by
// outdent so it never eats non-whitespace.
func leadingIndent(line []rune) int {
	if len(line) > 0 && line[0] == '\t' {
		return 1
	}
	n := 0
	for n < len(indentUnit) && n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}

// runAction dispatches act. Returns (Model, tea.Cmd) so handlers can
// schedule async work (only paste needs that today \u2014 see actPaste).
func (m Model) runAction(act action) (Model, tea.Cmd) {
	// Selecting actions reuse motion code but preserve the anchor.
	if isSelectingAction(act) {
		motion := motionForSelecting(act)
		if motion == actNone && act == actSelectAll {
			m.sel = &selection{
				anchor: position{},
				head:   position{line: m.buf.lineCount() - 1, col: m.buf.lineLen(m.buf.lineCount() - 1)},
			}
			m.cursor = m.sel.head
			m.syncViewport()
			return m, nil
		}
		if m.sel == nil {
			m.sel = &selection{anchor: m.cursor, head: m.cursor}
		}
		newPos := m.applyMotion(motion)
		m.cursor = newPos
		m.sel.head = newPos
		m.syncViewport()
		return m, nil
	}

	// Plain motion collapses any existing selection.
	if motion := motionForCursor(act); motion != actNone {
		m.cursor = m.applyMotion(motion)
		m.sel = nil
		m.syncViewport()
		return m, nil
	}

	switch act {
	case actBackspace:
		m.pushHistory()
		m.deleteSelectionOr(func() {
			m.cursor.line, m.cursor.col = m.buf.deleteRune(m.cursor.line, m.cursor.col)
		})
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actDeleteForward:
		m.pushHistory()
		m.deleteSelectionOr(func() {
			m.cursor.line, m.cursor.col = m.buf.deleteForward(m.cursor.line, m.cursor.col)
		})
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actEnter:
		m.pushHistory()
		if m.sel.active() {
			m.deleteSelection()
		}
		m.cursor.line, m.cursor.col = m.buf.splitLine(m.cursor.line, m.cursor.col)
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actIndent:
		m.pushHistory()
		if m.sel.active() {
			// Indent every line covered by the selection, keeping the
			// selection so the user can hit Tab repeatedly.
			a, b := m.sel.rng()
			last := b.line
			if b.col == 0 && b.line > a.line {
				last-- // selection ending at col 0 doesn't cover that line
			}
			for i := a.line; i <= last; i++ {
				m.buf.insertString(i, 0, indentUnit)
			}
			adjust := func(p *position) {
				if p.line >= a.line && p.line <= last {
					p.col += len(indentUnit)
				}
			}
			adjust(&m.sel.anchor)
			adjust(&m.sel.head)
			m.cursor = m.sel.head
		} else {
			m.cursor.line, m.cursor.col = m.buf.insertString(m.cursor.line, m.cursor.col, indentUnit)
		}
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actOutdent:
		m.pushHistory()
		first, last := m.cursor.line, m.cursor.line
		if m.sel.active() {
			a, b := m.sel.rng()
			first, last = a.line, b.line
			if b.col == 0 && b.line > a.line {
				last--
			}
		}
		for i := first; i <= last; i++ {
			removed := leadingIndent(m.buf.line(i))
			if removed == 0 {
				continue
			}
			m.buf.deleteRange(position{line: i, col: 0}, position{line: i, col: removed})
			if m.sel != nil {
				if m.sel.anchor.line == i {
					m.sel.anchor.col = max(0, m.sel.anchor.col-removed)
				}
				if m.sel.head.line == i {
					m.sel.head.col = max(0, m.sel.head.col-removed)
				}
			}
			if m.cursor.line == i {
				m.cursor.col = max(0, m.cursor.col-removed)
			}
		}
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actDuplicateLine:
		m.pushHistory()
		m.buf.duplicateLine(m.cursor.line)
		m.cursor.line++ // land on the copy, like every editor does
		m.sel = nil
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actDeleteLine:
		m.pushHistory()
		m.buf.deleteLine(m.cursor.line)
		if m.cursor.line >= m.buf.lineCount() {
			m.cursor.line = m.buf.lineCount() - 1
		}
		m.cursor = clampPosition(m.buf, m.cursor)
		m.sel = nil
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actMoveLineUp:
		if m.cursor.line == 0 {
			return m, nil
		}
		m.pushHistory()
		m.buf.swapLines(m.cursor.line, m.cursor.line-1)
		m.cursor.line--
		m.sel = nil
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actMoveLineDown:
		if m.cursor.line >= m.buf.lineCount()-1 {
			return m, nil
		}
		m.pushHistory()
		m.buf.swapLines(m.cursor.line, m.cursor.line+1)
		m.cursor.line++
		m.sel = nil
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, nil

	case actCopy:
		if !m.sel.active() {
			// No selection: copy the whole current line (newline
			// included) — the VS Code convention. Far more useful
			// than a silent no-op.
			return m, tea.SetClipboard(string(m.buf.line(m.cursor.line)) + "\n")
		}
		a, b := m.sel.rng()
		return m, tea.SetClipboard(m.buf.textInRange(a, b))

	case actCut:
		if !m.sel.active() {
			// No selection: cut the whole current line, mirroring
			// the copy behaviour above.
			m.pushHistory()
			text := string(m.buf.line(m.cursor.line)) + "\n"
			m.buf.deleteLine(m.cursor.line)
			if m.cursor.line >= m.buf.lineCount() {
				m.cursor.line = m.buf.lineCount() - 1
			}
			m.cursor = clampPosition(m.buf, m.cursor)
			m.dirty = true
			m.validationErrors = nil
			m.syncViewport()
			return m, tea.SetClipboard(text)
		}
		m.pushHistory()
		a, b := m.sel.rng()
		text := m.buf.textInRange(a, b)
		m.deleteSelection()
		m.dirty = true
		m.validationErrors = nil
		m.syncViewport()
		return m, tea.SetClipboard(text)

	case actPaste:
		// Ask the terminal for its clipboard; the reply comes back as
		// ClipboardMsg, handled in Update above.
		return m, func() tea.Msg { return tea.ReadClipboard() }

	case actUndo:
		m.undo()
		return m, nil

	case actRedo:
		m.redo()
		return m, nil

	case actFind:
		m.startSearch()
		return m, nil

	case actSave:
		if m.requireSave {
			m.confirmSave = true
			return m, nil
		}
		return m.runSaveNow()

	case actCancel:
		if m.dirty {
			m.confirmDiscard = true
			return m, nil
		}
		return m, emitCancel()
	}
	return m, nil
}

// applyMotion translates an action enum into its target position. It
// is the single place where motion knowledge lives; both move-only
// and select-mode go through it so behaviour stays consistent.
func (m *Model) applyMotion(act action) position {
	switch act {
	case actMoveLeft:
		return moveLeft(m.buf, m.cursor)
	case actMoveRight:
		return moveRight(m.buf, m.cursor)
	case actMoveUp:
		return moveUp(m.buf, m.cursor)
	case actMoveDown:
		return moveDown(m.buf, m.cursor)
	case actMoveHome:
		return moveHome(m.buf, m.cursor)
	case actMoveEnd:
		return moveEnd(m.buf, m.cursor)
	case actMoveDocStart:
		return moveDocStart(m.buf, m.cursor)
	case actMoveDocEnd:
		return moveDocEnd(m.buf, m.cursor)
	case actMovePageUp:
		return movePageUp(m.buf, m.cursor, max(1, m.vp.Height()))
	case actMovePageDown:
		return movePageDown(m.buf, m.cursor, max(1, m.vp.Height()))
	case actMoveWordLeft:
		return moveWordLeft(m.buf, m.cursor)
	case actMoveWordRight:
		return moveWordRight(m.buf, m.cursor)
	}
	return m.cursor
}

// motionForCursor returns the equivalent move-action for the move
// actions themselves, or actNone for non-motion actions. Useful for
// keeping the dispatcher's plain-motion branch a one-liner.
func motionForCursor(act action) action {
	switch act {
	case actMoveLeft, actMoveRight, actMoveUp, actMoveDown,
		actMoveHome, actMoveEnd, actMoveDocStart, actMoveDocEnd,
		actMovePageUp, actMovePageDown,
		actMoveWordLeft, actMoveWordRight:
		return act
	}
	return actNone
}

// insertRunes inserts every rune of rs at the cursor, collapsing any
// active selection first. Used for ordinary printable keys.
func (m Model) insertRunes(rs []rune) Model {
	m.pushHistory()
	if m.sel.active() {
		m.deleteSelection()
	}
	for _, r := range rs {
		m.cursor.col = m.buf.insertRune(m.cursor.line, m.cursor.col, r)
	}
	m.dirty = true
	m.validationErrors = nil
	m.syncViewport()
	return m
}

// handlePaste inserts s at the cursor (handling embedded newlines).
// Called both for bracketed paste (PasteMsg) and for clipboard reads.
func (m Model) handlePaste(s string) (Model, tea.Cmd) {
	if s == "" {
		return m, nil
	}
	s = sanitisePaste(s)
	if s == "" {
		return m, nil
	}
	m.pushHistory()
	if m.sel.active() {
		m.deleteSelection()
	}
	m.cursor.line, m.cursor.col = m.buf.insertString(m.cursor.line, m.cursor.col, s)
	m.dirty = true
	m.validationErrors = nil
	m.syncViewport()
	return m, nil
}

// sanitisePaste strips ASCII control characters (except newline and
// tab) from s. Terminals sometimes deliver escape sequences via the
// clipboard — most often when copying highlighted text from another
// pane — and those bytes would corrupt our buffer and break later
// renders (lipgloss measures widths assuming clean text). Newlines
// are preserved so multi-line pastes still split into rows; tabs are
// preserved as users genuinely paste indented YAML.
func sanitisePaste(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue // drop control char
		}
		b.WriteRune(r)
	}
	return b.String()
}

// deleteSelectionOr runs f when no selection is active, otherwise it
// deletes the current selection. Keeps backspace / delete-forward
// concise without duplicating the "if selected, just remove the
// selection" pattern.
func (m *Model) deleteSelectionOr(f func()) {
	if m.sel.active() {
		m.deleteSelection()
		return
	}
	f()
}

// deleteSelection drops the selected runes and moves the cursor to
// the start of the deleted range.
func (m *Model) deleteSelection() {
	a, b := m.sel.rng()
	m.cursor = m.buf.deleteRange(a, b)
	m.sel = nil
	m.cursor = clampPosition(m.buf, m.cursor)
}

// syncViewport keeps the cursor visible: if it moved off-screen, we
// scroll the viewport so it lands inside. Called after every action
// that can move the cursor or change the line count.
func (m *Model) syncViewport() {
	// Refresh the viewport's content BEFORE computing the offset.
	// SetYOffset clamps to maxYOffset(), which the viewport derives
	// from the lines it currently holds. Until we hand it the freshly
	// edited buffer it still thinks it has the old (shorter) content,
	// so a scroll toward a line that a paste/typing just added would
	// be clamped away and the view would refuse to move past what fit
	// before. View() also calls SetContentLines, but that runs on a
	// value receiver (a copy) so it never reaches the real model — the
	// authoritative update has to happen here, on the pointer receiver.
	m.vp.SetContentLines(m.renderLines())

	height := m.vp.Height()
	if height > 0 {
		off := m.vp.YOffset()
		if m.cursor.line < off {
			m.vp.SetYOffset(m.cursor.line)
		} else if m.cursor.line >= off+height {
			m.vp.SetYOffset(m.cursor.line - height + 1)
		}
	}

	// Horizontal scroll. The text area width is the viewport width
	// minus the line-number gutter, which we approximate as
	// max-digit-count + 2 (one space on each side). Cursor must
	// stay inside [xOffset, xOffset+textWidth-1].
	textWidth := m.contentWidth()
	if textWidth <= 0 {
		return
	}
	if m.cursor.col < m.xOffset {
		m.xOffset = m.cursor.col
	} else if m.cursor.col >= m.xOffset+textWidth {
		m.xOffset = m.cursor.col - textWidth + 1
	}
	if m.xOffset < 0 {
		m.xOffset = 0
	}
}

// contentWidth returns how many rune columns of line content fit on
// screen after subtracting the line-number gutter. Conservative on
// purpose: a couple of columns of slack is better than an off-by-one
// that cuts the cursor.
func (m *Model) contentWidth() int {
	w := m.vp.Width()
	if w <= 0 {
		return 0
	}
	gutter := digitsOf(m.buf.lineCount()) + 2 // "NN " (number + space)
	return w - gutter
}

// digitsOf returns the number of decimal digits in n (min 1).
func digitsOf(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		n /= 10
		d++
	}
	return d
}

// emitSave returns a Cmd that fires SaveMsg with the current buffer.
// Wrapped in a closure so the value is captured at call time.
func emitSave(v string) tea.Cmd {
	return func() tea.Msg { return SaveMsg{Value: v} }
}

// emitCancel returns a Cmd that fires CancelMsg.
func emitCancel() tea.Cmd {
	return func() tea.Msg { return CancelMsg{} }
}

// runSaveNow runs OnSave and either emits SaveMsg or stores the
// validation errors. Extracted from runAction so the confirmation
// modal can call it on 'y'.
func (m Model) runSaveNow() (Model, tea.Cmd) {
	errs := m.onSave(m.Value())
	m.validationErrors = errs
	if len(errs) == 0 {
		m.dirty = false
		return m, emitSave(m.Value())
	}
	return m, nil
}

// max returns the larger of a and b. Defined locally because Go 1.21
// has a builtin max but we keep compat with older toolchains the
// project already supports.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// handleSearchKey routes keys while the find-in-buffer bar is open.
// Editing keys (arrows, pgup, pgdn, ...) still pass through so the
// user can browse around without dismissing the search; the bar
// itself consumes Enter (next), Esc (close), and printable runes
// (extend query).
func (m Model) handleSearchKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "esc":
		m.closeSearch()
		return m, nil
	case "enter":
		m.nextMatch()
		return m, nil
	case "backspace":
		if m.searchSt == nil {
			return m, nil
		}
		if len(m.searchSt.query) == 0 {
			return m, nil
		}
		// Trim one rune off the end of the query.
		rs := []rune(m.searchSt.query)
		m.updateQuery(string(rs[:len(rs)-1]))
		return m, nil
	}

	// Navigation keys still work; we forward to the standard keymap
	// so up/down/pgup/pgdn/etc. behave as expected, but we DO NOT
	// invoke the completer or any mutating action while the search
	// bar is up: typing letters extends the query, not the buffer.
	switch keyStr {
	case "up", "down", "left", "right", "home", "end",
		"ctrl+home", "ctrl+end", "pgup", "pgdown":
		if act, ok := m.keymap[keyStr]; ok {
			return m.runAction(act)
		}
		return m, nil
	}

	// n / N step through matches without needing to keep typing.
	if keyStr == "n" {
		m.nextMatch()
		return m, nil
	}
	if keyStr == "N" {
		m.prevMatch()
		return m, nil
	}

	// Any other printable rune extends the query.
	if text := msg.Key().Text; text != "" {
		m.updateQuery(m.searchSt.query + text)
	}
	return m, nil
}
