// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/facade"
)

// switchInterlocutor swaps the chat to talk to a different participant
// of the system. Today the participants are:
//
//   - "root" (rootInterlocutor): the always-on coordinator agent.
//   - any worker_id: a worker currently registered in the workers
//     manager. The TUI subscribes to its event stream and renders
//     events as chat rows.
//
// State is preserved per interlocutor: the messages slice that was
// active before the switch is stashed in chatHistories under the old
// id, then the slice for the new id is restored (or initialised
// empty). The viewport is repainted at the end so the user lands on
// the right scroll position.
//
// When switching INTO a worker, the function also subscribes to the
// worker's live stream and replays its event history into the chat
// so the user sees what happened before they arrived. When switching
// AWAY from a worker, the previous subscription is cancelled.
func (m Model) switchInterlocutor(id string) (tea.Model, tea.Cmd) {
	if id == "" {
		id = rootInterlocutor
	}
	if id == m.activeInterlocutor {
		return m, nil
	}

	// Stash the outgoing interlocutor's messages so they survive the
	// swap. The active slice is always m.messages.
	if m.chatHistories == nil {
		m.chatHistories = make(map[string][]Message)
	}
	m.chatHistories[m.activeInterlocutor] = m.messages

	// Cancel any live subscription from the previous worker. The new
	// subscription (if any) is set up after the swap.
	if m.workerStreamCancel != nil {
		m.workerStreamCancel()
		m.workerStreamCancel = nil
	}

	// Switch.
	m.activeInterlocutor = id
	m.messages = m.chatHistories[id]
	// Defensive: a nil slice is fine for append, but we want to leave
	// the map entry visible in tests / debug prints.
	if m.messages == nil {
		m.messages = []Message{}
	}
	// Restore the streaming bubble id for the destination so a reply
	// still in flight keeps coalescing into its own bubble after the
	// swap. Root uses rootStreamID; workers use their per-id entry.
	if id == rootInterlocutor {
		m.chat.streamingID = m.rootStreamID
	} else {
		m.chat.streamingID = m.workerStreamIDs[id]
	}
	m.chat.SetMessages(m.messages)

	// Refresh the context-guard gauge for the new interlocutor: it
	// reappears (seeded from session state, no notice) when switching
	// back to the root and is cleared while a worker chat is active.
	m.guardSeeded = false
	m.refreshGuard(false)

	// If we just switched into a worker, hydrate the chat with its
	// event history and start listening for live updates.
	var cmd tea.Cmd
	if id != rootInterlocutor && m.facade != nil {
		var next tea.Model
		next, cmd = m.attachWorkerStream(id)
		return next, cmd
	}
	return m, cmd
}

