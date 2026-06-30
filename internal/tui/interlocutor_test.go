// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/facade"
)

// spyFacade extends fakeFacade with knobs to script the worker stream
// subscription so the tests can drive the interlocutor switch without
// a real workers manager.
type spyFacade struct {
	fakeFacade

	workers       []facade.WorkerInfo
	history       []facade.WorkerStreamEvent
	sendCalled    []string
	sendToID      string
	subscribeID   string
	subscribeCanc int
}

func (s *spyFacade) ListWorkers() []facade.WorkerInfo { return s.workers }
func (s *spyFacade) SubscribeWorker(id string) ([]facade.WorkerStreamEvent, <-chan facade.WorkerStreamEvent, func(), error) {
	s.subscribeID = id
	ch := make(chan facade.WorkerStreamEvent)
	// The stream stays open until cancel is called.
	cancel := func() {
		s.subscribeCanc++
		close(ch)
	}
	return s.history, ch, cancel, nil
}
func (s *spyFacade) SendToWorker(_ context.Context, id, msg string) error {
	s.sendToID = id
	s.sendCalled = append(s.sendCalled, msg)
	return nil
}

// TestSwitchInterlocutor_HydratesHistoryAndSetsActive checks the core
// contract of the interlocutor switch: history events become chat
// messages, the active interlocutor flips, and the previous chat is
// saved so we can return to it later.
func TestSwitchInterlocutor_HydratesHistoryAndSetsActive(t *testing.T) {
	facade := &spyFacade{
		workers: []facade.WorkerInfo{{ID: "w_abc", Name: "researcher"}},
		history: []facade.WorkerStreamEvent{
			{Kind: facade.WorkerStreamText, Text: "hola"},
			{Kind: facade.WorkerStreamStatus, StatusChange: "running"},
		},
	}
	m := NewModel(facade, false, "v0")
	m.splash = false
	// Seed the root chat with one message so we can verify it gets
	// preserved across the switch.
	m.messages = []Message{{Kind: MessageRoot, Text: "root reply"}}

	next, _ := m.switchInterlocutor("w_abc")
	nm := next.(Model)

	if nm.activeInterlocutor != "w_abc" {
		t.Errorf("activeInterlocutor = %q, want w_abc", nm.activeInterlocutor)
	}
	if facade.subscribeID != "w_abc" {
		t.Errorf("SubscribeWorker called with %q, want w_abc", facade.subscribeID)
	}
	// New chat hydrated with history (2 events).
	if len(nm.messages) != 2 {
		t.Fatalf("worker chat: got %d messages, want 2 (%+v)", len(nm.messages), nm.messages)
	}
	if nm.messages[0].Text != "hola" {
		t.Errorf("first hydrated message text = %q, want hola", nm.messages[0].Text)
	}
	// Root chat preserved in the history map.
	saved := nm.chatHistories[rootInterlocutor]
	if len(saved) != 1 || saved[0].Text != "root reply" {
		t.Errorf("root chat not preserved: %+v", saved)
	}
}

// TestSwitchBackRestoresOriginalChat ensures going back to root after
// visiting a worker recovers the original messages slice intact.
func TestSwitchBackRestoresOriginalChat(t *testing.T) {
	facade := &spyFacade{
		workers: []facade.WorkerInfo{{ID: "w_x", Name: "x"}},
	}
	m := NewModel(facade, false, "v0")
	m.splash = false
	m.messages = []Message{{Kind: MessageRoot, Text: "kept"}}

	n1, _ := m.switchInterlocutor("w_x")
	n2, _ := n1.(Model).switchInterlocutor(rootInterlocutor)
	final := n2.(Model)

	if final.activeInterlocutor != rootInterlocutor {
		t.Errorf("activeInterlocutor = %q, want root", final.activeInterlocutor)
	}
	if len(final.messages) != 1 || final.messages[0].Text != "kept" {
		t.Errorf("root chat not restored: %+v", final.messages)
	}
}

// killCollectFacade extends spyFacade to record kill/collect calls
// from the Workers-tab shortcuts.
type killCollectFacade struct {
	spyFacade

	killedID     string
	killedReason string
	collectedID  string
	collectOut   string
}

