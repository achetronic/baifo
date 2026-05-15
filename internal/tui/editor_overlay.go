// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/skills"
	"github.com/achetronic/baifo/internal/tui/components/editor"
	"github.com/achetronic/baifo/internal/tui/components/editor/yamlhl"
	"github.com/achetronic/baifo/internal/tui/overlays"
)

// openEmbeddedEditor opens the editor overlay with the given request.
// If the request includes a SavePath, the file is read from disk to
// seed the buffer; failures fall back to whatever InitialValue carries
// (which defaults to ""), and a system message warns the user.
//
// The editor is wired with a validator appropriate for the request's
// Kind: raw-file edits accept anything; MCP-upsert edits parse and
// validate the buffer before allowing save. Validation errors render
// in the editor's footer; the buffer stays open so the user can fix
// and retry without losing the in-flight edit.
func (m Model) openEmbeddedEditor(req openEditorRequest) (tea.Model, tea.Cmd) {
	initial := req.InitialValue
	if req.SavePath != "" && initial == "" {
		if data, err := os.ReadFile(req.SavePath); err == nil {
			initial = string(data)
		} else if !os.IsNotExist(err) {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("read %s: %v", req.SavePath, err),
			})
			m.chat.SetMessages(m.messages)
		}
	}

	// Build a kind-specific validator. The validator returns the
	// errors that the editor renders in its footer; an empty slice
	// means "save allowed", at which point the editor emits SaveMsg
	// and Update lands in handleEditorSave.
	var validator editor.Validator
	switch req.Kind {
	case editorKindMCPUpsert:
		facade := m.facade
		validator = func(buf string) []error {
			if facade == nil {
				return []error{errors.New("no facade available")}
			}
			return overlays.ValidateMCPYAML(buf)
		}
	case editorKindSkillUpsert:
		validator = func(buf string) []error {
			return skills.ValidateSkillMD([]byte(buf))
		}
	case editorKindAgentUpsert:
		validator = func(buf string) []error {
			return overlays.ValidateAgentYAML(buf)
		}
	case editorKindProviderUpsert:
		validator = func(buf string) []error {
			return overlays.ValidateProviderYAML(buf)
		}
	case editorKindFactUpsert, editorKindFactUpdate:
		validator = func(buf string) []error {
			return overlays.ValidateFactYAML(buf)
		}
	default:
		validator = func(string) []error { return nil }
	}

	// Build triggers: the secret picker is always available because
	// any MCP can reference secrets in its headers. /config edit
	// also benefits if the user types ${secret: in baifo.yaml.
	triggers := map[string]editor.CompletionProvider{
		"${secret:": overlays.SecretCompletionProvider(m.facade),
	}
	// Agent and provider edits also wire in the catwalk-backed
	// model / provider-type pickers so the user doesn't have to
	// memorise the exact spelling of `gemini-2.0-flash` or
	// `anthropic`. The triggers are YAML keys with a trailing
	// space, so they fire only when the cursor is positioned right
	// after `model: ` (or `type: `). Once the trigger fires the
	// editor's substring filter narrows the list as the user
	// keeps typing ("gem" narrows to the Gemini family).
	switch req.Kind {
	case editorKindAgentUpsert:
		// Agents pick a specific model in their llm.model field, and
		// optionally dial reasoning effort in llm.reasoning. The
		// reasoning picker reads the model line above the cursor to
		// tailor the suggested levels to that model.
		triggers["model: "] = overlays.ModelCompletionProvider(m.facade)
		triggers["reasoning: "] = overlays.ReasoningCompletionProvider()
	case editorKindProviderUpsert:
		// Providers only declare endpoint + credentials + type.
		// The model is chosen per-agent, so no model trigger
		// here on purpose.
		triggers["type: "] = overlays.ProviderTypeCompletionProvider()
	}

	ed := editor.New(editor.Options{
		Title:              req.Title,
		InitialValue:       initial,
		LineStyler:         stylerFor(req.Kind, initial),
		OnSave:             validator,
		Triggers:           triggers,
		RequireSaveConfirm: req.Kind == editorKindMCPUpsert || req.Kind == editorKindSkillUpsert || req.Kind == editorKindAgentUpsert || req.Kind == editorKindProviderUpsert || req.Kind == editorKindFactUpsert || req.Kind == editorKindFactUpdate,
		Styles:             canariasEditorStyles(),
	})
	ed.SetSize(m.width, m.height)
	ed.Focus()

	m.editor = &ed
	m.editorOnSavePath = req.SavePath
	m.editorOnSaveKind = req.Kind
	m.editorFactTargetID = req.FactTargetID
	return m, nil
}

