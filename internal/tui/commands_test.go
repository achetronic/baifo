// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/facade"
)

// recordingFacade extends fakeFacade with knobs for the slash-command
// tests so we can assert which facade method was invoked.
type recordingFacade struct {
	fakeFacade

	newCalled  bool
	switchedTo string
	renamedTo  map[string]string
	deletedIDs []string
	listResp   []facade.SessionInfo
	activeID   string

	// MCP-related test knobs.
	mcpDetails   []facade.MCPDetail
	mcpDeleted   []string
	mcpDeleteErr error

	// Provider-related test knobs.
	providerDetails []facade.ProviderDetail
}

func (r *recordingFacade) SessionID() string { return r.activeID }
func (r *recordingFacade) NewSession(context.Context) (string, error) {
	r.newCalled = true
	return "new-id", nil
}
func (r *recordingFacade) SwitchSession(_ context.Context, id string) error {
	r.switchedTo = id
	return nil
}
func (r *recordingFacade) RenameSession(_ context.Context, id, title string) error {
	if r.renamedTo == nil {
		r.renamedTo = map[string]string{}
	}
	r.renamedTo[id] = title
	return nil
}
func (r *recordingFacade) DeleteSession(_ context.Context, id string) (string, error) {
	r.deletedIDs = append(r.deletedIDs, id)
	if id == r.activeID {
		r.activeID = "after-del"
		return "after-del", nil
	}
	return r.activeID, nil
}
func (r *recordingFacade) ListSessions(context.Context) ([]facade.SessionInfo, error) {
	return r.listResp, nil
}
func (r *recordingFacade) SessionEvents(context.Context, string) ([]facade.Event, error) {
	return nil, nil
}
func (r *recordingFacade) MCPDetails() []facade.MCPDetail           { return r.mcpDetails }
func (r *recordingFacade) ProviderDetails() []facade.ProviderDetail { return r.providerDetails }
func (r *recordingFacade) DeleteMCPFromDisk(_ context.Context, name string) error {
	r.mcpDeleted = append(r.mcpDeleted, name)
	return r.mcpDeleteErr
}

func newModelWith(t *testing.T, facade facade.Facade) Model {
	t.Helper()
	m := NewModel(facade, false, "v0")
	m.splash = false
	return m
}

func TestSlashSessionsNewCreatesSessionAndClearsChat(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	m.messages = []Message{{Kind: MessageUser, Text: "old"}}

	res := m.handleSlashCommand("/session new")
	if !facade.newCalled {
		t.Error("expected NewSession to be called")
	}
	if !res.resetChat {
		t.Error("expected resetChat=true")
	}
	if !strings.Contains(res.systemMessage, "new-id") {
		t.Errorf("system message should include session id: %q", res.systemMessage)
	}
}

func TestSlashSessionsOpensOverlay(t *testing.T) {
	facade := &recordingFacade{
		activeID: "abc",
		listResp: []facade.SessionInfo{
			{ID: "abc", Title: "first", LastAt: "now", MsgCount: 2},
			{ID: "xyz", Title: "", LastAt: "before", MsgCount: 0},
		},
	}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/session list")
	if res.errorMessage != "" {
		t.Fatalf("unexpected error: %q", res.errorMessage)
	}
	if !res.openSessionsOverlay {
		t.Error("/session list should request opening the sessions overlay")
	}
	// No inline text listing any more; the overlay is the listing UX.
	if res.systemMessage != "" {
		t.Errorf("expected empty systemMessage now that the listing moved to an overlay, got %q",
			res.systemMessage)
	}
}

func TestSlashSessionsSwitchCallsFacade(t *testing.T) {
	facade := &recordingFacade{activeID: "old"}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/session switch target")
	if facade.switchedTo != "target" {
		t.Errorf("expected SwitchSession(\"target\"), got %q", facade.switchedTo)
	}
	if !res.resetChat {
		t.Error("switch should reset chat history")
	}
}

func TestSlashSessionsRenameCallsFacade(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/session rename abc Some new title")
	if facade.renamedTo["abc"] != "Some new title" {
		t.Errorf("unexpected rename map: %+v", facade.renamedTo)
	}
	if res.errorMessage != "" {
		t.Errorf("unexpected error: %q", res.errorMessage)
	}
}

func TestSlashSessionsDeleteCallsFacade(t *testing.T) {
	facade := &recordingFacade{activeID: "abc"}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/session delete abc")
	if len(facade.deletedIDs) != 1 || facade.deletedIDs[0] != "abc" {
		t.Errorf("unexpected deleted IDs: %+v", facade.deletedIDs)
	}
	if !res.resetChat {
		t.Error("deleting the active session should reset chat")
	}
}

func TestSlashUnknownReturnsError(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/quux")
	if res.errorMessage == "" {
		t.Error("expected unknown-command error")
	}
}

