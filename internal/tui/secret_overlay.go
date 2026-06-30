// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/tui/overlays"
)

// updateWithSecretPrompt routes messages while the secret-set modal
// is open. Keys go to the prompt; submit and cancel messages are
// intercepted here so the Model can talk to the Facade.
func (m Model) updateWithSecretPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case overlays.SecretSubmitMsg:
		// Try to persist. On error, keep the modal open and surface
		// the message inside the prompt so the user can fix and retry.
		if err := m.facade.SetSecret(context.Background(), v.Name, v.Value, v.Description); err != nil {
			if m.secretPrompt != nil {
				m.secretPrompt.SetError(err.Error())
			}
			return m, nil
		}
		m.secretPrompt = nil
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: fmt.Sprintf("stored secret %q", v.Name),
		})
		m.chat.SetMessages(m.messages)
		return m, nil

	case overlays.SecretCancelMsg:
		m.secretPrompt = nil
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: "secret prompt cancelled",
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	// Forward everything else to the prompt component.
	prompt, cmd := m.secretPrompt.Update(msg)
	m.secretPrompt = prompt
	return m, cmd
}

// viewSecretPromptOverlay wraps the secret prompt's content in
// the shared renderModal primitive so it picks up baifo's standard
// modal chrome (border, title band, footer, centring) and stays
// visually consistent with the rest of the TUI.
func (m Model) viewSecretPromptOverlay(back string) string {
	if m.secretPrompt == nil {
		return back
	}
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return renderModal(m.theme, overlayOpts{
		Title:    "Set secret",
		Content:  m.secretPrompt.View(),
		Footer:   m.secretPrompt.FooterHint(),
		MinWidth: 60,
		MaxWidth: 80,
	}, back, w, h)
}