// updateWithEditor is the Update branch taken while the editor
// overlay is open. It forwards every message to the embedded editor
// and intercepts SaveMsg / CancelMsg so the overlay can close cleanly.
func (m Model) updateWithEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.editor.SetSize(msg.Width, msg.Height)
		return m, nil

	case editor.SaveMsg:
		return m.handleEditorSave(msg.Value)

	case editor.CancelMsg:
		m.editor = nil
		m.editorOnSavePath = ""
		m.editorOnSaveKind = editorKindRawFile
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: "editor closed without saving",
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	updated, cmd := m.editor.Update(msg)
	m.editor = &updated
	return m, cmd
}

// handleEditorSave dispatches the buffer to the right persistence
// strategy based on editorOnSaveKind. Raw-file edits write through
// os.WriteFile; MCP-upsert edits go through the Facade which handles
// YAML parsing, validation and yamledit-based merge.
func (m Model) handleEditorSave(value string) (tea.Model, tea.Cmd) {
	kind := m.editorOnSaveKind
	path := m.editorOnSavePath
	m.editor = nil
	m.editorOnSavePath = ""
	m.editorOnSaveKind = editorKindRawFile

	switch kind {
	case editorKindMCPUpsert:
		if m.facade == nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "no facade available; MCP not saved",
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		if err := m.facade.UpsertMCPFromDisk(context.Background(), value); err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save MCP: %v", err),
			})
		} else {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: "MCP saved",
			})
			// Upsert triggers a reload inside the facade; mute the
			// generic "config reloaded" follow-up so the user sees
			// one row per save, not two.
			m.suppressNextReloadNotice = true
		}
		m.chat.SetMessages(m.messages)
		return m, nil

	case editorKindSkillUpsert:
		if m.facade == nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "no facade available; skill not saved",
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		if err := m.facade.UpsertSkill(context.Background(), value); err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save skill: %v", err),
			})
		} else {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: "skill saved",
			})
		}
		m.chat.SetMessages(m.messages)
		return m, nil

	case editorKindAgentUpsert:
		if m.facade == nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "no facade available; agent not saved",
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		if err := m.facade.UpsertAgent(context.Background(), value); err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save agent: %v", err),
			})
		} else {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: "agent saved",
			})
			m.suppressNextReloadNotice = true
		}
		m.chat.SetMessages(m.messages)
		return m, nil

	case editorKindProviderUpsert:
		if m.facade == nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "no facade available; provider not saved",
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		if err := m.facade.UpsertProvider(context.Background(), value); err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save provider: %v", err),
			})
		} else {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: "provider saved",
			})
			m.suppressNextReloadNotice = true
		}
		m.chat.SetMessages(m.messages)
		return m, nil

	case editorKindFactUpsert:
		if m.facade == nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "no facade available; fact not saved",
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		content, category, err := overlays.ParseFactYAML(value)
		if err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save fact: %v", err),
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		id, err := m.facade.AddFact(context.Background(), content, category)
		if err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save fact: %v", err),
			})
		} else {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: fmt.Sprintf("stored fact #%d", id),
			})
		}
		m.chat.SetMessages(m.messages)
		return m, nil

	case editorKindFactUpdate:
		if m.facade == nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "no facade available; fact not saved",
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		content, _, err := overlays.ParseFactYAML(value)
		if err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save fact: %v", err),
			})
			m.chat.SetMessages(m.messages)
			return m, nil
		}
		id := m.editorFactTargetID
		if err := m.facade.UpdateFact(context.Background(), id, content); err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("save fact: %v", err),
			})
		} else {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: fmt.Sprintf("updated fact #%d", id),
			})
		}
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	// Default: write raw to path.
	if path == "" {
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: "editor saved (no file target)",
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("write %s: %v", path, err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}
	m.messages = append(m.messages, Message{
		Kind: MessageSystem,
		Time: time.Now(),
		Text: "saved " + path,
	})
	// External file path: a reload follows automatically because
	// the file watcher picks it up. Mute the generic notice so the
	// user gets one row per save, not two.
	m.suppressNextReloadNotice = true
	m.chat.SetMessages(m.messages)
	return m, nil
}

// stylerFor picks the right line styler for the editor based on the
// payload kind. SKILL.md gets a composite styler (yaml frontmatter +
// markdown body); everything else uses plain yaml.
func stylerFor(kind editorKind, initial string) editor.LineStyler {
	if kind == editorKindSkillUpsert {
		return overlays.SkillLineStyler(initial, canariasYAMLTheme(), canariasMDTheme())
	}
	return yamlhl.New(canariasYAMLTheme())
}

// viewEditor renders the editor overlay.
func (m Model) viewEditor() string {
	if m.editor == nil {
		return ""
	}
	return m.editor.View()
}