// attachWorkerStream wires the chat to a worker's event stream. It
// pulls the history snapshot (replayed as chat messages so the user
// sees context) and stores the unsubscribe func plus a tea.Cmd that
// pumps live events back into the model loop.
//
// Translation from facade.WorkerStreamEvent to Message kinds:
//
//   - WorkerStreamText           → MessageRoot (the worker's own text)
//   - WorkerStreamToolCall       → MessageToolCall
//   - WorkerStreamToolResult     → MessageToolResult
//   - WorkerStreamStatus         → MessageSystem ("status: running")
//
// We reuse the existing message kinds intentionally: the chat
// component already knows how to render them, and from the user's
// point of view "the agent's reply" and "the worker's reply" should
// look the same.
func (m Model) attachWorkerStream(id string) (tea.Model, tea.Cmd) {
	history, stream, cancel, err := m.facade.SubscribeWorker(id)
	if err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("subscribe worker %s: %v", id, err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	m.chat.streamingID = ""
	streamID := ""
	seq := 0
	newID := func() string { seq++; return id + "-h" + strconv.Itoa(seq) }
	for _, evt := range history {
		m.messages, streamID = appendOrCoalesceWorkerText(m.messages, evt, streamID, newID)
	}
	m.chat.streamingID = "" // History is done, not live

	m.chat.SetMessages(m.messages)
	m.workerStreamCancel = cancel
	return m, listenWorkerStream(id, stream)
}

// appendOrCoalesceWorkerText appends evt to msgs. Consecutive text
// events grow the same MessageRoot bubble, identified by streamID
// (NOT by array position) so anything inserted after it can never
// split it. A non-text event (tool call/result, status) ends the
// current text bubble by clearing the active streamID, so the next
// text starts a fresh bubble. Returns the updated slice and the
// active streamID to carry into the next call.
//
// newStreamID mints a fresh id when a text bubble begins; it is
// passed in so the caller controls id uniqueness across workers.
func appendOrCoalesceWorkerText(msgs []Message, evt facade.WorkerStreamEvent, streamID string, newStreamID func() string) ([]Message, string) {
	if evt.Kind == facade.WorkerStreamText {
		if evt.Text == "" {
			return msgs, streamID
		}
		if streamID == "" {
			streamID = newStreamID()
		}
		msgs = coalesceStream(msgs, streamID, evt.Text, false)
		return msgs, streamID
	}
	// Non-text event: ends the current text bubble and gets its own row.
	return append(msgs, workerEventToMessage(evt)), ""
}

// workerEventToMessage maps a WorkerStreamEvent into a Message for the
// chat view. The label conventions match the root chat so visually
// nothing surprises the user when they switch interlocutors.
func workerEventToMessage(evt facade.WorkerStreamEvent) Message {
	ts := evt.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	switch evt.Kind {
	case facade.WorkerStreamText:
		return Message{Kind: MessageRoot, Time: ts, Text: evt.Text}
	case facade.WorkerStreamToolCall:
		if len(evt.ToolCalls) > 0 {
			call := evt.ToolCalls[0]
			return Message{
				Kind:       MessageToolCall,
				Time:       ts,
				Text:       formatToolCallSummary(call),
				ToolName:   call.Name,
				ToolCallID: call.CallID,
				ToolArgs:   call.Args,
			}
		}
	case facade.WorkerStreamToolResult:
		if len(evt.ToolResults) > 0 {
			r := evt.ToolResults[0]
			return Message{
				Kind:       MessageToolResult,
				Time:       ts,
				Text:       formatToolResultSummary(r),
				ToolName:   r.Name,
				ToolCallID: r.CallID,
				ToolResult: r.Result,
			}
		}
	case facade.WorkerStreamStatus:
		return Message{
			Kind: MessageSystem,
			Time: ts,
			Text: "status: " + evt.StatusChange,
		}
	}
	return Message{Kind: MessageSystem, Time: ts, Text: "(empty event)"}
}

// workerStreamMsg carries one live worker event back into the model
// loop. The handler appends it to the active chat (only when the
// active interlocutor still matches the event's worker_id — the user
// may have switched away in the meantime).
type workerStreamMsg struct {
	workerID string
	evt      facade.WorkerStreamEvent
	stream   <-chan facade.WorkerStreamEvent
	closed   bool
}

// listenWorkerStream returns a tea.Cmd that reads ONE event from the
// worker's live stream and reschedules itself for the next one. Same
// pattern as readNext for the root agent's stream.
func listenWorkerStream(workerID string, stream <-chan facade.WorkerStreamEvent) tea.Cmd {
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
		evt, ok := <-stream
		if !ok {
			return workerStreamMsg{workerID: workerID, closed: true}
		}
		return workerStreamMsg{workerID: workerID, evt: evt, stream: stream}
	}
}

// handleWorkerStream is the Update branch for workerStreamMsg.
func (m Model) handleWorkerStream(msg workerStreamMsg) (tea.Model, tea.Cmd) {
	// If the user is currently looking at a different chat, we must append
	// the chunk to the correct stashed history instead of throwing it away.
	isBackground := msg.workerID != m.activeInterlocutor
	targetMsgs := m.messages
	if isBackground {
		targetMsgs = m.chatHistories[msg.workerID]
	}

	if msg.closed {
		delete(m.workerStreamIDs, msg.workerID)
		if !isBackground {
			m.workerStreamCancel = nil
			m.chat.streamingID = ""
			m.chat.SetMessages(targetMsgs)
		}
		// If it's closed, we don't listen for more.
		return m, nil
	}

	// Coalesce by the worker's StreamID, minting a fresh one when a
	// text bubble begins. Identity, not position, keeps the bubble
	// intact regardless of what lands after it.
	if m.workerStreamIDs == nil {
		m.workerStreamIDs = map[string]string{}
	}
	newID := func() string {
		m.streamSeq++
		return msg.workerID + "-" + strconv.Itoa(m.streamSeq)
	}
	var sid string
	targetMsgs, sid = appendOrCoalesceWorkerText(targetMsgs, msg.evt, m.workerStreamIDs[msg.workerID], newID)
	if sid == "" {
		delete(m.workerStreamIDs, msg.workerID)
	} else {
		m.workerStreamIDs[msg.workerID] = sid
	}

	if isBackground {
		m.chatHistories[msg.workerID] = targetMsgs
	} else {
		m.messages = targetMsgs
		m.chat.streamingID = sid
		m.chat.SetMessages(m.messages)
	}

	// Arm a trailing-edge flush (once) when a foreground text chunk
	// may have rendered throttled, so a paused worker reply still
	// shows its full text without waiting for the next event.
	next := listenWorkerStream(msg.workerID, msg.stream)
	if !isBackground && msg.evt.Kind == facade.WorkerStreamText && msg.evt.Text != "" && !m.flushPending {
		m.flushPending = true
		return m, tea.Batch(next, scheduleStreamFlush())
	}
	return m, next
}