func (k *killCollectFacade) KillWorker(id, reason string) error {
	k.killedID = id
	k.killedReason = reason
	return nil
}
func (k *killCollectFacade) CollectWorker(_ context.Context, id string) (string, error) {
	k.collectedID = id
	return k.collectOut, nil
}

// TestWorkersTab_KillShortcutAsksConfirmThenKills exercises the
// y/n confirmation flow on the Workers tab. Pressing 'k' arms the
// prompt; 'y' confirms and calls Facade.KillWorker with the human
// reason; 'n' would clear the prompt without acting.
func TestWorkersTab_KillShortcutAsksConfirmThenKills(t *testing.T) {
	facade := &killCollectFacade{
		spyFacade: spyFacade{workers: []facade.WorkerInfo{{ID: "w_a", Name: "a"}}},
	}
	m := NewModel(facade, false, "v0")
	m.splash = false
	m.workersOpen = true
	m.workers = facade.workers

	// First 'k' arms the confirmation.
	afterK, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	armed := afterK.(Model)
	if armed.workersConfirmKill != "w_a" {
		t.Fatalf("confirm flag = %q, want w_a", armed.workersConfirmKill)
	}
	if facade.killedID != "" {
		t.Fatalf("kill should NOT fire on the first press")
	}

	// 'y' confirms.
	afterY, _ := armed.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	done := afterY.(Model)
	if done.workersConfirmKill != "" {
		t.Errorf("confirm flag should be cleared, got %q", done.workersConfirmKill)
	}
	if facade.killedID != "w_a" {
		t.Errorf("KillWorker not called: %+v", facade)
	}
	if facade.killedReason == "" {
		t.Errorf("kill reason should be non-empty (user attribution)")
	}
}

// TestWorkersTab_CollectShortcutAsksConfirmThenCollects covers the
// y/n flow for collect. Like kill, collect is destructive (it
// unregisters the worker AND wipes its sandbox), so a single press
// must arm the prompt, not fire.
func TestWorkersTab_CollectShortcutAsksConfirmThenCollects(t *testing.T) {
	facade := &killCollectFacade{
		spyFacade:  spyFacade{workers: []facade.WorkerInfo{{ID: "w_b", Name: "b"}}},
		collectOut: "all done",
	}
	m := NewModel(facade, false, "v0")
	m.splash = false
	m.workersOpen = true
	m.workers = facade.workers

	// First 'c' arms.
	afterC, _ := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	armed := afterC.(Model)
	if armed.workersConfirmCollect != "w_b" {
		t.Fatalf("confirm flag = %q, want w_b", armed.workersConfirmCollect)
	}
	if facade.collectedID != "" {
		t.Fatalf("collect should NOT fire on the first press")
	}

	// 'y' confirms.
	afterY, _ := armed.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	done := afterY.(Model)
	if done.workersConfirmCollect != "" {
		t.Errorf("confirm flag should clear, got %q", done.workersConfirmCollect)
	}
	if facade.collectedID != "w_b" {
		t.Errorf("CollectWorker not called: %+v", facade)
	}
}

// TestAppendOrCoalesceWorkerText_ConsecutiveTextMerges verifies the core
// coalescing contract: three consecutive text chunks collapse into a
// single MessageRoot row whose Text is the concatenation.
func TestAppendOrCoalesceWorkerText_ConsecutiveTextMerges(t *testing.T) {
	evts := []facade.WorkerStreamEvent{
		{Kind: facade.WorkerStreamText, Text: "Hel"},
		{Kind: facade.WorkerStreamText, Text: "lo"},
		{Kind: facade.WorkerStreamText, Text: " world"},
	}
	var msgs []Message
	sid := ""
	seq := 0
	newID := func() string { seq++; return "w-" + strconv.Itoa(seq) }
	for _, e := range evts {
		msgs, sid = appendOrCoalesceWorkerText(msgs, e, sid, newID)
	}
	_ = sid
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1; messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Kind != MessageRoot {
		t.Errorf("msg.Kind = %v, want MessageRoot", msgs[0].Kind)
	}
	if msgs[0].Text != "Hello world" {
		t.Errorf("msg.Text = %q, want %q", msgs[0].Text, "Hello world")
	}
}

