// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/tui/components/editor"
	"github.com/achetronic/baifo/internal/tui/overlays"
	"github.com/atotto/clipboard"
)

// Model is the BubbleTea program's top-level tea.Model. It owns every
// stateful piece of the TUI: the active tab, the chat history, the
// composer, the size, the theme, and a reference to the Facade. The
// model is intentionally small — components either receive plain data
// at render time or wrap a child tea.Model exposed via field access.
// rootInterlocutor is the sentinel value of Model.activeInterlocutor
// that means "the chat is talking to the root agent". Any other value
// is a worker_id; see PR B notes in the commit log.
const rootInterlocutor = "root"

// focusKind enumerates the two regions of the Chat tab the user
// can have focus on: the writing box at the bottom and the chat
// transcript above. Focus switches happen through clicks (and
// implicitly through Tab cycling, which always resets to
// composerFocus on the Chat tab so the user lands ready to type).
//
// There is no "navigation mode" — focus is just where the cursor
// goes, not a stateful UI mode. Arrows do one thing when the
// writing box has focus (move the text cursor) and another when
// the chat has focus (move between messages). The renderer
// signals focus through the panel borders.
type focusKind int

const (
	// composerFocus means the writing box receives keystrokes; the
	// chat above is read-only and shows no selection marker.
	composerFocus focusKind = iota
	// chatFocus means the chat transcript receives navigation keys;
	// the writing box still shows but its border is faint to make
	// the switch obvious. Typing while in chatFocus does nothing
	// (or, when we want, snaps focus back to the composer — that's
	// a design knob we can flip later).
	chatFocus
)

type Model struct {
	facade facade.Facade
	theme  Theme

	width    int
	height   int
	mode     LayoutMode
	tooSmall bool

	messages []Message
	chat     chatView
	composer composer

	// activeInterlocutor identifies whose chat is currently visible in
	// the chat tab. "root" (rootInterlocutor) is the default and means
	// "talk to the root agent via SendMessage". Any other value is a
	// worker_id; messages go to Facade.SendToWorker and the live
	// stream comes from Facade.SubscribeWorker.
	activeInterlocutor string

	// chatHistories stores the messages of EVERY interlocutor that is
	// not currently active. When the user switches, we swap the
	// active m.messages slice with the entry here so each chat keeps
	// its own conversation in isolation.
	chatHistories map[string][]Message

	// workerStreamCancel is the unsubscribe function returned by
	// Facade.SubscribeWorker. Non-nil while a worker chat is active;
	// invoked when the user switches away or the worker is collected.
	workerStreamCancel func()

	helpOpen bool

	// sessionsOpen is true while the /session overlay is up. The
	// overlay shares its rendering routine with the legacy
	// Sessions tab — same look, just floating over the chat
	// instead of being a separate workspace.
	sessionsOpen bool

	// workersOpen mirrors sessionsOpen for the /worker overlay.
	workersOpen bool

	// factsOpen mirrors sessionsOpen for the /fact overlay: a
	// navigable list of the stored long-term memory entries.
	factsOpen bool

	// catalogOpen is true while the generic catalogue overlay is up.
	// Unlike the bespoke sessions/workers/facts overlays, one catalogue
	// overlay backs every config-entity list verb (/agent, /provider,
	// /mcp, /secret, /skill): they all browse a flat, read-only list
	// with an optional Enter→edit primary action. The fields below carry
	// whichever entity is currently shown.
	catalogOpen bool
	catalogView catalogView
	catalogSel  int

	// secretPrompt is the masked-input modal baifo shows for
	// /secret set commands. nil when no prompt is active; when
	// non-nil, Update routes through it instead of the main keymap.
	secretPrompt *overlays.SecretPrompt

	// editor is the embedded YAML editor overlay. nil when not open.
	// When non-nil, all key events go to it and the rest of the
	// chrome is dimmed in View.
	editor *editor.Model

	// editorOnSavePath is the absolute path to write to when the
	// editor emits SaveMsg. Empty means "caller doesn't need a file
	// written" (currently unused; reserved for future CRUDs that
	// don't map 1:1 to a file).
	editorOnSavePath string

	// editorOnSaveKind selects which persistence strategy the editor
	// should run when it emits SaveMsg. Set when the editor opens.
	editorOnSaveKind editorKind

	// editorFactTargetID is the fact ID to update when
	// editorOnSaveKind is editorKindFactUpdate. Zero otherwise.
	editorFactTargetID uint64

	// editorSessionTargetID is the session ID to rename when
	// editorOnSaveKind is editorKindSessionRename. Empty otherwise.
	editorSessionTargetID string

	workersSel int

	// workersConfirmKill is the worker_id pending a kill confirmation.
	// Empty when no prompt is up. While set, the Workers tab steals
	// y/n/esc to confirm or cancel the destructive action.
	workersConfirmKill string

	// workersConfirmCollect mirrors workersConfirmKill for the
	// collect shortcut. Collect is also destructive: it unregisters
	// the worker AND wipes its sandbox, so a misclick should not
	// silently make a delegated worker disappear from under the
	// root agent's feet.
	workersConfirmCollect string
	sessionsSel           int

	// sessionsConfirmDelete is the session_id pending a delete
	// confirmation. Empty when no prompt is up. Same y/n gating
	// pattern as workersConfirmKill / workersConfirmCollect.
	sessionsConfirmDelete string

	factsSel int

	// factsConfirmDelete is the fact ID pending a delete
	// confirmation in the /fact overlay. Zero when no prompt is up.
	factsConfirmDelete uint64

	workers  []facade.WorkerInfo
	sessions []facade.SessionInfo
	facts    []facade.FactDetail

	// focus tells the model where the user's attention is right now
	// — the writing box at the bottom (composerFocus) or the chat
	// transcript above (chatFocus). It changes on mouse click and
	// nothing else (no "modes", no Esc shortcuts). The renderer uses
	// it to paint a faint border on the unfocused side and the
	// keymap uses it to route arrows: composerFocus → cursor in the
	// box, chatFocus → selection between messages.
	focus focusKind

	// chatSel is the index of the currently selected message in
	// m.messages when focus == chatFocus. Meaningless under
	// composerFocus. We seed it to the last message on first focus
	// switch into the chat so up-arrow has somewhere to start.
	chatSel int

	splash bool
	toasts []Toast

	statusVersion     string
	copiedNotify      bool
	keepToolsExpanded bool

	// guard is the latest context-guard snapshot for the root chat,
	// refreshed at the end of every root turn. Drives the footer
	// gauge chip. Zero (Enabled=false) while talking to a worker or
	// when the root has no context_guard block.
	guard facade.ContextGuardStatus

	// guardFingerprint is the last compaction fingerprint observed in
	// session state. When a refresh sees a different non-empty value
	// (and guardSeeded is already set) we know a fresh compaction just
	// happened and surface the notice row.
	guardFingerprint string

	// guardSeeded is set once the first guard refresh has run for the
	// active session, so resuming a session that already carries a
	// compaction does not spuriously fire the notice on the first read.
	guardSeeded bool

	// streamCancel is used by the next streaming-cancel work; we keep
	// the field here already so future event-handling lands cleanly.
	streamCancel context.CancelFunc

	// flushPending is true when a streamed chunk was rendered
	// through the throttled markdown cache and may have left the
	// in-flight bubble showing stale (truncated) text. It signals
	// that a trailing-edge repaint tick is already scheduled, so we
	// only ever have one in flight at a time.
	flushPending bool

	// streamSeq is a monotonic counter used to mint a fresh StreamID
	// for each turn. The active stream's bubble carries that ID so
	// coalescing finds it by identity, never by array position.
	streamSeq int

	// rootStreamID is the StreamID of the root reply currently being
	// streamed, or "" when the root is idle. Worker streams keep
	// their own per-worker IDs in workerStreamIDs.
	rootStreamID string

	// workerStreamIDs maps a worker ID to the StreamID of its
	// in-flight reply bubble. An entry exists only while that worker
	// is actively streaming text.
	workerStreamIDs map[string]string

	// streamSpinner drives the small "thinking…" indicator that
	// lives between the chat viewport and the writing box while
	// the root agent is producing a reply. Visible only while
	// streamCancel != nil; otherwise the streaming-bar zone is
	// painted blank.
	streamSpinner spinner.Model

	// lifecycleCancel unsubscribes the root chat from the worker
	// lifecycle feed. We hold it on the model so the goroutine
	// the facade spins up for the subscription gets cleaned up on
	// Quit (the View / Update path doesn't otherwise know about
	// the goroutine).
	lifecycleCancel func()

	// suppressNextReloadNotice tells the reloadEventMsg handler to
	// skip the generic "config reloaded" system row exactly once.
	// We set it whenever an in-TUI save (editor_overlay,
	// secret_overlay, ...) already appended a more specific
	// confirmation ("MCP saved", "agent saved", ...) — without
	// this flag the user gets two near-identical lines for the
	// same action. External edits (vcs checkout, external editor
	// save) leave the flag false so the user still gets the
	// passive "config reloaded" cue.
	suppressNextReloadNotice bool

	// palette is the slash-command autocomplete popup state.
	// Recomputed from m.composer.Value() after every keystroke
	// that may have touched the composer; rendered above the
	// writing box by overlayPaletteAboveComposer in render().
	// Hidden by default (Visible == false from the zero value)
	// so the popup only appears once the user types '/'.
	palette paletteState

	// historyIdx tracks shell-style recall of previously sent user
	// messages via Ctrl+Up / Ctrl+Down in the composer. -1 means
	// "not currently browsing history" (the composer holds whatever
	// the user typed). 0 is the most recent sent message, 1 the one
	// before it, and so on. It is reset to -1 whenever the user
	// submits, edits the buffer with a normal keystroke, or focus
	// leaves the composer, so recall always starts fresh.
	historyIdx int

	// historyStash preserves the in-progress draft the user had
	// typed before they started browsing history, so Ctrl+Down past
	// the most recent entry restores it verbatim instead of leaving
	// an empty box.
	historyStash string
}

