// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/facade"
)

// fakeFacade is an facade.Facade stub for TUI tests. It records the
// SendMessage call and replies with a canned text.
type fakeFacade struct {
	lastText string
	reply    string
	err      error

	// extraEvents is an optional list of events emitted before the
	// canned reply, used to exercise tool-call rendering.
	extraEvents []*facade.Event

	// guard is returned verbatim by ContextGuardStatus so tests can
	// drive the footer gauge and the compaction notice.
	guard facade.ContextGuardStatus
}

func (f *fakeFacade) SendMessage(_ context.Context, text string) iter.Seq2[*facade.Event, error] {
	f.lastText = text
	reply := f.reply
	err := f.err
	events := f.extraEvents
	return func(yield func(*facade.Event, error) bool) {
		if err != nil {
			yield(nil, err)
			return
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		yield(&facade.Event{Text: reply}, nil)
	}
}

func (f *fakeFacade) RootName() string                                           { return "baifo" }
func (f *fakeFacade) RootBuildError() error                                      { return nil }
func (f *fakeFacade) ConfigDir() string                                          { return "/tmp/.baifo" }
func (f *fakeFacade) ModelName() string                                          { return "fake/fake-model-1" }
func (f *fakeFacade) SessionID() string                                          { return "fake-session" }
func (f *fakeFacade) ListSessions(context.Context) ([]facade.SessionInfo, error) { return nil, nil }
func (f *fakeFacade) NewSession(context.Context) (string, error)                 { return "new", nil }
func (f *fakeFacade) SwitchSession(context.Context, string) error                { return nil }
func (f *fakeFacade) RenameSession(context.Context, string, string) error        { return nil }
func (f *fakeFacade) DeleteSession(context.Context, string) (string, error)      { return "after-del", nil }
func (f *fakeFacade) SessionEvents(context.Context, string) ([]facade.Event, error) {
	return nil, nil
}
func (f *fakeFacade) ListWorkers() []facade.WorkerInfo                      { return nil }
func (f *fakeFacade) KillWorker(string, string) error                       { return nil }
func (f *fakeFacade) CollectWorker(context.Context, string) (string, error) { return "", nil }
func (f *fakeFacade) SubscribeWorker(string) ([]facade.WorkerStreamEvent, <-chan facade.WorkerStreamEvent, func(), error) {
	ch := make(chan facade.WorkerStreamEvent)
	close(ch)
	return nil, ch, func() {}, nil
}
func (f *fakeFacade) SendToWorker(context.Context, string, string) error { return nil }
func (f *fakeFacade) SubscribeWorkerLifecycle() (<-chan facade.WorkerLifecycleEvent, func()) {
	ch := make(chan facade.WorkerLifecycleEvent)
	close(ch)
	return ch, func() {}
}
func (f *fakeFacade) ListSkills() []string                                { return nil }
func (f *fakeFacade) ListMCPs() []string                                  { return nil }
func (f *fakeFacade) ListProviders() []string                             { return nil }
func (f *fakeFacade) ListSecretNames() []string                           { return nil }
func (f *fakeFacade) ListAgentTemplates() []string                        { return nil }
func (f *fakeFacade) ListFacts() []string                                 { return nil }
func (f *fakeFacade) MCPDetails() []facade.MCPDetail                      { return nil }
func (f *fakeFacade) MCPYAML(string) (string, error)                      { return "", nil }
func (f *fakeFacade) MCPScaffold(string) string                           { return "" }
func (f *fakeFacade) UpsertMCPFromDisk(context.Context, string) error     { return nil }
func (f *fakeFacade) DeleteMCPFromDisk(context.Context, string) error     { return nil }
func (f *fakeFacade) AuthenticateMCP(context.Context, string, bool) error { return nil }
func (f *fakeFacade) TestMCPConnection(context.Context, string) (string, error) {
	return "✓ connected", nil
}
func (f *fakeFacade) ClearMCPAuth(context.Context, string) error              { return nil }
func (f *fakeFacade) SkillDetails() []facade.SkillDetail                      { return nil }
func (f *fakeFacade) SkillContent(string) (string, error)                     { return "", nil }
func (f *fakeFacade) SkillScaffold(string) string                             { return "" }
func (f *fakeFacade) UpsertSkill(context.Context, string) error               { return nil }
func (f *fakeFacade) DeleteSkill(context.Context, string) error               { return nil }
func (f *fakeFacade) InstallSkill(context.Context, string) (string, error)    { return "", nil }
func (f *fakeFacade) AgentDetails() []facade.AgentDetail                      { return nil }
func (f *fakeFacade) AgentYAML(string) (string, error)                        { return "", nil }
func (f *fakeFacade) AgentScaffold(string) string                             { return "" }
func (f *fakeFacade) UpsertAgent(context.Context, string) error               { return nil }
func (f *fakeFacade) DeleteAgent(context.Context, string) error               { return nil }
func (f *fakeFacade) SetRootAgent(context.Context, string) error              { return nil }
func (f *fakeFacade) ProviderDetails() []facade.ProviderDetail                { return nil }
func (f *fakeFacade) ProviderYAML(string) (string, error)                     { return "", nil }
func (f *fakeFacade) ProviderScaffold(string) string                          { return "" }
func (f *fakeFacade) UpsertProvider(context.Context, string) error            { return nil }
func (f *fakeFacade) DeleteProvider(context.Context, string) error            { return nil }
func (f *fakeFacade) SetSecret(context.Context, string, string, string) error { return nil }
func (f *fakeFacade) DeleteSecret(context.Context, string) error              { return nil }
func (f *fakeFacade) SecretsEncrypted() bool                                  { return false }
func (f *fakeFacade) EncodeSecrets(context.Context) (int, error)              { return 0, nil }
func (f *fakeFacade) DecodeSecrets(context.Context) (int, error)              { return 0, nil }
func (f *fakeFacade) FactDetails() []facade.FactDetail                        { return nil }
func (f *fakeFacade) AddFact(context.Context, string, string) (uint64, error) { return 0, nil }
func (f *fakeFacade) DeleteFact(context.Context, uint64) error                { return nil }
func (f *fakeFacade) UpdateFact(context.Context, uint64, string) error        { return nil }
func (f *fakeFacade) FactContent(uint64) (string, string, error)              { return "", "", nil }
func (f *fakeFacade) ReloadFromDisk(context.Context) error                    { return nil }
func (f *fakeFacade) SubscribeReload() <-chan facade.ReloadEvent              { return nil }
func (f *fakeFacade) Close() error                                            { return nil }
func (f *fakeFacade) ContextGuardStatus(context.Context) facade.ContextGuardStatus {
	return f.guard
}

// driveModel applies a sequence of messages to the model and returns
// the final state. Each step calls Update with the given message; any
// returned Cmd is executed synchronously and its tea.Msg fed back in.
// This is enough for messages that don't need a real renderer.
func driveModel(t *testing.T, m tea.Model, msgs ...tea.Msg) tea.Model {
	t.Helper()
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

// runCmd executes a tea.Cmd and feeds its result back to the model.
// Used to drive the streaming work after submitComposer scheduled it.
func runCmd(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	m, _ = m.Update(msg)
	return m
}

// drainStream executes the initial cmd and follows every chained
// agentChunkMsg.next until done. Use this whenever a test triggers
// a streaming run and wants to observe its final state.
func drainStream(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == nil {
			continue
		}
		msg := cur()
		if msg == nil {
			continue
		}
		// tea.Batch expands into a slice of Cmds; enqueue them
		// individually so streaming and ticker live side by side.
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		var nextCmd tea.Cmd
		m, nextCmd = m.Update(msg)
		if nextCmd != nil {
			queue = append(queue, nextCmd)
		}
		if chunk, ok := msg.(agentChunkMsg); ok && chunk.done {
			return m
		}
	}
	return m
}

func TestModelInitReturnsSplashTickCommand(t *testing.T) {
	model := NewModel(&fakeFacade{}, false, "v0")
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Init should return the splash-dismiss tick")
	}
}