// activeInterlocutorLabel returns the friendly name of the active
// interlocutor for display in the status bar. The root agent gets
// the literal "root"; a worker gets its friendly name (Spec.Name)
// when we can find it in the cached workers list, falling back to
// the bare ID otherwise.
func (m Model) activeInterlocutorLabel() string {
	if m.activeInterlocutor == rootInterlocutor {
		return "root"
	}
	for _, w := range m.workers {
		if w.ID == m.activeInterlocutor {
			if w.Name != "" {
				return w.Name
			}
			return w.ID
		}
	}
	return m.activeInterlocutor
}

// runKillWorker is the handler bound to the Workers-tab `k` action.
// It calls Facade.KillWorker with a user-attributed reason so the
// next collect_agent surfaces "killed by user from TUI" to the root.
// A short system message is appended to the active chat so the user
// has visual feedback that something happened.
func (m Model) runKillWorker(id string) (tea.Model, tea.Cmd) {
	if m.facade == nil {
		return m, nil
	}
	const reason = "killed by user from TUI"
	if err := m.facade.KillWorker(id, reason); err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("kill %s: %v", id, err),
		})
	} else {
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: fmt.Sprintf("killed worker %s (%s)", id, reason),
		})
	}
	m.chat.SetMessages(m.messages)

	// Also free the memory if we explicitly kill it (it will be collected soon anyway,
	// but dropping the TUI cache now is safe).
	if m.chatHistories != nil {
		delete(m.chatHistories, id)
	}

	// Refresh the workers list so the row repaints with the new status.
	if m.facade != nil {
		m.workers = m.facade.ListWorkers()
		if m.workersSel >= len(m.workers) && m.workersSel > 0 {
			m.workersSel = len(m.workers) - 1
		}
	}
	return m, nil
}

// runCollectWorker is the handler bound to the Workers-tab `c`
// action. It waits (with a short timeout) for the worker to reach a
// terminal state, captures its output and removes it from the live
// list. Useful to clean up after a kill or after a worker finished
// on its own.
//
// The timeout is intentionally short (200ms): if the worker is still
// running we don't want the TUI to block. The user can press `c`
// again later.
func (m Model) runCollectWorker(id string) (tea.Model, tea.Cmd) {
	if m.facade == nil {
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	output, err := m.facade.CollectWorker(ctx, id)
	if err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("collect %s: %v", id, err),
		})
	} else {
		text := fmt.Sprintf("collected worker %s", id)
		if output != "" {
			text += "\noutput: " + output
		}
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: text,
		})
	}
	m.chat.SetMessages(m.messages)

	// Free the memory of the collected worker's chat history.
	if m.chatHistories != nil {
		delete(m.chatHistories, id)
	}

	// The collected worker is gone from the registry; refresh.
	m.workers = m.facade.ListWorkers()
	if m.workersSel >= len(m.workers) && m.workersSel > 0 {
		m.workersSel = len(m.workers) - 1
	}
	return m, nil
}

// sendToActive routes the user-typed message to the right destination
// depending on the active interlocutor. The composer does not know
// whether it is talking to the root or to a worker; that asymmetry
// is hidden here.
func (m Model) sendToActive(ctx context.Context, text string) error {
	if m.facade == nil {
		return fmt.Errorf("no facade available")
	}
	if m.activeInterlocutor == rootInterlocutor {
		// Handled by submitComposer's existing streaming path; this
		// branch is only used by tests and future call sites that
		// want a single entry point.
		return nil
	}
	return m.facade.SendToWorker(ctx, m.activeInterlocutor, text)
}

