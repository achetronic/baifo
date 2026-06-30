// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"strings"
	"testing"
)

// newCompleterTestModel builds a Model wired with two triggers used
// by the completer tests. The secretProvider returns a small list
// of known names; the typeProvider returns the MCP type values.
func newCompleterTestModel(t *testing.T, initial string) Model {
	t.Helper()
	secretProvider := func(_ string, _ CompletionContext) []Completion {
		return []Completion{
			{View: "GITHUB_OAUTH_SECRET", Insert: "GITHUB_OAUTH_SECRET"},
			{View: "GITLAB_TOKEN", Insert: "GITLAB_TOKEN"},
			{View: "OPENAI_API_KEY", Insert: "OPENAI_API_KEY"},
		}
	}
	m := New(Options{
		Title:        "Test",
		InitialValue: initial,
		Triggers: map[string]CompletionProvider{
			"${secret:": secretProvider,
		},
	})
	m.SetSize(80, 20)
	return m
}

func TestCompleter_OpensOnTriggerSubstring(t *testing.T) {
	m := newCompleterTestModel(t, "")
	// Type "${secret:" rune by rune. The completer should open as
	// soon as the full trigger is matched.
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if m.completer == nil {
		t.Fatalf("completer should be open after typing the trigger")
	}
	if len(m.completer.filtered) != 3 {
		t.Errorf("expected 3 items, got %d", len(m.completer.filtered))
	}
}

func TestCompleter_FilterByPrefix(t *testing.T) {
	m := newCompleterTestModel(t, "")
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	// Now type "git" → only GITHUB_OAUTH_SECRET and GITLAB_TOKEN match.
	for _, r := range "git" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if m.completer == nil {
		t.Fatalf("completer should still be open while filtering")
	}
	if len(m.completer.filtered) != 2 {
		t.Errorf("expected 2 filtered items, got %d", len(m.completer.filtered))
	}
}

func TestCompleter_EnterInsertsSelected(t *testing.T) {
	m := newCompleterTestModel(t, "ref: ")
	// Position cursor at end of line.
	m, _ = m.Update(press("end", ""))
	// Type the trigger.
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	// Move down once then Enter → second item (GITLAB_TOKEN).
	m, _ = m.Update(press("down", ""))
	m, _ = m.Update(press("enter", ""))

	if m.completer != nil {
		t.Errorf("completer should be closed after Enter")
	}
	if got := m.Value(); got != "ref: GITLAB_TOKEN" {
		t.Errorf("value: got %q, want %q", got, "ref: GITLAB_TOKEN")
	}
}

func TestCompleter_EscClosesWithoutInsert(t *testing.T) {
	m := newCompleterTestModel(t, "")
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if m.completer == nil {
		t.Fatalf("setup: completer not open")
	}
	m, _ = m.Update(press("esc", ""))
	if m.completer != nil {
		t.Errorf("esc should close the completer")
	}
	// Buffer should keep the trigger text the user typed.
	if got := m.Value(); got != "${secret:" {
		t.Errorf("value: got %q, want %q", got, "${secret:")
	}
}

func TestCompleter_DownWrapsAround(t *testing.T) {
	m := newCompleterTestModel(t, "")
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	// Three items, indexes 0..2. Press down three times → back to 0.
	m, _ = m.Update(press("down", ""))
	m, _ = m.Update(press("down", ""))
	m, _ = m.Update(press("down", ""))
	if m.completer.selected != 0 {
		t.Errorf("selected: got %d, want 0 (wrap)", m.completer.selected)
	}
}

func TestCompleter_BackspaceOnEmptyPrefixCloses(t *testing.T) {
	m := newCompleterTestModel(t, "")
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	// Empty prefix + backspace → close completer.
	m, _ = m.Update(press("backspace", ""))
	if m.completer != nil {
		t.Errorf("backspace at empty prefix should close completer")
	}
}

func TestCompleter_NoOpWhenNoTriggers(t *testing.T) {
	// Editor with no triggers should never open a completer.
	m := New(Options{InitialValue: ""})
	m.SetSize(80, 20)
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if m.completer != nil {
		t.Errorf("completer should not open without registered triggers")
	}
}

func TestCompleter_FilterIsCaseInsensitive(t *testing.T) {
	m := newCompleterTestModel(t, "")
	for _, r := range "${secret:" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	for _, r := range "openai" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if m.completer == nil {
		t.Fatalf("completer should still be open")
	}
	if len(m.completer.filtered) != 1 {
		t.Errorf("expected 1 match for 'openai', got %d", len(m.completer.filtered))
	}
	if !strings.Contains(m.completer.filtered[0].View, "OPENAI") {
		t.Errorf("filter should match OPENAI_API_KEY")
	}
}