// NewModel constructs the top-level Model. The Facade may be nil so
// the TUI can boot in an error-state when the app couldn't (e.g. no
// root agent configured).
func NewModel(facade facade.Facade, useNerdFont bool, version string) Model {
	theme := NewTheme(useNerdFont)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = theme.AccentText()
	return Model{
		facade:             facade,
		theme:              theme,
		chat:               newChatView(theme, true),
		composer:           newComposer(theme),
		splash:             true,
		statusVersion:      version,
		activeInterlocutor: rootInterlocutor,
		chatHistories:      make(map[string][]Message),
		streamSpinner:      sp,
		// Focus defaults to the writing box so the user lands ready
		// to type. chatSel is -1 to mean "no selection" — only set
		// to a real index when focus flips to the chat.
		focus:      composerFocus,
		chatSel:    -1,
		historyIdx: -1,
	}
}

// NewModelWithAutoScroll is like NewModel but lets the caller seed
// the chat's auto-scroll behaviour and keep-tools-expanded behaviour.
func NewModelWithAutoScroll(facade facade.Facade, useNerdFont bool, version string, autoScroll bool, keepToolsExpanded bool) Model {
	m := NewModel(facade, useNerdFont, version)
	m.chat = newChatView(m.theme, autoScroll)
	m.keepToolsExpanded = keepToolsExpanded
	return m
}

// Init implements tea.Model. We schedule a tea.Tick to dismiss the
// splash screen after a short delay, and start listening on the
// reload channel so config-file changes refresh open overlays.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg {
			return splashDoneMsg{}
		}),
	}
	if m.facade != nil {
		cmds = append(cmds, waitForReload(m.facade.SubscribeReload()))
		// Subscribe to worker lifecycle events. We can't store the
		// cancel function here (Init has Model by value), so we
		// punt the actual subscribe into a tea.Cmd that produces
		// lifecycleSubscribedMsg — Update will then save the
		// cancel on the model and arm the listener.
		cmds = append(cmds, subscribeLifecycleCmd(m.facade))
		// Kick the workers poll ticker. This is a low-frequency
		// belt-and-braces refresh that keeps the footer chip and
		// any open workers overlay in sync with the manager's
		// real state regardless of whether the lifecycle bus
		// happened to deliver every transition (the bus is
		// best-effort drop-on-full, and there's an unavoidable
		// subscribe-time race window during Init). Polling is
		// O(N_workers) on a local in-memory snapshot — cheap
		// enough to do every half second forever.
		cmds = append(cmds, pollWorkersCmd())
	}
	return tea.Batch(cmds...)
}

// subscribeLifecycleCmd returns a tea.Cmd that subscribes the
// caller to the workers manager's lifecycle feed and emits a
// lifecycleSubscribedMsg carrying the (stream, cancel) pair. The
// indirection exists because Init has Model by value — we can't
// stash the cancel function there.
func subscribeLifecycleCmd(f facade.Facade) tea.Cmd {
	return func() tea.Msg {
		stream, cancel := f.SubscribeWorkerLifecycle()
		return lifecycleSubscribedMsg{stream: stream, cancel: cancel}
	}
}

// lifecycleSubscribedMsg is the bridge message from the
// Init-time subscription Cmd to Update, where we have *Model and
// can store the cancel function.
type lifecycleSubscribedMsg struct {
	stream <-chan facade.WorkerLifecycleEvent
	cancel func()
}

// reloadEventMsg is delivered when the App's file watcher rebuilt
// the in-memory state. The TUI uses it to refresh any open overlay
// (Settings) so the user sees the new config without restarting.
type reloadEventMsg struct{}

// waitForReload returns a tea.Cmd that blocks on ch until one
// ReloadEvent arrives, then emits a reloadEventMsg. After Update
// processes the message it must re-arm the listener — see the
// reloadEventMsg case in Update.
func waitForReload(ch <-chan facade.ReloadEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		<-ch
		return reloadEventMsg{}
	}
}

// workerLifecycleMsg is delivered when a worker is spawned or
// reaches a terminal state. It bundles the event itself with the
// upstream stream so Update can re-arm the listener without
// re-subscribing (which would leak the previous subscription).
type workerLifecycleMsg struct {
	event  facade.WorkerLifecycleEvent
	stream <-chan facade.WorkerLifecycleEvent
}

// waitForLifecycle returns a tea.Cmd that blocks on ch until one
// lifecycle event arrives, then emits a workerLifecycleMsg. The
// Update branch must re-arm the listener with the same `stream`
// reference so the underlying goroutine on the facade side keeps
// publishing without being torn down.
//
// A nil channel returns a nil Cmd; this lets the Init wiring stay
// defensive when the facade has no workers manager yet (boot path
// before App.New finishes, or test fakes that return nil).
func waitForLifecycle(ch <-chan facade.WorkerLifecycleEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil // upstream cancelled; stop the dispatch chain
		}
		return workerLifecycleMsg{event: ev, stream: ch}
	}
}

// splashDoneMsg is delivered when the splash timer fires.
type splashDoneMsg struct{}