func TestModelDismissesSplashOnDoneMessage(t *testing.T) {
	model := NewModel(&fakeFacade{}, false, "v0")
	m := driveModel(t, model, splashDoneMsg{}).(Model)
	if m.splash {
		t.Error("splash should be dismissed after splashDoneMsg")
	}
}

func TestModelHelpToggle(t *testing.T) {
	model := NewModel(&fakeFacade{}, false, "v0")
	m := driveModel(t, model, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}).(Model)
	if !m.helpOpen {
		t.Error("ctrl+/ should open help overlay")
	}
	m = driveModel(t, m, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}).(Model)
	if m.helpOpen {
		t.Error("ctrl+/ should close help overlay")
	}
}

func TestSendingMessageProducesUserAndRootRows(t *testing.T) {
	facade := &fakeFacade{reply: "hi back"}
	model := NewModel(facade, false, "v0")
	model.splash = false // skip splash for the test

	// Type some text into the composer.
	model.composer.ta.SetValue("hello")

	// Trigger submit (Enter on Chat tab).
	m, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected streaming cmd, got nil")
	}

	// Drive the cmd so the streaming msgs flow back in.
	m = drainStream(t, m, cmd)

	final := m.(Model)
	if len(final.messages) != 2 {
		t.Fatalf("messages: got %d, want 2 (user + root)", len(final.messages))
	}
	if final.messages[0].Kind != MessageUser || final.messages[0].Text != "hello" {
		t.Errorf("user row: %+v", final.messages[0])
	}
	if final.messages[1].Kind != MessageRoot || final.messages[1].Text != "hi back" {
		t.Errorf("root row: %+v", final.messages[1])
	}
	if facade.lastText != "hello" {
		t.Errorf("facade did not receive the text: %q", facade.lastText)
	}
}

