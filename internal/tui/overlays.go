// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// overlays.go owns the per-overlay key dispatchers for the
// sessions and workers overlays. Settings has its own dispatcher
// because it covers six sections; the two here are simpler and
// could share code, but keeping them separate makes the per-list
// clamps explicit and the future "two-pane detail" easier to
// retrofit without contorting a shared helper.
//
// Conventions every overlay enforces:
//
//   - Esc closes the overlay (returns focus to the chat).
//   - ↑/↓/PgUp/PgDn/Home/End move the selection.
//   - Enter triggers the primary action (resume / talk-to-worker).
//   - n / d / k / c are destructive shortcuts where applicable,
//     gated by a y/N confirmation prompt.

// overlayPageStep is how many rows PgUp/PgDn jump in the list-
// based overlays. Roughly matches the visible window size
// (listOverlayMinRows) so a single press moves the selection one
// "screenful" inside the modal.
const overlayPageStep = 10

// handleSessionsOverlayKey owns keystrokes while the /session
// overlay is open. The overlay shares its renderer with the
// legacy Sessions tab — same look, same shortcuts, just floating
// over the chat.
func (m Model) handleSessionsOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.sessionsConfirmDelete != "" {
		switch key {
		case "y", "Y":
			id := m.sessionsConfirmDelete
			m.sessionsConfirmDelete = ""
			return m.runDeleteSession(id)
		case "n", "N", "esc":
			m.sessionsConfirmDelete = ""
			return m, nil
		}
		return m, nil
	}
	switch key {
	case "esc":
		m.sessionsOpen = false
		return m, nil
	case "up":
		if m.sessionsSel > 0 {
			m.sessionsSel--
		}
		return m, nil
	case "down":
		if m.sessionsSel < len(m.sessions)-1 {
			m.sessionsSel++
		}
		return m, nil
	case "pgup":
		m.sessionsSel -= overlayPageStep
		if m.sessionsSel < 0 {
			m.sessionsSel = 0
		}
		return m, nil
	case "pgdown":
		m.sessionsSel += overlayPageStep
		if m.sessionsSel > len(m.sessions)-1 {
			m.sessionsSel = len(m.sessions) - 1
		}
		if m.sessionsSel < 0 {
			m.sessionsSel = 0
		}
		return m, nil
	case "home":
		m.sessionsSel = 0
		return m, nil
	case "end":
		m.sessionsSel = len(m.sessions) - 1
		return m, nil
	case "enter":
		if m.sessionsSel < len(m.sessions) {
			m.sessionsOpen = false // close before resume so the chat repaints
			return m.runResumeSession(m.sessions[m.sessionsSel].ID)
		}
	case "n", "N":
		m.sessionsOpen = false
		return m.runNewSession()
	case "d", "D":
		if m.sessionsSel < len(m.sessions) {
			m.sessionsConfirmDelete = m.sessions[m.sessionsSel].ID
		}
		return m, nil
	case "r", "R":
		// Open the embedded editor pre-filled with the current
		// title; on save it goes through editorKindSessionRename and
		// calls Facade.RenameSession. The overlay closes first so
		// the editor takes over cleanly (issue #2: the key used to
		// just print the CLI equivalent into the chat).
		if m.sessionsSel < len(m.sessions) {
			sess := m.sessions[m.sessionsSel]
			m.sessionsOpen = false
			return m.openEmbeddedEditor(openEditorRequest{
				Title:           "Rename session",
				InitialValue:    sess.Title,
				Kind:            editorKindSessionRename,
				SessionTargetID: sess.ID,
			})
		}
		return m, nil
	}
	return m, nil
}

// handleWorkersOverlayKey owns keystrokes while the /worker
// overlay is open. The shortcuts mirror the per-row actions
// available through the /worker slash verbs (talk, kill,
// collect), so the user can either browse via the overlay or
// drive everything from the command bar — both produce the same
// outcomes.
func (m Model) handleWorkersOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.workersConfirmKill != "" {
		switch key {
		case "y", "Y":
			id := m.workersConfirmKill
			m.workersConfirmKill = ""
			return m.runKillWorker(id)
		case "n", "N", "esc":
			m.workersConfirmKill = ""
			return m, nil
		}
		return m, nil
	}
	if m.workersConfirmCollect != "" {
		switch key {
		case "y", "Y":
			id := m.workersConfirmCollect
			m.workersConfirmCollect = ""
			return m.runCollectWorker(id)
		case "n", "N", "esc":
			m.workersConfirmCollect = ""
			return m, nil
		}
		return m, nil
	}
	switch key {
	case "esc":
		m.workersOpen = false
		return m, nil
	case "up":
		if m.workersSel > 0 {
			m.workersSel--
		}
		return m, nil
	case "down":
		if m.workersSel < len(m.workers)-1 {
			m.workersSel++
		}
		return m, nil
	case "pgup":
		m.workersSel -= overlayPageStep
		if m.workersSel < 0 {
			m.workersSel = 0
		}
		return m, nil
	case "pgdown":
		m.workersSel += overlayPageStep
		if m.workersSel > len(m.workers)-1 {
			m.workersSel = len(m.workers) - 1
		}
		if m.workersSel < 0 {
			m.workersSel = 0
		}
		return m, nil
	case "home":
		m.workersSel = 0
		return m, nil
	case "end":
		m.workersSel = len(m.workers) - 1
		return m, nil
	case "enter":
		if m.workersSel < len(m.workers) {
			worker := m.workers[m.workersSel]
			m.workersOpen = false
			return m.switchInterlocutor(worker.ID)
		}
	case "k":
		if m.workersSel < len(m.workers) {
			m.workersConfirmKill = m.workers[m.workersSel].ID
		}
		return m, nil
	case "c":
		if m.workersSel < len(m.workers) {
			m.workersConfirmCollect = m.workers[m.workersSel].ID
		}
		return m, nil
	}
	return m, nil
}