// streamFlushMsg is the trailing-edge repaint tick. It fires shortly
// after a throttled streaming chunk so the in-flight bubble re-renders
// its full text even when the stream goes briefly quiet (model pauses
// mid-reply, or the last chunk of a burst landed inside the markdown
// throttle window). Without it, that final chunk's tail stays hidden
// until the next event or a click forces a repaint.
type streamFlushMsg struct{}

// streamFlushDelay is how long after a throttled chunk we schedule the
// trailing repaint. Slightly longer than markdownThrottle so the
// forced re-render is guaranteed to fall outside the throttle window.
const streamFlushDelay = markdownThrottle + 50*time.Millisecond

// scheduleStreamFlush returns a tick that fires a streamFlushMsg after
// streamFlushDelay.
func scheduleStreamFlush() tea.Cmd {
	return tea.Tick(streamFlushDelay, func(time.Time) tea.Msg {
		return streamFlushMsg{}
	})
}

// copiedDoneMsg is delivered when the clipboard copy notification timer fires.
type copiedDoneMsg struct{}

// workersPollMsg is the periodic tick that drives the
// belt-and-braces refresh of the cached workers list. We re-arm
// it from its own Update branch so the ticker keeps running for
// the lifetime of the TUI.
type workersPollMsg struct{}

// workersPollInterval is how often we re-pull the workers list
// from the facade. Half a second is fast enough to feel real-time
// for spawn/collect transitions (the user perceives sub-second
// updates as "live") and slow enough that the cost is invisible.
// The pull itself is an in-memory snapshot copy on the manager
// side, no I/O, no locks held across goroutines.
const workersPollInterval = 500 * time.Millisecond

// pollWorkersCmd returns a tea.Cmd that fires a workersPollMsg
// after workersPollInterval. The handler in Update is responsible
// for both consuming the message AND re-arming the ticker; this
// keeps the polling loop self-perpetuating without needing a
// background goroutine on the Model side.
func pollWorkersCmd() tea.Cmd {
	return tea.Tick(workersPollInterval, func(time.Time) tea.Msg {
		return workersPollMsg{}
	})
}

// agentChunkMsg carries one streamed event from the running agent to
// the model. The stream is broken into a sequence of chunks so the
// TUI can render assistant text as it arrives instead of waiting for
// the full reply.
type agentChunkMsg struct {
	text        string
	toolCalls   []facade.ToolCallInfo
	toolResults []facade.ToolResultInfo
	// replace marks a chunk whose text REPLACES the running streamed
	// reply rather than appending to it. The A2A executor sends the
	// final, complete artifact with Append=false after the incremental
	// partials; honouring it stops the full copy from being
	// concatenated onto the partials (a duplicated reply).
	replace bool
	// agentError marks a chunk whose text is a failure reported by the
	// agent run (the executor surfaces failed turns as a task-failed
	// event tagged Role "error", not as the stream's err). The TUI
	// renders it as a MessageAgentError special row rather than as
	// ordinary root reply text.
	agentError bool
	err        error
	done       bool
	next       tea.Cmd // command that fetches the next chunk; nil when done
}

// agentEventMsg keeps backwards compatibility with the previous
// single-shot streaming protocol. Update treats it as a single
// agentChunkMsg with done=true so existing tests do not change
// semantics.
type agentEventMsg = agentChunkMsg

// streamStartedMsg carries the channel + cancel pair created by
// startStream. The model stores the cancel so Esc can abort the run.
type streamStartedMsg struct {
	cancel context.CancelFunc
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// When the embedded editor overlay is open, everything routes
	// through it. We still let WindowSizeMsg propagate so layouts
	// stay consistent, and we intercept SaveMsg / CancelMsg to close
	// the overlay and persist the buffer.
	if m.editor != nil {
		return m.updateWithEditor(msg)
	}
	if m.secretPrompt != nil {
		return m.updateWithSecretPrompt(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.mode = Classify(msg.Width, msg.Height)
		m.tooSmall = m.mode == LayoutTooSmall
		m.resizeChat()
		return m, nil

	case splashDoneMsg:
		m.splash = false
		// Surface any root-build failure right after the splash so the
		// user sees the real reason (e.g. unknown provider, bad model
		// id) before they bother typing.
		if m.facade != nil {
			if err := m.facade.RootBuildError(); err != nil {
				m.messages = append(m.messages, Message{
					Kind: MessageError,
					Time: time.Now(),
					Text: "Root agent failed to build: " + err.Error(),
				})
				m.chat.SetMessages(m.messages)
			}
			// Seed the context-guard gauge from the active session so
			// the footer chip reflects the real state immediately on
			// boot (e.g. when resuming a long conversation). No notice
			// fires: this is the seed read, not a fresh compaction.
			m.refreshGuard(false)
		}
		return m, nil

	case copiedDoneMsg:
		m.copiedNotify = false
		return m, nil

	case agentChunkMsg:
		return m.handleAgentChunk(msg)

	case streamFlushMsg:
		// Trailing-edge repaint: a previous streamed chunk rendered
		// through the throttled markdown cache and may have left the
		// in-flight bubble showing truncated text. Force one
		// unthrottled re-render so the full text is visible even if
		// the stream has gone quiet.
		m.flushPending = false
		if m.splash {
			return m, nil
		}
		m.chat.forceStreamRender = true
		m.chat.SetMessages(m.messages)
		return m, nil

	case workerStreamMsg:
		return m.handleWorkerStream(msg)

	case streamStartedMsg:
		m.streamCancel = msg.cancel
		// streamStartedMsg is currently unused (submitComposer
		// installs the cancel directly and kicks the spinner
		// itself). Kept here as a future hook for async stream
		// setup; safe no-op when nothing produces it.
		return m, nil

	case spinner.TickMsg:
		// Calling Update is what re-chains the next tick (the returned cmd
		// is the re-arm). Stop calling it once the stream ends so the tick
		// chain dies naturally instead of running forever in the background.
		if m.streamCancel == nil {
			return m, nil
		}
		var cmd tea.Cmd
		m.streamSpinner, cmd = m.streamSpinner.Update(msg)
		return m, cmd

	case reloadEventMsg:
		// Re-arm the listener first so we never miss the next event,
		// then refresh any open overlay that displays config-derived
		// data. The chat history is intentionally left untouched.
		var cmds []tea.Cmd
		if m.facade != nil {
			cmds = append(cmds, waitForReload(m.facade.SubscribeReload()))
		}
		// Sessions and workers also derive from config — refresh
		// the lists feeding any overlay that might be open so a
		// fresh agents.yaml shows up immediately without the
		// user having to close+reopen the overlay.
		m.refreshOverlayData()
		// Suppress the generic "config reloaded" line when the
		// reload was triggered by an in-TUI save (the editor /
		// secret overlay already appended a more specific
		// confirmation). External edits — file watcher catching
		// a vcs checkout, /config reload typed manually — still
		// produce the notice.
		if !m.suppressNextReloadNotice {
			m.messages = append(m.messages, Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: "config reloaded",
			})
		}
		m.suppressNextReloadNotice = false
		m.chat.SetMessages(m.messages)
		return m, tea.Batch(cmds...)

	case mcpAuthDoneMsg:
		// Follow-up to /mcp authenticate. The starting system
		// message was already appended when the user submitted the
		// command; this branch just confirms (or reports) the
		// outcome without blocking the TUI loop.
		var row Message
		if msg.err != nil {
			row = Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("authenticate %s: %v", msg.name, msg.err),
			}
		} else {
			row = Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: "authenticated " + msg.name,
			}
		}
		m.messages = append(m.messages, row)
		m.chat.SetMessages(m.messages)
		return m, nil

	case mcpTestDoneMsg:
		// Follow-up to the "Test connection" action / a future
		// /mcp test slash command. The status string carries
		// the ✓/✗ + details so we can surface it verbatim.
		var row Message
		if msg.err != nil {
			row = Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: fmt.Sprintf("test %s: %v", msg.name, msg.err),
			}
		} else {
			row = Message{
				Kind: MessageSystem,
				Time: time.Now(),
				Text: fmt.Sprintf("test %s: %s", msg.name, msg.status),
			}
		}
		m.messages = append(m.messages, row)
		m.chat.SetMessages(m.messages)
		return m, nil

	case lifecycleSubscribedMsg:
		// Store the cancel so we can clean up the goroutine the
		// facade spawned for this subscription when baifo exits.
		// Arm waitForLifecycle so the first event we get back
		// turns into a workerLifecycleMsg below.
		m.lifecycleCancel = msg.cancel
		return m, waitForLifecycle(msg.stream)

	case workersPollMsg:
		// Periodic refresh of the cached workers list. Always
		// re-arms the ticker so the loop never stops; the pull
		// itself is skipped when there's no facade (boot-time
		// error state) since there's nothing to read.
		if m.facade != nil {
			m.workers = m.facade.ListWorkers()
		}
		return m, pollWorkersCmd()

	case workerLifecycleMsg:
		// Re-arm with the SAME stream so the underlying facade
		// goroutine keeps publishing — we don't want to leak
		// subscriptions per event.
		cmd := waitForLifecycle(msg.stream)
		row := formatLifecycleRow(msg.event, m.theme)

		// If the worker has reached a terminal state, we can clean up
		// its chat history cache.
		if msg.event.Kind == facade.WorkerLifecycleDone ||
			msg.event.Kind == facade.WorkerLifecycleFailed ||
			msg.event.Kind == facade.WorkerLifecycleKilled {
			if m.chatHistories != nil && msg.event.WorkerID != m.activeInterlocutor {
				delete(m.chatHistories, msg.event.WorkerID)
			}
		}

		if row.Text != "" {
			// Insert the lifecycle notice directly. The streaming
			// bubble is found by StreamID, not by position, so
			// appending here can never split an in-flight reply.
			m.messages = append(m.messages, row)
			m.chat.SetMessages(m.messages)
		}
		// Refresh the workers list so the Workers tab stays in
		// sync even if the user never opens it.
		if m.facade != nil {
			m.workers = m.facade.ListWorkers()
		}
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseClickMsg, tea.MouseWheelMsg:
		// Mouse routing lives in chat_focus.go: clicks switch
		// focus and (on tools) toggle expansion; wheel always
		// scrolls the chat regardless of focus.
		mm, _ := msg.(tea.MouseMsg)
		return m.handleMouse(mm)
	}

	// Forward unhandled messages to the composer so it can do its
	// thing (cursor blink, etc.).
	var cmd tea.Cmd
	m.composer.ta, cmd = m.composer.ta.Update(msg)
	return m, cmd
}