func TestSlashHelpTogglesHelpOverlay(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/help")
	if !res.toggleHelp {
		t.Error("/help should set toggleHelp=true")
	}
}

func TestSlashQuitSignalsQuit(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/quit")
	if !res.quit {
		t.Error("/quit should set quit=true")
	}
}

// TestSlashSettingsThemeRemoved confirms the theme sub-verb is gone:
// the theme is single and fixed, changed by hand in config, not via a
// runtime command. /settings theme must now be an unknown verb.
func TestSlashSettingsThemeRemoved(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/settings theme cyan")
	if res.errorMessage == "" {
		t.Errorf("/settings theme should be rejected as unknown, got %+v", res)
	}
}

// TestSlashCollectionPluralsOpenSettings checks the collection
// plurals that have no overlay of their own redirect to /settings
// (the unified read-only view of every catalogue).
//
// Note: /session and /worker do NOT belong here — they each
// have their own dedicated overlay.
func TestSlashCollectionPluralsOpenSettings(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	cases := []string{"/skill", "/mcp", "/provider", "/agent", "/fact", "/secret"}
	for _, cmd := range cases {
		res := m.handleSlashCommand(cmd)
		// These commands all delegate to their per-collection
		// handler; the handler with no sub-verb falls through to
		// either an inline listing or the settings overlay. Both
		// modes are OK — what we're checking here is that the
		// dispatcher recognises the command (no "unknown
		// command" error).
		if res.errorMessage != "" {
			t.Errorf("%s reported error %q", cmd, res.errorMessage)
		}
	}
}

func TestSlashSettingsReportsPath(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/settings")
	if res.errorMessage != "" {
		t.Fatalf("unexpected error: %q", res.errorMessage)
	}
	if !strings.Contains(res.systemMessage, "baifo.yaml") {
		t.Errorf("system message should mention baifo.yaml: %q", res.systemMessage)
	}
}

func TestSlashMCPListEmpty(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/mcp")
	if res.errorMessage != "" {
		t.Fatalf("unexpected error: %q", res.errorMessage)
	}
	if res.openCatalog == nil {
		t.Fatalf("expected /mcp to open the catalogue overlay")
	}
	if res.openCatalog.Kind != catalogMCPs {
		t.Errorf("expected MCP catalogue, got kind %d", res.openCatalog.Kind)
	}
	if len(res.openCatalog.Items) != 0 {
		t.Errorf("expected empty catalogue, got %d items", len(res.openCatalog.Items))
	}
}

func TestSlashMCPListRendersDetails(t *testing.T) {
	facade := &recordingFacade{
		mcpDetails: []facade.MCPDetail{
			{Name: "filesystem", Type: "builtin", Endpoint: "filesystem", AuthKind: "none"},
			{Name: "github", Type: "http", Endpoint: "https://api.example.com", AuthKind: "oauth", HasAuth: true},
		},
	}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/mcp list")
	if res.openCatalog == nil {
		t.Fatalf("expected /mcp list to open the catalogue overlay")
	}
	flat := catalogFlat(res.openCatalog)
	if !strings.Contains(flat, "filesystem") {
		t.Errorf("filesystem entry missing: %q", flat)
	}
	if !strings.Contains(flat, "auth=oauth") {
		t.Errorf("oauth marker missing: %q", flat)
	}
}

// catalogFlat flattens a catalogue overlay's rows (label + suffix +
// meta) into a single string for substring assertions.
func catalogFlat(cv *catalogView) string {
	var b strings.Builder
	for _, it := range cv.Items {
		b.WriteString(it.Label)
		b.WriteString(" ")
		b.WriteString(it.Suffix)
		b.WriteString(" ")
		b.WriteString(strings.Join(it.MetaLines, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func TestSlashMCPDeleteCallsFacade(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/mcp delete github")
	if res.errorMessage != "" {
		t.Fatalf("unexpected error: %q", res.errorMessage)
	}
	if len(facade.mcpDeleted) != 1 || facade.mcpDeleted[0] != "github" {
		t.Errorf("facade.DeleteMCPFromDisk not called with 'github', got %v", facade.mcpDeleted)
	}
	if !strings.Contains(res.systemMessage, "deleted MCP github") {
		t.Errorf("unexpected message: %q", res.systemMessage)
	}
}

func TestSlashMCPDeleteMissingName(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/mcp delete")
	if res.errorMessage == "" {
		t.Fatalf("expected error for missing NAME, got systemMessage=%q", res.systemMessage)
	}
}

func TestSlashMCPUnknownVerb(t *testing.T) {
	facade := &recordingFacade{}
	m := newModelWith(t, facade)
	res := m.handleSlashCommand("/mcp wat")
	if res.errorMessage == "" {
		t.Fatalf("expected error for unknown verb")
	}
}