// TestAppendOrCoalesceWorkerText_ToolCallBreaksStreak verifies that a
// non-text event in the middle breaks the coalescing streak: [text "A",
// toolcall, text "B"] must produce three rows (MessageRoot, MessageToolCall,
// MessageRoot), each with the right content.
func TestAppendOrCoalesceWorkerText_ToolCallBreaksStreak(t *testing.T) {
	evts := []facade.WorkerStreamEvent{
		{Kind: facade.WorkerStreamText, Text: "A"},
		{
			Kind:      facade.WorkerStreamToolCall,
			ToolCalls: []facade.ToolCallInfo{{Name: "my_tool", CallID: "c1"}},
		},
		{Kind: facade.WorkerStreamText, Text: "B"},
	}
	var msgs []Message
	sid := ""
	seq := 0
	newID := func() string { seq++; return "w-" + strconv.Itoa(seq) }
	for _, e := range evts {
		msgs, sid = appendOrCoalesceWorkerText(msgs, e, sid, newID)
	}
	_ = sid
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3; messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Kind != MessageRoot || msgs[0].Text != "A" {
		t.Errorf("row 0: got Kind=%v Text=%q, want MessageRoot/A", msgs[0].Kind, msgs[0].Text)
	}
	if msgs[1].Kind != MessageToolCall {
		t.Errorf("row 1: got Kind=%v, want MessageToolCall", msgs[1].Kind)
	}
	if msgs[2].Kind != MessageRoot || msgs[2].Text != "B" {
		t.Errorf("row 2: got Kind=%v Text=%q, want MessageRoot/B", msgs[2].Kind, msgs[2].Text)
	}
}

// TestAppendOrCoalesceWorkerText_EmptyTextIsNoop verifies that a text
// event with an empty payload does not append any row (the upstream
// driver never sends empty deltas, but the helper must be safe).
func TestAppendOrCoalesceWorkerText_EmptyTextIsNoop(t *testing.T) {
	evts := []facade.WorkerStreamEvent{
		{Kind: facade.WorkerStreamText, Text: "hello"},
		{Kind: facade.WorkerStreamText, Text: ""},
	}
	var msgs []Message
	sid := ""
	seq := 0
	newID := func() string { seq++; return "w-" + strconv.Itoa(seq) }
	for _, e := range evts {
		msgs, sid = appendOrCoalesceWorkerText(msgs, e, sid, newID)
	}
	_ = sid
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1; messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Text != "hello" {
		t.Errorf("msg.Text = %q, want %q", msgs[0].Text, "hello")
	}
}

// TestAppendOrCoalesceWorkerText_StatusDoesNotCoalesceIntoText verifies
// that a status event after a text event produces its own row and that the
// next text starts a fresh bubble (three rows total).
func TestAppendOrCoalesceWorkerText_StatusDoesNotCoalesceIntoText(t *testing.T) {
	evts := []facade.WorkerStreamEvent{
		{Kind: facade.WorkerStreamText, Text: "thinking"},
		{Kind: facade.WorkerStreamStatus, StatusChange: "running"},
		{Kind: facade.WorkerStreamText, Text: "done"},
	}
	var msgs []Message
	sid := ""
	seq := 0
	newID := func() string { seq++; return "w-" + strconv.Itoa(seq) }
	for _, e := range evts {
		msgs, sid = appendOrCoalesceWorkerText(msgs, e, sid, newID)
	}
	_ = sid
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3; messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Kind != MessageRoot || msgs[0].Text != "thinking" {
		t.Errorf("row 0: got Kind=%v Text=%q", msgs[0].Kind, msgs[0].Text)
	}
	if msgs[1].Kind != MessageSystem {
		t.Errorf("row 1: got Kind=%v, want MessageSystem", msgs[1].Kind)
	}
	if msgs[2].Kind != MessageRoot || msgs[2].Text != "done" {
		t.Errorf("row 2: got Kind=%v Text=%q", msgs[2].Kind, msgs[2].Text)
	}
}