// resizeChat keeps the chat viewport in sync with the current layout.
// Called on every WindowSize and after operations that change which
// zones are visible (composer hidden / shown, settings overlay open).
func (m *Model) resizeChat() {
	// The chat is the only main view now — overlays float over it.
	// composerVisible is therefore always true.
	zones := ZonesFor(m.height, true)
	// The chat panel carries the same side margin as the composer and
	// status bar so all three left/right edges align. Shrink its width
	// by 2*panelSideMargin; the View() applies the matching MarginLeft.
	chatWidth := m.width - 2*panelSideMargin
	if chatWidth < 1 {
		chatWidth = m.width
	}
	m.chat.SetSize(chatWidth, zones.Main)
	m.chat.SetMessages(m.messages)
	// The composer is a bordered box with side margins. Its inner
	// content width is the screen width minus the two side margins,
	// the box border (2) and the box padding (2). Keep this in sync
	// with composer.View()'s geometry.
	composerContent := m.width - 2*composerSideMargin - 4
	if composerContent < 1 {
		composerContent = 1
	}
	m.composer.ta.SetWidth(composerContent)
}

// handleKey deals with global key bindings. Composer-local keys (text
// input, Ctrl+Enter newline) reach the textarea via the default path
// at the bottom of Update.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Each overlay claims the keyboard while it's open. Settings
	// has its own handler; the sessions/worker overlays delegate
	// to a shared per-key handler that knows their navigation
	// semantics. Ctrl+C is the only global override that bypasses
	// everything.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.sessionsOpen {
		return m.handleSessionsOverlayKey(msg)
	}
	if m.workersOpen {
		return m.handleWorkersOverlayKey(msg)
	}
	if m.factsOpen {
		return m.handleFactsOverlayKey(msg)
	}
	if m.catalogOpen {
		return m.handleCatalogOverlayKey(msg)
	}

	switch msg.String() {
	case "ctrl+/", "f1":
		m.helpOpen = !m.helpOpen
		return m, nil
	case "ctrl+up":
		// Shell-style recall: pull the previous user message into
		// the composer. Only meaningful while typing (composerFocus)
		// and when the autocomplete popup isn't capturing arrows.
		if m.focus == composerFocus && !m.palette.Visible {
			m.recallHistory(-1)
			return m, nil
		}
	case "ctrl+down":
		// The inverse of ctrl+up: walk back toward the most recent
		// message and finally restore the in-progress draft.
		if m.focus == composerFocus && !m.palette.Visible {
			m.recallHistory(+1)
			return m, nil
		}
	case "esc":
		switch {
		case m.palette.Visible:
			// Hide the autocomplete popup first. Esc only
			// cancels the stream / closes help once the popup
			// is out of the way, so the user can dismiss
			// "/mcp" suggestions without also cancelling a
			// long-running reply they happen to be watching.
			m.palette = paletteState{}
		case m.helpOpen:
			m.helpOpen = false
		case m.streamCancel != nil:
			m.streamCancel()
			m.streamCancel = nil
		}
		return m, nil
	case "up":
		if m.palette.Visible {
			m.palette.move(-1)
			return m, nil
		}
		if m.focus == chatFocus {
			return m.moveChatSelection(msg.String()), nil
		}
		// Composer focus, popup hidden: fall through so the
		// textarea moves the cursor.
	case "down":
		if m.palette.Visible {
			m.palette.move(+1)
			return m, nil
		}
		if m.focus == chatFocus {
			return m.moveChatSelection(msg.String()), nil
		}
	case "tab":
		// Tab is the dedicated "accept suggestion" key.
		if m.palette.Visible && m.palette.Selected >= 0 &&
			m.palette.Selected < len(m.palette.Items) {
			item := m.palette.Items[m.palette.Selected]
			if newLine, ok := m.palette.accept(m.composer.ta.Value()); ok {
				m.composer.ta.SetValue(newLine)
				m.composer.ta.CursorEnd()
				if item.IsLeaf {
					// Clear the palette so the popup closes for leaf commands
					m.palette = paletteState{}
				} else {
					m.palette.refresh(newLine)
				}
				return m, nil
			}
		}
	case "enter":
		// Enter always SENDS. Completion is Tab's job: the old
		// behaviour (Enter accepts the highlighted suggestion, a
		// second Enter submits) made every command a double-Enter
		// dance. Now the popup is purely advisory — if the user
		// wants the suggestion they press Tab; Enter fires whatever
		// is actually typed in the composer, popup or not.
		if m.palette.Visible {
			m.palette = paletteState{}
		}
		if m.focus == chatFocus {
			// Enter on the chat side toggles the focused tool
			// row; on user/root rows it does nothing.
			return m.toggleSelectedTool(), nil
		}
		return m.submitComposer()
	case "pgup", "pgdown", "home", "end":
		// With chatFocus the arrows move the message selection;
		// with composerFocus they fall through to the textarea
		// so the user can move the cursor inside a multi-line
		// message they're writing.
		if m.focus == chatFocus {
			return m.moveChatSelection(msg.String()), nil
		}
		// Composer focus: fall through to the textarea handler.
	case "y", "c":
		if m.focus == chatFocus && m.chatSel >= 0 && m.chatSel < len(m.messages) {
			cleanText := m.selectedMessageCopyableText()
			_ = clipboard.WriteAll(cleanText)
			m.copiedNotify = true
			return m, tea.Batch(
				tea.SetClipboard(cleanText),
				tea.Tick(1200*time.Millisecond, func(t time.Time) tea.Msg {
					return copiedDoneMsg{}
				}),
			)
		}
	}

	var cmd tea.Cmd
	before := m.composer.ta.Value()
	m.composer.ta, cmd = m.composer.ta.Update(msg)
	// Any keystroke that actually edits the buffer ends history
	// recall: the user is composing a new message, so Ctrl+Up should
	// start again from the latest sent one. Pure cursor motion
	// (arrows, word jumps) leaves the value untouched and keeps the
	// recall position, which is what the user expects.
	if m.composer.ta.Value() != before {
		m.historyIdx = -1
	}
	// Refresh the popup AFTER every keystroke the composer
	// processes — that way the popup is always in sync with
	// what's actually in the buffer, including pastes,
	// backspaces and cursor moves the textarea handled
	// internally.
	m.palette.refresh(m.composer.ta.Value())
	return m, cmd
}