// handleFactsOverlayKey owns keystrokes while the /fact overlay is
// open. The shortcuts mirror the /fact slash verbs (edit, delete,
// add) so the user can either browse via the overlay or drive
// everything from the command bar — both produce the same outcomes.
func (m Model) handleFactsOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.factsConfirmDelete != 0 {
		switch key {
		case "y", "Y":
			id := m.factsConfirmDelete
			m.factsConfirmDelete = 0
			return m.runDeleteFact(id)
		case "n", "N", "esc":
			m.factsConfirmDelete = 0
			return m, nil
		}
		return m, nil
	}
	switch key {
	case "esc":
		m.factsOpen = false
		return m, nil
	case "up":
		if m.factsSel > 0 {
			m.factsSel--
		}
		return m, nil
	case "down":
		if m.factsSel < len(m.facts)-1 {
			m.factsSel++
		}
		return m, nil
	case "pgup":
		m.factsSel -= overlayPageStep
		if m.factsSel < 0 {
			m.factsSel = 0
		}
		return m, nil
	case "pgdown":
		m.factsSel += overlayPageStep
		if m.factsSel > len(m.facts)-1 {
			m.factsSel = len(m.facts) - 1
		}
		if m.factsSel < 0 {
			m.factsSel = 0
		}
		return m, nil
	case "home":
		m.factsSel = 0
		return m, nil
	case "end":
		m.factsSel = len(m.facts) - 1
		return m, nil
	case "enter", "e", "E":
		// Primary action: edit the focused fact in the embedded
		// editor, exactly like /fact edit ID. The overlay closes
		// first so the editor takes over cleanly; on save the
		// update goes through the same editorKindFactUpdate flow.
		if m.factsSel < len(m.facts) {
			fact := m.facts[m.factsSel]
			current, _, err := m.facade.FactContent(fact.ID)
			if err != nil {
				m.messages = append(m.messages, Message{
					Kind: MessageError,
					Time: time.Now(),
					Text: fmt.Sprintf("fact edit: %v", err),
				})
				m.chat.SetMessages(m.messages)
				return m, nil
			}
			m.factsOpen = false
			return m.openEmbeddedEditor(openEditorRequest{
				Title:        fmt.Sprintf("Edit fact #%d", fact.ID),
				InitialValue: factEditScaffold(current),
				Kind:         editorKindFactUpdate,
				FactTargetID: fact.ID,
			})
		}
	case "n", "N", "a", "A":
		// Add a new fact via the editor — same flow as /fact add
		// without inline content.
		m.factsOpen = false
		return m.startNewFact()
	case "d", "D":
		if m.factsSel < len(m.facts) {
			m.factsConfirmDelete = m.facts[m.factsSel].ID
		}
		return m, nil
	}
	return m, nil
}

// runDeleteFact deletes the given fact, refreshes the overlay's
// backing list in place (the overlay stays open so the user can keep
// pruning), and appends a confirmation row to the chat.
func (m Model) runDeleteFact(id uint64) (tea.Model, tea.Cmd) {
	if m.facade == nil {
		return m, nil
	}
	if err := m.facade.DeleteFact(context.Background(), id); err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("fact delete: %v", err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}
	m.facts = m.facade.FactDetails()
	if m.factsSel >= len(m.facts) {
		m.factsSel = len(m.facts) - 1
	}
	if m.factsSel < 0 {
		m.factsSel = 0
	}
	m.messages = append(m.messages, Message{
		Kind: MessageSystem,
		Time: time.Now(),
		Text: fmt.Sprintf("deleted fact #%d", id),
	})
	m.chat.SetMessages(m.messages)
	return m, nil
}