// TestHandleWorkerStream_LiveChunksCoalesce exercises the live path
// (handleWorkerStream) end-to-end using constructed workerStreamMsg
// values, without spawning any goroutines.
func TestHandleWorkerStream_LiveChunksCoalesce(t *testing.T) {
	spy := &spyFacade{
		workers: []facade.WorkerInfo{{ID: "w_live", Name: "live"}},
	}
	m := NewModel(spy, false, "v0")
	m.splash = false
	// Switch to the worker so m.activeInterlocutor == "w_live".
	next, _ := m.switchInterlocutor("w_live")
	nm := next.(Model)

	// Feed three text chunks via handleWorkerStream (no real channel
	// needed, stream is nil so listenWorkerStream returns nil cmd).
	chunks := []string{"foo", "bar", " baz"}
	for _, ch := range chunks {
		var next2 tea.Model
		next2, _ = nm.handleWorkerStream(workerStreamMsg{
			workerID: "w_live",
			evt:      facade.WorkerStreamEvent{Kind: facade.WorkerStreamText, Text: ch},
			stream:   nil,
		})
		nm = next2.(Model)
	}

	if len(nm.messages) != 1 {
		t.Fatalf("got %d messages after 3 chunks, want 1; %+v", len(nm.messages), nm.messages)
	}
	if nm.messages[0].Text != "foobar baz" {
		t.Errorf("coalesced text = %q, want %q", nm.messages[0].Text, "foobar baz")
	}
}

// TestAttachWorkerStream_HistoryCoalesced checks that the history-replay
// path in attachWorkerStream also coalesces consecutive text events.
func TestAttachWorkerStream_HistoryCoalesced(t *testing.T) {
	spy := &spyFacade{
		workers: []facade.WorkerInfo{{ID: "w_hist", Name: "hist"}},
		history: []facade.WorkerStreamEvent{
			{Kind: facade.WorkerStreamText, Text: "part1"},
			{Kind: facade.WorkerStreamText, Text: " part2"},
			{Kind: facade.WorkerStreamStatus, StatusChange: "idle"},
			{Kind: facade.WorkerStreamText, Text: "final"},
		},
	}
	m := NewModel(spy, false, "v0")
	m.splash = false
	next, _ := m.switchInterlocutor("w_hist")
	nm := next.(Model)

	// Expected: "part1 part2" (coalesced), "status: idle", "final"
	if len(nm.messages) != 3 {
		t.Fatalf("got %d messages, want 3; %+v", len(nm.messages), nm.messages)
	}
	if nm.messages[0].Text != "part1 part2" {
		t.Errorf("row 0 text = %q, want %q", nm.messages[0].Text, "part1 part2")
	}
	if nm.messages[1].Kind != MessageSystem {
		t.Errorf("row 1: got Kind=%v, want MessageSystem", nm.messages[1].Kind)
	}
	if nm.messages[2].Text != "final" {
		t.Errorf("row 2 text = %q, want %q", nm.messages[2].Text, "final")
	}
}

// TestComposerSubmitRoutesToWorkerWhenActive checks that when a worker
// is the active interlocutor, the composer's Enter sends through
// SendToWorker instead of the root's streaming runner.
func TestComposerSubmitRoutesToWorkerWhenActive(t *testing.T) {
	facade := &spyFacade{
		workers: []facade.WorkerInfo{{ID: "w_y", Name: "y"}},
	}
	m := NewModel(facade, false, "v0")
	m.splash = false
	next, _ := m.switchInterlocutor("w_y")
	nm := next.(Model)
	nm.composer.ta.SetValue("oye worker")

	final, _ := nm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = final

	if facade.sendToID != "w_y" {
		t.Errorf("SendToWorker called with %q, want w_y", facade.sendToID)
	}
	if len(facade.sendCalled) != 1 || facade.sendCalled[0] != "oye worker" {
		t.Errorf("SendToWorker messages: %v", facade.sendCalled)
	}
}