// recallHistory walks the sent-message history shown in the composer.
//
// dir == -1 moves toward older messages (Ctrl+Up), dir == +1 toward
// newer ones and finally back to the live draft (Ctrl+Down). The
// index is 0-based from the most recent user message; -1 means "not
// browsing — show the draft". On first step back we stash the current
// draft so Ctrl+Down past the newest entry restores it verbatim.
func (m *Model) recallHistory(dir int) {
	hist := m.userMessageHistory()
	if len(hist) == 0 {
		return
	}

	// Entering recall from the live draft: remember it.
	if m.historyIdx == -1 {
		if dir > 0 {
			return // Ctrl+Down with nothing to come back to: no-op.
		}
		m.historyStash = m.composer.ta.Value()
	}

	next := m.historyIdx - dir // -1 (up) increments age; +1 (down) decrements
	switch {
	case next < -1:
		next = -1
	case next > len(hist)-1:
		next = len(hist) - 1
	}
	if next == m.historyIdx {
		return
	}
	m.historyIdx = next

	if next == -1 {
		// Back to the draft we stashed on entry.
		m.composer.ta.SetValue(m.historyStash)
	} else {
		// hist is newest-first, so index maps directly.
		m.composer.ta.SetValue(hist[next])
	}
	m.composer.ta.CursorEnd()
	m.palette.refresh(m.composer.ta.Value())
}

// userMessageHistory returns the text of every user-authored message
// in the current transcript, newest first, skipping blanks. Used by
// the Ctrl+Up / Ctrl+Down composer recall.
func (m Model) userMessageHistory() []string {
	var out []string
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.Kind != MessageUser {
			continue
		}
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		out = append(out, msg.Text)
	}
	return out
}

// submitComposer takes the composer text, pushes it as a user message,
// and starts the agent stream. If the text starts with '/', it is
// dispatched as a slash command instead.
func (m Model) submitComposer() (tea.Model, tea.Cmd) {
	text := m.composer.Value()
	if text == "" {
		return m, nil
	}

	// Clear the autocomplete popup on submit: the user committed
	// the line, so any in-flight suggestion list is now stale.
	// (The popup would also disappear naturally once the composer
	// is reset, but doing it explicitly here keeps the state flip
	// next to its cause.)
	m.palette = paletteState{}

	// Sending commits the line, so history recall starts fresh next
	// time: the just-sent message becomes the newest entry.
	m.historyIdx = -1
	m.historyStash = ""

	if strings.HasPrefix(text, "/") {
		res := m.handleSlashCommand(text)
		m.composer.Reset()
		return m.applySlashResult(res)
	}

	if m.facade == nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: "No agent configured — set root.llm in baifo.yaml.",
		})
		m.composer.Reset()
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	if err := m.facade.RootBuildError(); err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: "Root agent failed to build: " + err.Error(),
		})
		m.composer.Reset()
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	// When the active interlocutor is a worker, route the message
	// through SendToWorker instead of the root's streaming runner.
	// The reply will arrive as workerStreamMsg events through the
	// existing subscription that switchInterlocutor opened.
	if m.activeInterlocutor != rootInterlocutor {
		m.messages = append(m.messages, Message{Kind: MessageUser, Time: time.Now(), Text: text})
		m.composer.Reset()
		m.chat.SetMessages(m.messages)
		if err := m.facade.SendToWorker(context.Background(), m.activeInterlocutor, text); err != nil {
			m.messages = append(m.messages, Message{
				Kind: MessageError,
				Time: time.Now(),
				Text: "send to worker: " + err.Error(),
			})
			m.chat.SetMessages(m.messages)
		}
		return m, nil
	}

	m.messages = append(m.messages, Message{Kind: MessageUser, Time: time.Now(), Text: text})
	m.composer.Reset()
	m.chat.SetMessages(m.messages)

	// Capture whether a tick chain is already alive before installing
	// the new cancel. agentEventMsg can also set streamCancel, so a
	// chain can be running even if the composer was never re-submitted
	// while locked. The guard below is defensive: in normal flow the
	// composer is disabled during streaming, so alreadyStreaming is
	// false here, but we still avoid double-seeding in edge cases.
	alreadyStreaming := m.streamCancel != nil
	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel
	// Kick the spinner ticker BEFORE the first chunk arrives.
	// The LLM's first token can take a couple of seconds and
	// that wait is precisely the moment the user most needs a
	// "yes, something is happening" cue. Waiting for the first
	// agentChunkMsg to arm the ticker (as we used to) meant the
	// spinner stayed frozen during the longest, most anxious
	// part of every turn.
	//
	// Only seed the tick chain when no chain is already alive. Two
	// concurrent chains race on the spinner's internal tag counter;
	// when both pending ticks carry a stale tag they both die and the
	// spinner freezes for the rest of the session.
	if alreadyStreaming {
		return m, startStreamCmd(ctx, m.facade, text)
	}
	return m, tea.Batch(startStreamCmd(ctx, m.facade, text), m.streamSpinner.Tick)
}