func TestSendingMessageWithoutFacadeShowsError(t *testing.T) {
	model := NewModel(nil, false, "v0")
	model.splash = false
	model.composer.ta.SetValue("anything")

	m, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	final := m.(Model)
	if len(final.messages) != 1 || final.messages[0].Kind != MessageError {
		t.Fatalf("expected one error message, got %+v", final.messages)
	}
	if !strings.Contains(final.messages[0].Text, "No agent configured") {
		t.Errorf("error text: %q", final.messages[0].Text)
	}
}

func TestStreamingErrorIsSurfaced(t *testing.T) {
	facade := &fakeFacade{err: errors.New("boom")}
	model := NewModel(facade, false, "v0")
	model.splash = false
	model.composer.ta.SetValue("hi")

	m, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = drainStream(t, m, cmd)
	final := m.(Model)
	// Expect user msg + error.
	if len(final.messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(final.messages))
	}
	if final.messages[1].Kind != MessageError {
		t.Errorf("expected error row, got %v", final.messages[1].Kind)
	}
}

func TestEscCancelsStreamingWhenActive(t *testing.T) {
	facade := &fakeFacade{reply: "later"}
	model := NewModel(facade, false, "v0")
	model.splash = false
	model.composer.ta.SetValue("hi")
	m, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Now simulate Esc; should clear streamCancel.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	final := m.(Model)
	if final.streamCancel != nil {
		t.Error("streamCancel should be nil after Esc")
	}
}

// Compile-time check: the fake facade satisfies facade.Facade.
var _ facade.Facade = (*fakeFacade)(nil)

// TestToolCallAndResultRenderAsSeparateMessages exercises the wiring
// from a streamed Event with ToolCalls/ToolResults all the way to
// distinct chat rows. The current decision is that call and result
// each get their own row — no in-place merging — so the user can see
// the agent's trace as it unfolds.
func TestToolCallAndResultRenderAsSeparateMessages(t *testing.T) {
	facade := &fakeFacade{
		reply: "done",
		extraEvents: []*facade.Event{
			{ToolCalls: []facade.ToolCallInfo{{
				CallID: "call-1",
				Name:   "filesystem.read_file",
				Args:   map[string]any{"path": "/tmp/x"},
			}}},
			{ToolResults: []facade.ToolResultInfo{{
				CallID: "call-1",
				Name:   "filesystem.read_file",
				Result: map[string]any{"bytes": 42},
			}}},
		},
	}
	model := NewModel(facade, false, "v0")
	model.splash = false
	model.composer.ta.SetValue("go")

	m, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected streaming cmd, got nil")
	}
	m = drainStream(t, m, cmd)
	final := m.(Model)

	// Expect: user + tool call + tool result + root reply = 4 rows.
	if len(final.messages) != 4 {
		t.Fatalf("messages: got %d (%+v), want 4", len(final.messages), final.messages)
	}
	if final.messages[0].Kind != MessageUser {
		t.Errorf("row 0 kind = %v, want MessageUser", final.messages[0].Kind)
	}
	if final.messages[1].Kind != MessageToolCall {
		t.Errorf("row 1 kind = %v, want MessageToolCall", final.messages[1].Kind)
	}
	if final.messages[1].ToolName != "filesystem.read_file" {
		t.Errorf("row 1 ToolName = %q", final.messages[1].ToolName)
	}
	if final.messages[1].ToolArgs["path"] != "/tmp/x" {
		t.Errorf("row 1 ToolArgs missing path: %+v", final.messages[1].ToolArgs)
	}
	if final.messages[2].Kind != MessageToolResult {
		t.Errorf("row 2 kind = %v, want MessageToolResult", final.messages[2].Kind)
	}
	if final.messages[2].ToolCallID != "call-1" {
		t.Errorf("row 2 ToolCallID = %q", final.messages[2].ToolCallID)
	}
	if final.messages[3].Kind != MessageRoot || final.messages[3].Text != "done" {
		t.Errorf("row 3: %+v", final.messages[3])
	}
}

