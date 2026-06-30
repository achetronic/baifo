// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package overlays

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// SecretPrompt is the masked-input modal baifo shows for /secret set.
// It collects three things: name, value, description. The value
// field uses textinput.EchoPassword so keystrokes render as bullets
// instead of plain text \u2014 critical given that the chat history
// could otherwise be screenshot or scrolled back to.
//
// Lifecycle: created when the user runs /secret set, lives on Model
// while open, dismissed on submit or Esc. Submit fires a tea.Msg
// that the Model translates into a Facade.SetSecret call.
type SecretPrompt struct {
	nameInput  textinput.Model
	valueInput textinput.Model
	descInput  textinput.Model

	// focused is which field is currently active. We rotate through
	// 0 (name) -> 1 (value) -> 2 (desc) on Tab / Enter and back with
	// Shift+Tab. Submit happens on Enter at the description field.
	focused int

	// errorMsg is shown below the inputs when SetSecret fails (e.g.
	// the secrets store is not configured). Cleared on next keypress.
	// Mutated through SetError so callers outside this package can
	// surface a failure without poking private state.
	errorMsg string

	// valueRevealed toggles the value field between EchoPassword
	// (bullets, default) and EchoNormal (clear text). Driven by Ctrl+R
	// so the user can sanity-check what they typed before submitting.
	valueRevealed bool
}

// SetError records the error message rendered below the inputs.
// Exposed so the TUI's secret-overlay handler (in package tui) can
// surface a failed SetSecret without depending on the prompt's
// private state.
func (p *SecretPrompt) SetError(msg string) {
	if p == nil {
		return
	}
	p.errorMsg = msg
}

// SecretSubmitMsg carries the user's input out of the modal. The
// host Model receives this and forwards to Facade.SetSecret.
type SecretSubmitMsg struct {
	Name        string
	Value       string
	Description string
}

// SecretCancelMsg signals the user dismissed the prompt without
// submitting. The host Model just closes the modal.
type SecretCancelMsg struct{}

// NewSecretPrompt returns an open prompt with the given seed name.
// Seed is useful when the user typed `/secret set FOO` so the name
// field comes prefilled and focus jumps straight to the value.
func NewSecretPrompt(seedName string) *SecretPrompt {
	name := textinput.New()
	name.Placeholder = "secret name (e.g. ANTHROPIC_API_KEY)"
	name.Prompt = "name:  "
	name.CharLimit = 64
	name.SetWidth(40)

	value := textinput.New()
	value.Placeholder = "secret value"
	value.Prompt = "value: "
	value.EchoMode = textinput.EchoPassword
	value.EchoCharacter = '•'
	value.CharLimit = 4096
	value.SetWidth(40)

	desc := textinput.New()
	desc.Placeholder = "optional note"
	desc.Prompt = "note:  "
	desc.CharLimit = 200
	desc.SetWidth(40)

	p := &SecretPrompt{
		nameInput:  name,
		valueInput: value,
		descInput:  desc,
	}
	if seedName != "" {
		p.nameInput.SetValue(seedName)
		p.focused = 1
		p.valueInput.Focus()
	} else {
		p.nameInput.Focus()
	}
	return p
}

// Update routes key events. Tab cycles focus, Enter submits when on
// the last field (or jumps focus otherwise), Esc cancels.
func (p *SecretPrompt) Update(msg tea.Msg) (*SecretPrompt, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc":
			return p, func() tea.Msg { return SecretCancelMsg{} }
		case "ctrl+r":
			// Toggle the value field between bullets and clear text
			// so the user can verify what they're typing.
			p.valueRevealed = !p.valueRevealed
			if p.valueRevealed {
				p.valueInput.EchoMode = textinput.EchoNormal
			} else {
				p.valueInput.EchoMode = textinput.EchoPassword
			}
			return p, nil
		case "tab":
			p.cycleFocus(+1)
			return p, nil
		case "shift+tab":
			p.cycleFocus(-1)
			return p, nil
		case "enter":
			if p.focused < 2 {
				p.cycleFocus(+1)
				return p, nil
			}
			// At the last field: submit.
			name := strings.TrimSpace(p.nameInput.Value())
			value := p.valueInput.Value()
			if name == "" {
				p.errorMsg = "name is required"
				return p, nil
			}
			if value == "" {
				p.errorMsg = "value is required"
				return p, nil
			}
			return p, func() tea.Msg {
				return SecretSubmitMsg{
					Name:        name,
					Value:       value,
					Description: strings.TrimSpace(p.descInput.Value()),
				}
			}
		}
	}

	// Default: forward to the focused input so typing works.
	p.errorMsg = ""
	var cmd tea.Cmd
	switch p.focused {
	case 0:
		p.nameInput, cmd = p.nameInput.Update(msg)
	case 1:
		p.valueInput, cmd = p.valueInput.Update(msg)
	case 2:
		p.descInput, cmd = p.descInput.Update(msg)
	}
	return p, cmd
}

// cycleFocus moves the active field by delta (typically +/-1) with
// wraparound. Calling Focus/Blur on each textinput keeps the cursor
// blinking in the right place.
func (p *SecretPrompt) cycleFocus(delta int) {
	p.errorMsg = ""
	// Blur current.
	switch p.focused {
	case 0:
		p.nameInput.Blur()
	case 1:
		p.valueInput.Blur()
	case 2:
		p.descInput.Blur()
	}
	// Move.
	p.focused = (p.focused + delta + 3) % 3
	// Focus next.
	switch p.focused {
	case 0:
		p.nameInput.Focus()
	case 1:
		p.valueInput.Focus()
	case 2:
		p.descInput.Focus()
	}
}

// View renders the modal CONTENTS only (no frame, no border).
// The host wraps the returned string in the shared renderModal
// primitive so the prompt picks up baifo's standard chrome
// (theme.PanelBorderFocused, title band, centring, footer). The
// returned content includes the three input fields stacked with
// blank rows in between and an optional error line; the host
// passes the keybinding hint as the modal's footer.
func (p *SecretPrompt) View() string {
	var b strings.Builder
	b.WriteString(p.nameInput.View())
	b.WriteString("\n")
	b.WriteString(p.valueInput.View())
	b.WriteString("\n")
	b.WriteString(p.descInput.View())
	if p.errorMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("\u26a0 " + p.errorMsg))
	}
	return b.String()
}

// FooterHint returns the keybinding hint string the host should
// pass to the modal frame. Lives on the type so the "[ctrl+r]
// reveal/hide value" toggle text stays in sync with valueRevealed
// — the host doesn't need to inspect internal state.
func (p *SecretPrompt) FooterHint() string {
	reveal := "reveal value"
	if p.valueRevealed {
		reveal = "hide value"
	}
	return "[tab] field · [enter] next/submit · [ctrl+r] " + reveal + " · [esc] cancel"
}