// handleAgentChunk appends a streamed chunk to the trailing root
// message (or creates one), then schedules the next chunk read. A
// non-nil err produces an error row and ends the stream.
//
// Tool calls and results are appended as their own rows (NOT merged
// into the trailing root text). The current decision is to show every
// event separately so the user can read the agent's full reasoning
// trace; future iterations may collapse call+result into a single row.
func (m Model) handleAgentChunk(msg agentChunkMsg) (tea.Model, tea.Cmd) {
	// If the user is currently looking at a worker's chat, the active
	// m.messages slice belongs to the worker. We must append the root's
	// incoming chunks to the root's stashed history instead, and we do
	// NOT update the active chat view.
	targetMsgs := m.messages
	isBackground := m.activeInterlocutor != rootInterlocutor
	if isBackground {
		targetMsgs = m.chatHistories[rootInterlocutor]
	}

	if msg.err != nil {
		// A user-initiated cancel (Esc) surfaces as context.Canceled
		// from the stream. That is not a failure — render it as a
		// quiet system row instead of an alarming error row.
		if errors.Is(msg.err, context.Canceled) {
			targetMsgs = append(targetMsgs, Message{Kind: MessageSystem, Time: time.Now(), Text: "generation cancelled"})
		} else {
			targetMsgs = append(targetMsgs, Message{Kind: MessageError, Time: time.Now(), Text: msg.err.Error()})
		}
		if !isBackground {
			m.chat.streamingID = ""
			m.chat.SetMessages(targetMsgs)
			m.messages = targetMsgs
		} else {
			m.chatHistories[rootInterlocutor] = targetMsgs
			m.messages = m.chatHistories[m.activeInterlocutor]
		}
		m.rootStreamID = ""
		m.streamCancel = nil
		return m, nil
	}

	if msg.agentError && msg.text != "" {
		// A failed turn the agent reported as a task-failed event.
		// Render it as a special error row (not as root reply text)
		// and end the stream.
		targetMsgs = append(targetMsgs, Message{
			Kind: MessageAgentError,
			Time: time.Now(),
			Text: msg.text,
		})
		m.rootStreamID = ""
		if !isBackground {
			m.chat.streamingID = ""
			m.chat.SetMessages(targetMsgs)
		}
	} else if msg.text != "" {
		// Mint a StreamID for this turn on the first text chunk, then
		// coalesce by that ID. Position in the slice is irrelevant:
		// the bubble is found by identity, so tool rows / notices /
		// errors inserted afterwards can never split it.
		if m.rootStreamID == "" {
			m.streamSeq++
			m.rootStreamID = "root-" + strconv.Itoa(m.streamSeq)
		}
		targetMsgs = coalesceStream(targetMsgs, m.rootStreamID, msg.text, msg.replace)
		if !isBackground {
			m.chat.streamingID = m.rootStreamID
		}
	}

	for _, call := range msg.toolCalls {
		// Deduplicate: the executor might emit the same tool call in partial
		// artifacts, the final aggregate artifact, and the final message.
		exists := false
		if call.CallID != "" {
			for _, existing := range targetMsgs {
				if existing.Kind == MessageToolCall && existing.ToolCallID == call.CallID {
					exists = true
					break
				}
			}
		}
		if exists {
			continue
		}

		targetMsgs = append(targetMsgs, Message{
			Kind:       MessageToolCall,
			Time:       time.Now(),
			Text:       formatToolCallSummary(call),
			ToolName:   call.Name,
			ToolCallID: call.CallID,
			ToolArgs:   call.Args,
		})
	}

	for _, result := range msg.toolResults {
		// Deduplicate for the same reason as tool calls.
		exists := false
		if result.CallID != "" {
			for _, existing := range targetMsgs {
				if existing.Kind == MessageToolResult && existing.ToolCallID == result.CallID {
					exists = true
					break
				}
			}
		}
		if exists {
			continue
		}

		targetMsgs = append(targetMsgs, Message{
			Kind:       MessageToolResult,
			Time:       time.Now(),
			Text:       formatToolResultSummary(result),
			ToolName:   result.Name,
			ToolCallID: result.CallID,
			ToolResult: result.Result,
		})
	}

	// A tool call or result is a semantic break in the reply: the
	// text that comes after it belongs to a fresh bubble. Clear the
	// active StreamID so the next text chunk mints a new one.
	if len(msg.toolCalls) > 0 || len(msg.toolResults) > 0 {
		m.rootStreamID = ""
		if !isBackground {
			m.chat.streamingID = ""
		}
	}

	if msg.text != "" || len(msg.toolCalls) > 0 || len(msg.toolResults) > 0 {
		if !isBackground {
			m.chat.SetMessages(targetMsgs)
		}
	}

	// Belt-and-braces refresh of the cached workers list whenever
	// the root agent issues a tool call or receives a tool result.
	if (len(msg.toolCalls) > 0 || len(msg.toolResults) > 0) && m.facade != nil {
		m.workers = m.facade.ListWorkers()
	}

	if isBackground {
		m.chatHistories[rootInterlocutor] = targetMsgs
	} else {
		m.messages = targetMsgs
	}

	if msg.done {
		m.rootStreamID = ""
		if !isBackground {
			m.chat.streamingID = ""
			if len(m.messages) > 0 {
				m.chat.SetMessages(m.messages)
			}
		}
		m.streamCancel = nil
		// One last workers refresh on stream end
		if m.facade != nil {
			m.workers = m.facade.ListWorkers()
		}
		m.refreshGuard(true)
		return m, nil
	}
	// Continue the stream. When this chunk carried text we may have
	// rendered it throttled, so arm a trailing-edge flush (once) to
	// repaint the full text if the stream goes quiet before the
	// next chunk arrives.
	if !isBackground && msg.text != "" && !m.flushPending {
		m.flushPending = true
		return m, tea.Batch(msg.next, scheduleStreamFlush())
	}
	return m, msg.next
}

// formatToolCallSummary renders the one-line preview shown in the
// chat for a MessageToolCall row. Keeps the args readable but bounded
// so the chat does not blow up on tools with verbose payloads.
func formatToolCallSummary(call facade.ToolCallInfo) string {
	name := call.Name
	if name == "" {
		name = "(unnamed tool)"
	}
	if len(call.Args) == 0 {
		return name + "()"
	}
	return name + "(" + truncateInline(formatArgsInline(call.Args), 120) + ")"
}

// formatToolResultSummary renders the one-line preview shown in the
// chat for a MessageToolResult row. Reports a marker plus the result
// trimmed to a manageable length; the full payload lives on the
// Message for a future expand-on-click view.
func formatToolResultSummary(result facade.ToolResultInfo) string {
	name := result.Name
	if name == "" {
		name = "(unnamed tool)"
	}
	if len(result.Result) == 0 {
		return name + " · ok"
	}
	return name + " · " + truncateInline(formatArgsInline(result.Result), 120)
}

// formatArgsInline renders a map[string]any as a compact "k=v, k=v"
// string, sorted by key for stable output. Values are shortened to
// their %v representation; nested structures are stringified verbatim
// (good enough for the summary line; the expand view will pretty-print).
func formatArgsInline(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString("=")
		fmt.Fprintf(&b, "%v", args[k])
	}
	return b.String()
}

