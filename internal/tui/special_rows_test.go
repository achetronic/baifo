// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"github.com/achetronic/baifo/internal/facade"
)

// TestRefreshGuard_NoticeCarriesSummary verifies the context-guard
// notice row carries the model's compaction summary as its (expandable)
// body, not a boilerplate explanation. The renderer hides it until the
// row is expanded; here we only assert the data is plumbed through.
func TestRefreshGuard_NoticeCarriesSummary(t *testing.T) {
	f := &fakeFacade{guard: facade.ContextGuardStatus{
		Enabled: true, Strategy: "threshold", Percent: 95, Fingerprint: "100:42",
	}}
	m := NewModel(f, false, "v0")
	m.splash = false

	m.refreshGuard(false) // seed
	f.guard.Fingerprint = "20:88"
	f.guard.Summary = "User asked about X; agent did Y and Z."

	m.refreshGuard(true)

	if len(m.messages) != 1 {
		t.Fatalf("expected one notice row, got %d", len(m.messages))
	}
	if m.messages[0].Kind != MessageNotice {
		t.Fatalf("row should be MessageNotice, got %v", m.messages[0].Kind)
	}
	if m.messages[0].Text != "User asked about X; agent did Y and Z." {
		t.Fatalf("notice body should be the summary, got %q", m.messages[0].Text)
	}
}

// TestAgentErrorChunkRendersAsSpecialRow verifies a chunk tagged
// agentError lands as a MessageAgentError row (the special error
// styling) rather than being merged into the root reply text.
func TestAgentErrorChunkRendersAsSpecialRow(t *testing.T) {
	f := &fakeFacade{}
	m := NewModel(f, false, "v0")
	m.splash = false

	updated, _ := m.handleAgentChunk(agentChunkMsg{
		text:       "llm error response: \"overloaded\"",
		agentError: true,
	})
	mm := updated.(Model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected one row, got %d", len(mm.messages))
	}
	if mm.messages[0].Kind != MessageAgentError {
		t.Fatalf("row should be MessageAgentError, got %v", mm.messages[0].Kind)
	}
	if mm.messages[0].Text == "" {
		t.Fatal("agent error row should carry the failure text")
	}
}

// TestAgentErrorAndNoticeRenderWithoutBox is a light smoke test that
// the special rows render to a non-empty string for both kinds (the
// header band path) at a realistic width.
func TestAgentErrorAndNoticeRenderWithoutBox(t *testing.T) {
	f := &fakeFacade{}
	m := NewModel(f, false, "v0")
	m.splash = false
	m.chat.SetSize(80, 24)

	m.messages = []Message{
		{Kind: MessageNotice, Text: "a summary", Expanded: true},
		{Kind: MessageAgentError, Text: "boom"},
	}
	for i := range m.messages {
		out := m.chat.renderMessage(m.messages[i], i, false, false)
		if out == "" {
			t.Fatalf("special row %d rendered empty", i)
		}
	}
}

// TestDebugCommandInjectsSpecialRows verifies the hidden /debug command
// drops the special rows straight into the transcript.
func TestDebugCommandInjectsSpecialRows(t *testing.T) {
	f := &fakeFacade{}
	m := NewModel(f, false, "v0")
	m.splash = false

	res := m.handleDebugCommand([]string{"special"})
	if len(res.injectMessages) != 2 {
		t.Fatalf("expected 2 injected rows, got %d", len(res.injectMessages))
	}
	if res.injectMessages[0].Kind != MessageNotice {
		t.Fatalf("first row should be MessageNotice, got %v", res.injectMessages[0].Kind)
	}
	if res.injectMessages[1].Kind != MessageAgentError {
		t.Fatalf("second row should be MessageAgentError, got %v", res.injectMessages[1].Kind)
	}

	updated, _ := m.applySlashResult(res)
	mm := updated.(Model)
	var gotNotice, gotErr bool
	for _, msg := range mm.messages {
		switch msg.Kind {
		case MessageNotice:
			gotNotice = true
		case MessageAgentError:
			gotErr = true
		}
	}
	if !gotNotice || !gotErr {
		t.Fatalf("applySlashResult did not append both rows (notice=%v err=%v)", gotNotice, gotErr)
	}
}