// runResumeSession is bound to the Sessions-tab Enter action. It
// asks the facade to make the selected session active, pulls the
// session's persisted event log, and repaints the chat with those
// historical messages so the resumed conversation is visible right
// away (the user can scroll through, copy text, etc., before
// sending a new turn). We also jump back to the Chat tab so the
// user lands where they can actually type.
//
// Failure to load history is non-fatal: SwitchSession already
// succeeded, so the user can still send messages — they just
// won't see the past turns. We surface the error as a system
// notice instead of refusing the resume.
func (m Model) runResumeSession(id string) (tea.Model, tea.Cmd) {
	if m.facade == nil {
		return m, nil
	}
	if err := m.facade.SwitchSession(context.Background(), id); err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("switch session %s: %v", id, err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	// Hydrate the chat with the session's persisted history. The
	// returned events are already chronologically ordered and
	// stripped of streaming partials by the sessions service.
	ctx := context.Background()
	events, hydrateErr := m.facade.SessionEvents(ctx, id)
	hydrated := make([]Message, 0, len(events)+1)
	hydrated = append(hydrated, Message{
		Kind: MessageSystem,
		Time: time.Now(),
		Text: "resumed session " + id,
	})
	for _, ev := range events {
		hydrated = append(hydrated, facadeEventToMessages(ev)...)
	}
	if hydrateErr != nil {
		hydrated = append(hydrated, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: "could not load history: " + hydrateErr.Error(),
		})
	}
	m.messages = hydrated

	// The chat keeps a per-interlocutor history map; resuming a
	// session is conceptually "drop the old root conversation,
	// start with this one", so we overwrite the root entry too.
	if m.chatHistories == nil {
		m.chatHistories = make(map[string][]Message)
	}
	m.chatHistories[rootInterlocutor] = m.messages
	m.activeInterlocutor = rootInterlocutor

	m.chat.SetMessages(m.messages)
	// Refresh the sessions list so the new "active" marker repaints
	// when the user reopens the /session overlay.
	if list, err := m.facade.ListSessions(ctx); err == nil {
		m.sessions = list
	}
	return m, nil
}

// facadeEventToMessages turns one persisted facade.Event into the
// chat rows it represents. A single ADK event can carry assistant
// text AND tool calls AND tool results at the same time; we split
// them into separate Messages so the renderer can pair tool
// call+result cards just like it does for live streams.
//
// Role mapping:
//   - "user"           → MessageUser
//   - "model" / other  → MessageRoot (the only "assistant" we render)
//
// Tool calls and results are always rendered through
// MessageToolCall / MessageToolResult regardless of role; the chat
// component already knows how to pair them by CallID.
func facadeEventToMessages(ev facade.Event) []Message {
	ts := time.Now()
	if ev.Raw != nil {
		// session.Event has a Timestamp field; preserve the real
		// time of the turn so the per-message clock stamps the
		// resumed view faithfully.
		if !ev.Raw.Timestamp.IsZero() {
			ts = ev.Raw.Timestamp
		}
	}
	out := make([]Message, 0, 1+len(ev.ToolCalls)+len(ev.ToolResults))
	if ev.Text != "" {
		kind := MessageRoot
		if ev.Role == "user" {
			kind = MessageUser
		}
		out = append(out, Message{Kind: kind, Time: ts, Text: ev.Text})
	}
	for _, c := range ev.ToolCalls {
		out = append(out, Message{
			Kind:       MessageToolCall,
			Time:       ts,
			Text:       formatToolCallSummary(c),
			ToolName:   c.Name,
			ToolCallID: c.CallID,
			ToolArgs:   c.Args,
		})
	}
	for _, r := range ev.ToolResults {
		out = append(out, Message{
			Kind:       MessageToolResult,
			Time:       ts,
			Text:       formatToolResultSummary(r),
			ToolName:   r.Name,
			ToolCallID: r.CallID,
			ToolResult: r.Result,
		})
	}
	return out
}

// runDeleteSession is bound to the Sessions-tab `d` action. It
// removes the selected session via the facade; when the deleted
// session was active, the facade promotes a new one and we mirror
// the swap locally by resetting the chat history.
func (m Model) runDeleteSession(id string) (tea.Model, tea.Cmd) {
	if m.facade == nil {
		return m, nil
	}
	wasActive := id == m.facade.SessionID()
	newActive, err := m.facade.DeleteSession(context.Background(), id)
	if err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("delete session %s: %v", id, err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}
	if wasActive {
		m.messages = []Message{{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: fmt.Sprintf("deleted active session, switched to %s", newActive),
		}}
	} else {
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: "deleted session " + id,
		})
	}
	m.chat.SetMessages(m.messages)
	if list, err := m.facade.ListSessions(context.Background()); err == nil {
		m.sessions = list
		if m.sessionsSel >= len(list) && m.sessionsSel > 0 {
			m.sessionsSel = len(list) - 1
		}
	}
	return m, nil
}

// runNewSession is bound to the Sessions-tab `n` action. It asks the
// facade to allocate a fresh session, swaps to it locally, and jumps
// the user to the Chat tab so they can type right away.
func (m Model) runNewSession() (tea.Model, tea.Cmd) {
	if m.facade == nil {
		return m, nil
	}
	id, err := m.facade.NewSession(context.Background())
	if err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("new session: %v", err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}
	m.messages = []Message{{
		Kind: MessageSystem,
		Time: time.Now(),
		Text: "started new session " + id,
	}}
	m.chat.SetMessages(m.messages)
	if list, err := m.facade.ListSessions(context.Background()); err == nil {
		m.sessions = list
	}
	return m, nil
}