// Compile-time check: Model satisfies tea.Model.
var _ tea.Model = Model{}

// quietRandomization avoids the splash logo varying between test runs.
var _ = func() any {
	_ = time.Now()
	return nil
}()

// TestClickOnToolRowExpandsIt simulates a left mouse click on the
// row of a tool call and verifies (a) the chat receives focus, (b)
// chatSel points to the tool, (c) the tool's Expanded flag flips
// to true. Reproduces the bug report "clicking the tool doesn't
// expand anything".
//
// The test builds a known message slice, lets the chat render it
// at a known size (which populates rowSpans), then fires a
// MouseClickMsg with coordinates that land on the tool row.
func TestClickOnToolRowExpandsIt(t *testing.T) {
	facade := &fakeFacade{}
	model := NewModel(facade, false, "v0")
	model.splash = false
	// Window large enough to fit a few rows of chat.
	wsz := tea.WindowSizeMsg{Width: 80, Height: 40}
	mAfterSize, _ := model.Update(wsz)
	model = mAfterSize.(Model)

	// Seed three messages: a user row, a tool call, a root reply.
	now := time.Now()
	model.messages = []Message{
		{Kind: MessageUser, Time: now, Text: "do the thing"},
		{Kind: MessageToolCall, Time: now, ToolName: "x.read_file",
			ToolCallID: "c1", ToolArgs: map[string]any{"path": "/x"}},
		{Kind: MessageToolResult, Time: now, ToolName: "x.read_file",
			ToolCallID: "c1", ToolResult: map[string]any{"bytes": 12}},
		{Kind: MessageRoot, Time: now, Text: "done"},
	}
	model.chat.SetMessages(model.messages)

	// rowSpans is populated by SetMessages. Sanity check.
	if len(model.chat.rowSpans) != len(model.messages) {
		t.Fatalf("rowSpans len = %d, want %d",
			len(model.chat.rowSpans), len(model.messages))
	}
	// The tool call (index 1) must own a non-empty span.
	toolSpan := model.chat.rowSpans[1]
	if toolSpan[0] == toolSpan[1] {
		t.Fatalf("tool row got empty span %v — it was consumed?", toolSpan)
	}

	// Compute terminal-Y for a click on the first row of the tool.
	// chatRowAt() inverts this: chatRowAt(y) = y - (zones.Tabs + 1).
	// So y = toolSpan[0] - vp.YOffset() + zones.Tabs + 1.
	zones := ZonesFor(40, true)
	clickY := toolSpan[0] - model.chat.vp.YOffset() + zones.Tabs + 1

	click := tea.MouseClickMsg{
		X:      5,
		Y:      clickY,
		Button: tea.MouseLeft,
	}
	after, _ := model.Update(click)
	a := after.(Model)

	if a.focus != chatFocus {
		t.Errorf("focus = %v, want chatFocus", a.focus)
	}
	if a.chatSel != 1 {
		t.Errorf("chatSel = %d, want 1 (tool row)", a.chatSel)
	}
	if !a.messages[1].Expanded {
		t.Errorf("tool row Expanded = false, want true after click")
	}
}

// TestClickOnToolRowAfterLongUserMessage checks the click-to-row
// math survives a wrapped user message above the tool. The user
// types something longer than the chat content width; renderBody
// hard-wraps it, the row span for the user message grows, and the
// tool row that comes next must still be hittable by a click on
// its actual visual row. Without explicit wrapping the viewport
// would reflow under our feet and shift the tool one row.
func TestClickOnToolRowAfterLongUserMessage(t *testing.T) {
	facade := &fakeFacade{}
	model := NewModel(facade, false, "v0")
	model.splash = false
	// Narrow window so the user message DEFINITELY wraps.
	wsz := tea.WindowSizeMsg{Width: 40, Height: 40}
	mAfterSize, _ := model.Update(wsz)
	model = mAfterSize.(Model)

	long := strings.Repeat("blah blah ", 20) // ~200 chars
	now := time.Now()
	model.messages = []Message{
		{Kind: MessageUser, Time: now, Text: long},
		{Kind: MessageToolCall, Time: now, ToolName: "x.read_file",
			ToolCallID: "c1", ToolArgs: map[string]any{"path": "/x"}},
		{Kind: MessageToolResult, Time: now, ToolName: "x.read_file",
			ToolCallID: "c1", ToolResult: map[string]any{"bytes": 12}},
	}
	model.chat.SetMessages(model.messages)

	// The tool row's recorded span must point to actual content
	// rows in the rendered string. Read the start row, slice the
	// rendered viewport content by '\n', and confirm the line at
	// that index is the tool header (contains the tool name).
	toolSpan := model.chat.rowSpans[1]
	rendered := model.chat.vp.View()
	visualLines := strings.Split(rendered, "\n")
	if toolSpan[0] >= len(visualLines) {
		t.Fatalf("tool span %v out of rendered lines (n=%d)",
			toolSpan, len(visualLines))
	}
	headerLine := visualLines[toolSpan[0]-model.chat.vp.YOffset()]
	if !strings.Contains(headerLine, "x.read_file") {
		t.Errorf("tool header expected on visual row %d, got %q",
			toolSpan[0], headerLine)
	}
}