// truncateInline cuts s to max runes (not bytes), appending "…" when
// truncation happens. Used only for the summary lines.
func truncateInline(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// startStreamCmd kicks off the streaming run. It spawns a goroutine
// that consumes the iterator and writes events to an in-process
// channel. The returned tea.Cmd reads ONE event from the channel and
// returns an agentChunkMsg whose next field reads the following one;
// BubbleTea schedules them sequentially, so the chat repaints chunk
// by chunk instead of jumping at the end.
func startStreamCmd(ctx context.Context, f facade.Facade, text string) tea.Cmd {
	type chunk struct {
		text        string
		toolCalls   []facade.ToolCallInfo
		toolResults []facade.ToolResultInfo
		replace     bool
		agentError  bool
		err         error
	}
	ch := make(chan chunk, 32)

	go func() {
		defer close(ch)
		// Recover any panic inside the iterator (e.g. a buggy ADK
		// callback) and surface it as a chunk error. Without this
		// guard a panic would leave the channel without a final
		// message, the TUI would block on readNext forever, and
		// BubbleTea's altscreen would freeze the terminal until the
		// user kills the window.
		defer func() {
			if r := recover(); r != nil {
				select {
				case ch <- chunk{err: fmt.Errorf("agent stream panicked: %v", r)}:
				default:
				}
			}
		}()
		for ev, err := range f.SendMessage(ctx, text) {
			if err != nil {
				ch <- chunk{err: err}
				return
			}
			if ev == nil {
				continue
			}
			// One ADK event may carry text, tool calls, tool results,
			// or a combination. We forward all three slots so the TUI
			// renders them as separate chat rows in the order the
			// stream delivered them.
			if ev.Text != "" || len(ev.ToolCalls) > 0 || len(ev.ToolResults) > 0 {
				ch <- chunk{
					text:        ev.Text,
					toolCalls:   ev.ToolCalls,
					toolResults: ev.ToolResults,
					replace:     ev.Replace,
					agentError:  ev.Role == "error",
				}
			}
		}
	}()

	var readNext func() tea.Msg
	readNext = func() (out tea.Msg) {
		// Same idea as the producer goroutine above: a panic inside
		// the BubbleTea cmd would leave the runtime in a corrupted
		// state. Convert it into an error chunk instead.
		defer func() {
			if r := recover(); r != nil {
				out = agentChunkMsg{err: fmt.Errorf("stream reader panicked: %v", r), done: true}
			}
		}()
		c, ok := <-ch
		if !ok {
			return agentChunkMsg{done: true}
		}
		if c.err != nil {
			return agentChunkMsg{err: c.err, done: true}
		}
		return agentChunkMsg{
			text:        c.text,
			toolCalls:   c.toolCalls,
			toolResults: c.toolResults,
			replace:     c.replace,
			agentError:  c.agentError,
			next:        tea.Cmd(readNext),
		}
	}
	return tea.Cmd(readNext)
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	// Cell-motion mouse tracking: gives us click and wheel events
	// at cell granularity, which is enough for click-to-focus and
	// wheel-to-scroll. We don't need motion-during-drag (AllMotion).
	// The trade-off the user accepts: terminal-side "select text to
	// copy" requires holding Shift while selecting (all modern
	// terminals support this).
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// render produces the actual screen content as a single styled string.
func (m Model) render() string {
	if m.tooSmall {
		return m.theme.StatusWarning().Render("terminal too small (need at least 24 rows)")
	}
	if m.splash {
		return renderSplash(m.theme, m.width)
	}
	if m.editor != nil {
		return m.viewEditor()
	}

	// 4-zone vertical layout. The main area is always the chat;
	// alternate views (sessions, workers, settings) float over
	// it as modal overlays invoked via slash commands.
	zones := ZonesFor(m.height, true)

	header := renderHeader(m.theme, m.width)
	main := m.chat.View()
	streamBar := m.renderStreamingBar(m.width)
	status := renderStatusBar(m.theme, statusBarData{
		Model:          m.modelName(),
		A2AStatus:      "off",
		Talking:        m.activeInterlocutorLabel(),
		TalkingKind:    m.activeInterlocutorKind(),
		WorkersRunning: m.countRunningWorkers(),
		CopiedNotify:   m.copiedNotify,
		GuardEnabled:   m.guard.Enabled,
		GuardStrategy:  m.guard.Strategy,
		GuardPercent:   m.guard.Percent,
	}, m.width)
	_ = zones // kept to document the layout calc; SetSize already used it

	body := header + "\n" + main + "\n" +
		streamBar + "\n" +
		m.composer.View(m.width, m.focus == composerFocus) + "\n" +
		status

	// Splice the slash-command autocomplete popup into the rows
	// directly above the composer's top border. We do this BEFORE
	// the modal overlays below: those replace `body` entirely
	// (so the popup correctly disappears when /settings, /help,
	// etc. are open) but they also subsume the writing box, so
	// there's nothing to anchor the popup to anyway.
	if m.palette.Visible {
		popup := renderPalette(m.theme, m.palette, m.width)
		body = overlayPaletteAboveComposer(body, popup)
	}

	if m.helpOpen {
		body = renderHelp(m.theme, body, m.width, m.height)
	}
	if m.sessionsOpen {
		activeID := ""
		if m.facade != nil {
			activeID = m.facade.SessionID()
		}
		body = renderSessions(m.theme, m.sessions, activeID, m.sessionsSel, m.sessionsConfirmDelete, body, m.width, m.height)
	}
	if m.workersOpen {
		body = renderWorkers(m.theme, m.workers, m.workersSel, m.workersConfirmKill, m.workersConfirmCollect, body, m.width, m.height)
	}
	if m.factsOpen {
		body = renderFacts(m.theme, m.facts, m.factsSel, m.factsConfirmDelete, body, m.width, m.height)
	}
	if m.catalogOpen {
		body = renderCatalog(m.theme, m.catalogView, m.catalogSel, body, m.width, m.height)
	}
	if m.secretPrompt != nil {
		body = m.viewSecretPromptOverlay(body)
	}
	return body
}

// modelName returns the model label for the status bar, falling back
// gracefully when the facade is unset.
func (m Model) modelName() string {
	if m.facade == nil {
		return ""
	}
	return m.facade.ModelName()
}

// refreshOverlayData repopulates the data backing every visual
// overlay that derives from config (sessions, workers, settings).
// Called after a reload event so an open overlay sees the new
// state immediately, and at boot to seed the slices.
func (m *Model) refreshOverlayData() {
	if m.facade == nil {
		return
	}
	m.workers = m.facade.ListWorkers()
	if m.workersSel >= len(m.workers) {
		m.workersSel = 0
	}
	m.facts = m.facade.FactDetails()
	if m.factsSel >= len(m.facts) {
		m.factsSel = 0
	}
	if list, err := m.facade.ListSessions(context.Background()); err == nil {
		m.sessions = list
		if m.sessionsSel >= len(list) {
			m.sessionsSel = 0
		}
	}
	m.rebuildCatalog()
}

// refreshGuard pulls the current context-guard snapshot from the
// facade and stores it for the footer chip. When fireNotice is true
// and a fresh compaction is detected (the fingerprint changed since
// the last seeded read), it appends a highlighted MessageNotice row so
// the user sees that the conversation was compacted.
//
// The guard only applies to the root chat: while a worker chat is
// active we clear the snapshot so the chip disappears, and we never
// fire a notice for it.
func (m *Model) refreshGuard(fireNotice bool) {
	if m.facade == nil {
		return
	}
	if m.activeInterlocutor != rootInterlocutor {
		m.guard = facade.ContextGuardStatus{}
		return
	}

	st := m.facade.ContextGuardStatus(context.Background())
	if fireNotice && m.guardSeeded && st.Fingerprint != "" && st.Fingerprint != m.guardFingerprint {
		m.messages = append(m.messages, Message{
			Kind: MessageNotice,
			Time: time.Now(),
			// Body is the actual summary the model produced, shown
			// only when the user expands the row. No explanatory
			// boilerplate: the band itself says "context guard", which
			// is all the user needs to know fired.
			Text: st.Summary,
		})
		m.chat.SetMessages(m.messages)
	}
	m.guardFingerprint = st.Fingerprint
	m.guardSeeded = true
	m.guard = st
}

// formatLifecycleRow turns a facade.WorkerLifecycleEvent into a
// chat row. We render lifecycle notices as compact MessageSystem
// rows so they read like passive notifications, not like the root
// agent talking. The rendered text is intentionally short — the
// Workers tab is where the user goes for detail.
//
// Returns a zero Message when the event is not interesting to
// render (today: nothing falls into that branch, but the option is
// there for future event kinds).
func formatLifecycleRow(ev facade.WorkerLifecycleEvent, theme Theme) Message {
	id := ev.WorkerID
	if ev.Name != "" {
		id = ev.WorkerID + " (" + ev.Name + ")"
	}
	var text string
	switch ev.Kind {
	case facade.WorkerLifecycleSpawned:
		text = "worker " + id + " spawned"
	case facade.WorkerLifecycleDone:
		text = "worker " + id + " is done — call collect_agent for the output"
	case facade.WorkerLifecycleFailed:
		text = "worker " + id + " failed"
		if ev.LastEvent != "" {
			text += ": " + ev.LastEvent
		}
	case facade.WorkerLifecycleKilled:
		text = "worker " + id + " was killed"
		if ev.LastEvent != "" {
			text += " (" + ev.LastEvent + ")"
		}
	default:
		return Message{}
	}
	return Message{
		Kind: MessageSystem,
		Time: ev.Timestamp,
		Text: text,
	}
}

// activeInterlocutorKind returns "root" / "static" / "dynamic" for
// the chat's current interlocutor. Drives the colour of the
// interlocutor chip in the footer. Defaults to "root" when the
// kind can't be resolved (worker not in the cached list yet).
func (m Model) activeInterlocutorKind() string {
	if m.activeInterlocutor == rootInterlocutor {
		return "root"
	}
	for _, w := range m.workers {
		if w.ID == m.activeInterlocutor {
			if w.Kind != "" {
				return w.Kind
			}
			return "dynamic"
		}
	}
	return "dynamic"
}

// countRunningWorkers returns how many workers in the cached list
// are currently alive — meaning either actively processing a query
// (running) or waiting for the next one (idle). Both states count
// as "the worker exists and is consuming resources", which is what
// the footer chip is trying to surface. Workers in any terminal
// state (done / failed / killed) are excluded because they're just
// awaiting collection and no longer holding a runtime.
//
// We deliberately count idle workers too: a spawned worker that
// hasn't received its first query_agent yet is still very much a
// live worker the user wants to see in the chip. Counting only
// "running" caused the chip to appear lazily on the first query
// instead of immediately on spawn, which is what the user
// originally noticed.
func (m Model) countRunningWorkers() int {
	n := 0
	for _, w := range m.workers {
		switch w.Status {
		case "running", "idle":
			n++
		}
	}
	return n
}

// secretsCount returns the number of secrets configured in the
// active store. Used by the footer chip. Returns 0 when no facade
// is wired (e.g. boot-time error state).
func (m Model) secretsCount() int {
	if m.facade == nil {
		return 0
	}
	return len(m.facade.ListSecretNames())
}

// renderStreamingBar paints the one-line strip between the chat
// viewport and the writing box. When a stream is in flight (the
// runner is producing tokens for an active root reply), it shows
// the spinner frame plus a short "thinking…" label in the accent
// colour. When nothing is streaming, the row stays blank so the
// layout doesn't jump as streams start and finish.
//
// The label is bounded so it never overflows even on narrow
// terminals; if the row can't fit "thinking…" we drop it and keep
// just the spinner frame.
func (m Model) renderStreamingBar(width int) string {
	if m.streamCancel == nil {
		return strings.Repeat(" ", width)
	}
	// One left-aligned space so the spinner doesn't hug the
	// terminal edge.
	indent := " "
	glyph := m.streamSpinner.View()
	label := m.theme.FaintText().Render("thinking…")
	line := indent + glyph + " " + label
	if w := lipgloss.Width(line); w > width {
		line = indent + glyph
		if lipgloss.Width(line) > width {
			line = glyph
		}
	}
	// Pad to width so background colours (if added later) reach
	// the right edge.
	pad := width - lipgloss.Width(line)
	if pad < 0 {
		pad = 0
	}
	return line + strings.Repeat(" ", pad)
}

// applySlashResult is the shared post-dispatch handler for any
// slashResult-producing source. submitComposer feeds it the
// outcome of /-commands. Centralising the wiring here keeps the
// open-and-refresh path for overlays (Settings, Sessions, …) in
// one place regardless of which command triggered it.
//
// Callers are responsible for resetting their own input state
// (the composer in submitComposer's case, the palette in the
// palette handler's case) before invoking this method — the
// slashResult itself does not know which surface produced it.
func (m Model) applySlashResult(res slashResult) (tea.Model, tea.Cmd) {
	if res.resetChat {
		m.messages = nil
		// A session switch/new/delete changed which conversation is
		// active. Re-seed the guard gauge from the new session's state
		// without firing a notice (a session that already carries a
		// compaction must not spuriously announce it on load).
		m.guardSeeded = false
		m.refreshGuard(false)
	}
	// Closing the other top-level overlays before opening a new
	// one keeps the render order from picking the wrong winner.
	// View() composes overlays in a fixed sequence (settings →
	// sessions → workers) and the LAST true flag wins on the
	// screen; without this reset, opening Sessions from the
	// command palette while Workers was still flagged open
	// would render Workers on top. Help is left alone because
	// it's a transient toggle the user can dismiss with Esc.
	if res.openSessionsOverlay || res.openWorkersOverlay || res.openFactsOverlay || res.openCatalog != nil {
		m.sessionsOpen = false
		m.workersOpen = false
		m.factsOpen = false
		m.catalogOpen = false
	}
	if res.openSessionsOverlay {
		if m.facade != nil {
			if list, err := m.facade.ListSessions(context.Background()); err == nil {
				m.sessions = list
			}
		}
		if m.sessionsSel >= len(m.sessions) {
			m.sessionsSel = 0
		}
		m.sessionsOpen = true
	}
	if res.openWorkersOverlay {
		if m.facade != nil {
			m.workers = m.facade.ListWorkers()
		}
		if m.workersSel >= len(m.workers) {
			m.workersSel = 0
		}
		m.workersOpen = true
	}
	if res.openFactsOverlay {
		if m.facade != nil {
			m.facts = m.facade.FactDetails()
		}
		if m.factsSel >= len(m.facts) {
			m.factsSel = 0
		}
		m.factsOpen = true
	}
	if res.openCatalog != nil {
		m.catalogView = *res.openCatalog
		m.catalogSel = 0
		m.catalogOpen = true
	}
	if res.toggleHelp {
		m.helpOpen = !m.helpOpen
	}
	switch {
	case res.errorMessage != "":
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: res.errorMessage,
		})
	case res.systemMessage != "":
		m.messages = append(m.messages, Message{
			Kind: MessageSystem,
			Time: time.Now(),
			Text: res.systemMessage,
		})
	}
	if len(res.injectMessages) > 0 {
		now := time.Now()
		for _, im := range res.injectMessages {
			if im.Time.IsZero() {
				im.Time = now
			}
			m.messages = append(m.messages, im)
		}
	}
	m.chat.SetMessages(m.messages)
	if res.switchInterlocutorTo != "" {
		return m.switchInterlocutor(res.switchInterlocutorTo)
	}
	if res.quit {
		return m, tea.Quit
	}
	if res.openEditor != nil {
		return m.openEmbeddedEditor(*res.openEditor)
	}
	if res.openSecretPrompt != nil {
		m.secretPrompt = overlays.NewSecretPrompt(res.openSecretPrompt.Name)
		return m, nil
	}
	if res.openFactEditor {
		return m.startNewFact()
	}
	if res.asyncCmd != nil {
		return m, res.asyncCmd
	}
	return m, nil
}

func (m Model) selectedMessageCopyableText() string {
	if m.chatSel < 0 || m.chatSel >= len(m.messages) {
		return ""
	}
	msg := m.messages[m.chatSel]
	if msg.Kind == MessageToolCall && msg.ToolCallID != "" {
		var resultMsg *Message
		for _, other := range m.messages {
			if other.Kind == MessageToolResult && other.ToolCallID == msg.ToolCallID {
				resultMsg = &other
				break
			}
		}
		if resultMsg != nil {
			var b strings.Builder
			b.WriteString(msg.CopyableText())
			b.WriteString("\n\n")
			b.WriteString(resultMsg.CopyableText())
			return b.String()
		}
	}
	return msg.CopyableText()
}