// TestClickOnRootMessageAfterLongUserMessage exercises the same
// invariant for a click on a multi-line root reply: every row in
// the rendered chat must map back to the right message index.
func TestClickOnRootMessageAfterLongUserMessage(t *testing.T) {
	facade := &fakeFacade{}
	model := NewModel(facade, false, "v0")
	model.splash = false
	wsz := tea.WindowSizeMsg{Width: 50, Height: 40}
	mAfterSize, _ := model.Update(wsz)
	model = mAfterSize.(Model)

	now := time.Now()
	model.messages = []Message{
		{Kind: MessageUser, Time: now, Text: strings.Repeat("foo bar ", 20)},
		// Three list items rather than three plain lines: the
		// markdown renderer keeps these as separate body rows
		// regardless of paragraph reflow.
		{Kind: MessageRoot, Time: now, Text: "- first\n- second\n- third"},
	}
	model.chat.SetMessages(model.messages)

	// The root span should cover at least 4 rows (label + 3 body
	// lines) plus whatever lipgloss padding adds.
	rootSpan := model.chat.rowSpans[1]
	if rootSpan[1]-rootSpan[0] < 4 {
		t.Errorf("root span height = %d, want >= 4", rootSpan[1]-rootSpan[0])
	}

	// Clicking the first content row of root should select index 1.
	zones := ZonesFor(40, true)
	clickY := rootSpan[0] - model.chat.vp.YOffset() + zones.Tabs + 1
	click := tea.MouseClickMsg{X: 5, Y: clickY, Button: tea.MouseLeft}
	after, _ := model.Update(click)
	a := after.(Model)
	if a.chatSel != 1 {
		t.Errorf("chatSel = %d, want 1 (root)", a.chatSel)
	}
}

// TestArrowDownSkipsHiddenToolResults verifies that ↓ navigation
// jumps from one visible row to the next, skipping consumed
// MessageToolResult entries that the renderer folded into the
// paired call's card. Before the fix, the user had to press ↓
// twice per tool to traverse the chat because every paired tool
// occupied two slice indices but only one visible row.
func TestArrowDownSkipsHiddenToolResults(t *testing.T) {
	facade := &fakeFacade{}
	model := NewModel(facade, false, "v0")
	model.splash = false
	wsz := tea.WindowSizeMsg{Width: 80, Height: 40}
	mAfterSize, _ := model.Update(wsz)
	model = mAfterSize.(Model)

	now := time.Now()
	model.messages = []Message{
		{Kind: MessageUser, Time: now, Text: "go"},
		{Kind: MessageToolCall, Time: now, ToolName: "x.ls",
			ToolCallID: "c1", ToolArgs: map[string]any{"path": "/"}},
		{Kind: MessageToolResult, Time: now, ToolName: "x.ls",
			ToolCallID: "c1", ToolResult: map[string]any{"n": 1}},
		{Kind: MessageToolCall, Time: now, ToolName: "x.read",
			ToolCallID: "c2", ToolArgs: map[string]any{"path": "/x"}},
		{Kind: MessageToolResult, Time: now, ToolName: "x.read",
			ToolCallID: "c2", ToolResult: map[string]any{"bytes": 99}},
		{Kind: MessageRoot, Time: now, Text: "done"},
	}
	model.chat.SetMessages(model.messages)
	// Focus the chat at the user row (index 0).
	model.focusChat(0)

	// Visible rows are 0 (user), 1 (tool ls), 3 (tool read), 5 (root).
	// Three down-arrows should reach the root reply.
	want := []int{1, 3, 5}
	cur := tea.Model(model)
	for step, expected := range want {
		next, _ := cur.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		cur = next
		got := cur.(Model).chatSel
		if got != expected {
			t.Errorf("after %d down-press(es): chatSel = %d, want %d",
				step+1, got, expected)
		}
	}

	// One more down on the last visible row should be a no-op.
	next, _ := cur.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if next.(Model).chatSel != 5 {
		t.Errorf("clamped down stayed: chatSel = %d, want 5", next.(Model).chatSel)
	}
}
