// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package facade declares the contract every client (TUI, HTTP server,
// CLI sub-commands) speaks to interact with baifo's core. It is
// deliberately tiny on dependencies: a single ADK type (session.Event,
// kept as opaque escape hatch on Event.Raw) plus stdlib types.
//
// The implementation lives in internal/app. Splitting interface from
// implementation lets the TUI import only this package without
// dragging in the 15-package transitive dependency graph of app.New
// (storage, mcps, providers, workers, etc.).
package facade

import (
	"context"
	"iter"
	"time"

	"google.golang.org/adk/session"
)

// Facade is the contract clients use to drive the baifo core. The
// concrete implementation lives in internal/app and is constructed
// via app.New. Methods are grouped by concern:
//
//   - Agent loop: SendMessage, RootName, RootBuildError, ConfigDir,
//     ModelName, SessionID
//   - Sessions: ListSessions, NewSession, SwitchSession,
//     RenameSession, DeleteSession
//   - Workers: ListWorkers, KillWorker, CollectWorker,
//     SubscribeWorker, SendToWorker
//   - Skills: ListSkills, SkillDetails, SkillContent, SkillScaffold,
//     UpsertSkill, DeleteSkill, InstallSkill
//   - MCPs: ListMCPs, MCPDetails, MCPYAML, MCPScaffold,
//     UpsertMCPFromDisk, DeleteMCPFromDisk, AuthenticateMCP
//   - Providers: ListProviders, ProviderDetails, ProviderYAML,
//     ProviderScaffold, UpsertProvider, DeleteProvider
//   - Secrets: ListSecretNames, SetSecret, DeleteSecret,
//     SecretsEncrypted, EncodeSecrets, DecodeSecrets
//   - Agent templates: ListAgentTemplates, AgentDetails, AgentYAML,
//     AgentScaffold, UpsertAgent, DeleteAgent, SetRootAgent
//   - Facts: ListFacts, FactDetails, AddFact, FactContent,
//     UpdateFact, DeleteFact
//   - Lifecycle: ReloadFromDisk, SubscribeReload, Close
type Facade interface {
	// SendMessage delivers user text to the root agent and yields every
	// Event the agent produces, in order. The iterator stops when the
	// run finishes or when ctx is cancelled.
	SendMessage(ctx context.Context, text string) iter.Seq2[*Event, error]

	// RootBuildError returns the error from the last attempt to build
	// the root agent. Nil when the root is healthy. The TUI uses this
	// to render a precise reason (e.g. "unknown provider 'gemini'")
	// instead of the generic "no agent configured" message.
	RootBuildError() error

	// RootName returns the friendly name of the root agent, as
	// configured under root.name in baifo.yaml. Useful for the TUI
	// header.
	RootName() string

	// ConfigDir returns the absolute path of the active .baifo/ dir.
	ConfigDir() string

	// ModelName returns the provider/model combo the root agent is
	// using, for display in the status bar.
	ModelName() string

	// SessionID returns the ID of the currently active session.
	SessionID() string

	// ListSessions returns the persisted sessions for this user,
	// most-recent first, without their events.
	ListSessions(ctx context.Context) ([]SessionInfo, error)

	// NewSession creates a fresh session and makes it the active one.
	// Returns the new session ID.
	NewSession(ctx context.Context) (string, error)

	// SwitchSession changes the active session to id. The events of
	// the previous session are flushed to disk by their AppendEvent
	// calls, so switching is a cheap pointer swap.
	SwitchSession(ctx context.Context, id string) error

	// RenameSession updates the title of a session.
	RenameSession(ctx context.Context, id, title string) error

	// DeleteSession removes a session and its events. If the deleted
	// session was the active one, a new session is created and the
	// new ID is returned; otherwise the returned ID is unchanged.
	DeleteSession(ctx context.Context, id string) (newActive string, err error)

	// SessionEvents returns the persisted event history of a session
	// as a flat, chronologically-ordered slice. Used by the TUI to
	// repaint the chat with past messages when the user resumes a
	// session (the in-memory chat slice is empty until SendMessage
	// produces something, so without this the resumed view looks
	// blank).
	//
	// Each returned Event carries Role ("user" / "model") so the
	// renderer can tell user bubbles from agent bubbles. Tool calls
	// and their results are returned as separate Events; the TUI
	// pairs them by CallID for the bordered card render.
	//
	// Partial / streaming-fragment events are filtered out by the
	// session store on persistence, so the slice only contains
	// committed turns.
	SessionEvents(ctx context.Context, id string) ([]Event, error)

	// ListWorkers returns a snapshot of every live worker, suitable
	// for the Workers tab and the workers sidebar.
	ListWorkers() []WorkerInfo

	// KillWorker cancels a worker by ID. reason is a free-form
	// explanation surfaced to the next CollectWorker as the error
	// string, so the root agent can tell apart "I (the LLM) killed
	// this" from "the human killed this from the TUI".
	KillWorker(id string, reason string) error

	// CollectWorker waits for a worker to reach idle/done and returns
	// its captured output text.
	CollectWorker(ctx context.Context, id string) (string, error)

	// SubscribeWorker returns the recent event history of a worker
	// plus a live channel that delivers every subsequent event. The
	// returned cancel function unsubscribes from the live stream and
	// must always be called by the consumer (the TUI does this when
	// the user leaves the worker's chat). Events arrive in the same
	// order the worker produced them; history is oldest-first.
	SubscribeWorker(id string) (history []WorkerStreamEvent, stream <-chan WorkerStreamEvent, cancel func(), err error)

	// SendToWorker delivers a user message to an existing worker.
	// Returns immediately; the response arrives as events on the
	// subscription channel. Used by the TUI to talk to a worker
	// directly from its chat view (bypassing the root).
	SendToWorker(ctx context.Context, id string, message string) error

	// SubscribeWorkerLifecycle returns a passive feed of worker
	// birth + terminal-state transitions across the whole system.
	// Unlike SubscribeWorker (which targets one worker for its
	// chat view), this stream lets the TUI's root chat surface
	// "worker X finished" without polling and without the user
	// having to switch into the Workers tab.
	//
	// The cancel function unsubscribes the consumer. The returned
	// channel is closed when cancel is called or when the App
	// shuts down. Buffer size is small (drops are counted upstream
	// in workers.EventBus); consumers should drain quickly.
	SubscribeWorkerLifecycle() (stream <-chan WorkerLifecycleEvent, cancel func())

	// ListSkills returns the slug of every loaded skill. Used by the
	// dynamic-spawn validator and by the /settings overlay.
	ListSkills() []string

	// SkillDetails returns one-line summaries of every loaded skill,
	// suitable for the Settings overlay and /skill list.
	SkillDetails() []SkillDetail

	// SkillContent returns the raw bytes of the named skill's
	// SKILL.md. Used by the editor to seed /skill edit with the
	// on-disk content (comments, whitespace, anchors preserved).
	SkillContent(name string) (string, error)

	// SkillScaffold returns the SKILL.md skeleton baifo drops into
	// the editor for /skill add. suggestedName pre-fills the name.
	SkillScaffold(suggestedName string) string

	// UpsertSkill validates content as a SKILL.md document and
	// writes it to .baifo/skills/<name>/SKILL.md, creating the
	// directory if needed. Skill name comes from the parsed
	// frontmatter; passing a mismatching dirName returns an error.
	UpsertSkill(ctx context.Context, content string) error

	// DeleteSkill removes .baifo/skills/<name>/ recursively.
	DeleteSkill(ctx context.Context, name string) error

	// InstallSkill downloads sourceURL (a .zip or .tar.gz), validates
	// the contained SKILL.md against the ADK schema, and installs it
	// under .baifo/skills/<name>/. Returns the installed name on
	// success; the disk is left untouched on any error.
	InstallSkill(ctx context.Context, sourceURL string) (string, error)

	// ListMCPs returns the declared MCP names from baifo.yaml.
	ListMCPs() []string

	// MCPDetails returns a one-line, user-facing description of every
	// declared MCP. Suitable for `/mcp list` output.
	MCPDetails() []MCPDetail

	// MCPYAML returns the YAML text of the named MCP entry, rendered
	// with comment-preserving fields and stable ordering. Used to
	// seed the editor for /mcp edit. Returns ErrUnknownMCP-style
	// error semantics if the name is not found.
	MCPYAML(name string) (string, error)

	// MCPScaffold returns the YAML skeleton baifo drops into the
	// editor for /mcp add. suggestedName pre-fills the name placeholder.
	MCPScaffold(suggestedName string) string

	// UpsertMCPFromDisk parses yamlText as a single MCP entry,
	// validates it against the schema, and writes it through
	// yamledit so comments in baifo.yaml are preserved. Triggers a
	// reload after the write so the in-memory registry matches.
	UpsertMCPFromDisk(ctx context.Context, yamlText string) error

	// DeleteMCPFromDisk removes the named MCP entry from baifo.yaml and
	// triggers a reload. Comments and unrelated keys in the file are
	// preserved. Returns an error if the name is unknown.
	DeleteMCPFromDisk(ctx context.Context, name string) error

	// AuthenticateMCP runs the OAuth flow for the named MCP (HTTP
	// transport, auth.kind=oauth). For service-to-service flows the
	// call is synchronous and silent; for interactive flows it opens
	// the user's browser and waits for the redirect. The resulting
	// token is persisted to SQLite and reused on subsequent boots.
	//
	// When force is true, any cached token + Dynamic Client
	// Registration credentials for the MCP are deleted first so
	// the full flow re-runs from scratch. Use this when an
	// authorization is suspected to be stale (consent revoked
	// upstream, AS rotated keys, etc.). When false, a still-valid
	// cached token short-circuits the call.
	AuthenticateMCP(ctx context.Context, name string, force bool) error

	// TestMCPConnection opens a fresh session against the named
	// MCP and returns a one-line human-readable status string
	// ("connected · 12 tools · 230ms" or "failed: …"). The TUI
	// surfaces it in the chat after the user invokes the
	// "Test connection" action from the Settings overlay or the
	// /mcps test slash command.
	//
	// Returns an error only when the call itself couldn't run
	// (unknown MCP, registry not initialised). A successful
	// connection AND a failed connection both come back via the
	// string return; callers don't need to branch on errors to
	// surface the outcome.
	TestMCPConnection(ctx context.Context, name string) (string, error)

	// ClearMCPAuth drops every cached credential baifo holds for
	// the named MCP: access/refresh token AND the persisted
	// DCR client registration. The next call to AuthenticateMCP
	// (or the agent's first real use of the MCP) starts fresh:
	// register a new client if needed, open the browser, mint a
	// new token. Idempotent: calling it on an MCP without
	// cached creds is a no-op.
	//
	// Use this when an authorisation is stuck (consent revoked
	// upstream, AS rotated keys, refresh token poisoned) and
	// you want a clean slate without editing baifo.yaml.
	ClearMCPAuth(ctx context.Context, name string) error

	// ListProviders returns the declared provider names from baifo.yaml.
	ListProviders() []string

	// ProviderDetails returns one-line summaries of every provider.
	ProviderDetails() []ProviderDetail

	// ProviderYAML returns the YAML text of the named provider entry,
	// rendered comment-preserving from disk. Used by /provider edit.
	ProviderYAML(name string) (string, error)

	// ProviderScaffold returns the YAML skeleton baifo drops into the
	// editor for /provider add.
	ProviderScaffold(suggestedName string) string

	// UpsertProvider parses yamlText as one ProviderEntry, validates
	// it against the providers registry, and writes through yamledit.
	UpsertProvider(ctx context.Context, yamlText string) error

	// DeleteProvider removes the named provider entry from baifo.yaml
	// and triggers a reload.
	DeleteProvider(ctx context.Context, name string) error

	// ListSecretNames returns the names (not values) of every secret
	// in secrets.yaml. Used to validate allowed_secrets in spawn specs.
	ListSecretNames() []string

	// SetSecret writes (name, value) to the encrypted secrets store.
	// description is the human-readable note shown in listings; the
	// value itself is NEVER surfaced after this call.
	SetSecret(ctx context.Context, name, value, description string) error

	// DeleteSecret removes the named entry from the encrypted store.
	DeleteSecret(ctx context.Context, name string) error

	// SecretsEncrypted reports whether the secrets store is currently
	// running in encrypted mode (true) or plaintext mode (false). Used
	// to render a badge in Settings and to gate /secret encode|decode.
	SecretsEncrypted() bool

	// EncodeSecrets re-seals every plaintext entry into the encrypted
	// format. Requires encryption_key to be configured. Returns the
	// number of entries converted.
	EncodeSecrets(ctx context.Context) (int, error)

	// DecodeSecrets unwraps every encrypted entry into plaintext and
	// flips the on-disk flag. Requires encryption_key to be configured.
	// Destructive of confidentiality, the caller MUST confirm first.
	DecodeSecrets(ctx context.Context) (int, error)

	// ListAgentTemplates returns the names of every static agent
	// template declared in agents.yaml.
	ListAgentTemplates() []string

	// AgentDetails returns one-line summaries of every agent template
	// for /agent list and the Settings overlay.
	AgentDetails() []AgentDetail

	// AgentYAML returns the YAML text of the named agent template
	// rendered comment-preserving from disk. Used by /agent edit.
	AgentYAML(name string) (string, error)

	// AgentScaffold returns the YAML skeleton baifo drops into the
	// editor for /agent add.
	AgentScaffold(suggestedName string) string

	// UpsertAgent parses yamlText as one AgentTemplate, validates it
	// against the schema and writes through yamledit so agents.yaml
	// comments survive. Triggers a reload on success.
	UpsertAgent(ctx context.Context, yamlText string) error

	// DeleteAgent removes the named entry from agents.yaml and
	// triggers a reload.
	DeleteAgent(ctx context.Context, name string) error

	// SetRootAgent promotes the named agent to root: it sets
	// root: true on that entry, strips the flag from the previous
	// root, writes through yamledit (comments survive) and reloads
	// so the live root agent is rebuilt. Errors if the name is
	// unknown or already the root.
	SetRootAgent(ctx context.Context, name string) error

	// ListFacts returns the persisted long-term memory entries for
	// the current user, newest first. The overlay shows them as a
	// flat list for v1.
	ListFacts() []string

	// FactDetails returns the full Facts entries (id + content +
	// category + author + timestamp) so the Settings overlay can
	// surface metadata, not just the truncated content.
	FactDetails() []FactDetail

	// AddFact inserts a new long-term memory entry for the current
	// user. Used by /fact add and the Settings overlay 'add new'
	// action. Returns the new entry ID on success.
	AddFact(ctx context.Context, content, category string) (uint64, error)

	// FactContent returns the current text and category of the named
	// entry, used to pre-fill the editor for /fact edit. Errors when
	// the ID is unknown.
	FactContent(entryID uint64) (content, category string, err error)

	// UpdateFact replaces the content of an existing fact. The
	// timestamp is bumped to now so the corrected entry surfaces at
	// the top of memory searches. Used by /fact edit and the
	// Settings overlay 'edit content' action.
	UpdateFact(ctx context.Context, entryID uint64, content string) error

	// DeleteFact removes the entry with the given ID. Idempotent
	// from the user's mental model: a delete-then-delete should
	// surface a clear "not found" error without panicking.
	DeleteFact(ctx context.Context, entryID uint64) error

	// ReloadFromDisk re-reads baifo.yaml and agents.yaml from the
	// active config directory and rebuilds the in-memory registries
	// (providers, mcps, agent templates) and the root agent. Called
	// automatically by the file watcher; exposed for tests and for
	// the future /config reload command. A reload that fails leaves
	// the previous state in place and returns the error.
	ReloadFromDisk(ctx context.Context) error

	// SubscribeReload returns a channel that fires after every
	// successful ReloadFromDisk. Receivers should drain quickly; the
	// channel is buffered (size 1) and emissions that find it full
	// are dropped, since every event is equivalent ("something
	// changed, refresh your view").
	SubscribeReload() <-chan ReloadEvent

	// ContextGuardStatus returns a snapshot of the root agent's
	// context-guard runtime for the active session. Enabled is false
	// when the root has no context_guard block, when the guard is
	// turned off, or when there is no active session. The call performs
	// a single cheap session-state read and is safe to invoke once per
	// turn to refresh the footer gauge.
	ContextGuardStatus(ctx context.Context) ContextGuardStatus

	// Close releases every resource owned by the facade. After Close,
	// further calls have undefined behaviour.
	Close() error
}

// ContextGuardStatus is a snapshot of the root agent's context guard,
// used to drive the footer gauge chip and the "context compacted"
// chat notice. All numeric fields are zero when Enabled is false.
type ContextGuardStatus struct {
	// Enabled reports whether the root agent has an active
	// context_guard block for the current session.
	Enabled bool

	// Strategy is the active compaction strategy: "threshold"
	// (token-based) or "sliding_window" (turn-based).
	Strategy string

	// Used is the current gauge numerator: real prompt tokens for the
	// threshold strategy, or turns elapsed since the last compaction
	// for the sliding-window strategy.
	Used int

	// Limit is the gauge denominator: the token count at which a
	// threshold compaction fires, or max_turns for sliding_window.
	Limit int

	// Percent is Used/Limit clamped to 0..100, representing how close the
	// conversation is to triggering the next compaction.
	Percent int

	// Fingerprint changes whenever a new compaction is recorded in
	// session state. The TUI diffs it across turns to decide when to
	// surface the compaction notice. Empty when no compaction has yet
	// happened in the active session.
	Fingerprint string

	// Summary is the running conversation summary the contextguard
	// plugin produced at the last compaction. Empty until the first
	// compaction happens. The TUI shows it as the expandable body of
	// the context-guard notice so the user can read exactly what the
	// model condensed the history into.
	Summary string
}

// DTOs

// ReloadEvent is the message broadcast on SubscribeReload after the
// implementation rebuilt its state from disk. It carries no payload
// today; clients use it as a cue to refresh any overlay that
// displays config-derived data (Settings, model name, ...).
type ReloadEvent struct {
	// At is the wall-clock time the reload completed.
	At time.Time
}

// WorkerInfo is the public, time-formatted view of one live worker.
type WorkerInfo struct {
	ID        string
	Name      string
	Kind      string // "static" | "dynamic"
	Status    string // "running" | "idle" | "done" | "failed" | "killed"
	Elapsed   string
	LastEvent string
}

// SessionInfo is the public view of one persisted session, suitable
// for the /sessions tab listing.
type SessionInfo struct {
	ID        string
	Title     string
	CreatedAt string
	LastAt    string
	MsgCount  int
}

// MCPDetail is the public, human-readable view of one MCP declaration.
// Built by the implementation for /mcp list and the Settings overlay.
type MCPDetail struct {
	Name     string // unique key under mcps[]
	Type     string // builtin | http | stdio
	Endpoint string // for http / builtin slug / stdio command, depending on type
	AuthKind string // none | oauth
	HasAuth  bool   // true when AuthKind != "none"
}

// SkillDetail is the public view of one installed skill. Built by
// the implementation so the TUI can render Settings rows without
// loading every SKILL.md from disk every render.
type SkillDetail struct {
	Name        string // slug, also the frontmatter name
	Description string // first paragraph of the frontmatter description
}

// AgentDetail is the public view of one static agent template,
// suitable for /agent list and the Settings overlay.
type AgentDetail struct {
	Name        string
	Description string
	Provider    string
	Model       string
}

// ProviderDetail is the public view of one LLM provider entry,
// suitable for /provider list and the Settings overlay. The API key
// is NEVER included, only its presence (HasKey).
type ProviderDetail struct {
	Name   string
	Type   string
	URL    string
	HasKey bool
}

// FactDetail is the public view of one stored long-term memory entry.
// Built by the implementation for /fact list and the Settings overlay.
type FactDetail struct {
	ID        uint64
	Content   string
	Category  string
	Author    string
	Timestamp time.Time
}

// ToolCallInfo describes one tool invocation emitted by the agent
// inside a stream event. The TUI renders it as a dedicated chat row.
type ToolCallInfo struct {
	// CallID is the ADK-assigned identifier that pairs a call with its
	// matching ToolResultInfo. Empty when the underlying part did not
	// carry an ID.
	CallID string

	// Name is the tool name as the agent invoked it (e.g.
	// "filesystem.read_file"). Never empty for a well-formed call.
	Name string

	// Args is the argument map the agent passed to the tool, after
	// secret expansion. May be nil for argument-less tools.
	Args map[string]any
}

// ToolResultInfo describes the response of one tool invocation. The
// CallID matches the one in the preceding ToolCallInfo so the TUI can
// pair them visually if it wants to.
type ToolResultInfo struct {
	CallID string
	Name   string
	Result map[string]any
}

// Event is baifo's transport-agnostic envelope around an ADK event.
// Today this is a thin wrapper; future phases will add per-worker
// routing, redacted/raw flags, and structural fields the TUI uses to
// render tool cards, streaming chunks, and so on.
type Event struct {
	// Role identifies who produced the event. "user" for events
	// authored by the human, "model" for the agent's replies, ""
	// for events with no clear authorship (system notices). Set
	// when the event comes from historical session storage; left
	// empty for streaming events from SendMessage (which the TUI
	// already knows are from the agent).
	Role string

	// Text holds the assistant text for this slice of the stream. May
	// be empty when the underlying event carries only tool-call
	// metadata.
	Text string

	// Replace reports how Text relates to the text already shown for
	// the current agent turn. false (the default) means "append this
	// chunk to the running reply" (the normal streaming case). true
	// means "Text is the complete reply; replace what was streamed so
	// far". The A2A executor streams incremental artifact chunks
	// (Append=true) and then emits one final artifact carrying the
	// FULL text with Append=false; without honouring that flag the TUI
	// concatenates the final complete copy onto the partials it
	// summarises, duplicating the reply. Mapped from
	// TaskArtifactUpdateEvent.Append (Replace = !Append).
	Replace bool

	// ToolCalls is non-empty when the event carries one or more
	// FunctionCall parts. The TUI renders them as separate chat rows.
	ToolCalls []ToolCallInfo

	// ToolResults is non-empty when the event carries one or more
	// FunctionResponse parts.
	ToolResults []ToolResultInfo

	// Raw is the underlying ADK session.Event. TUI code that needs
	// fields outside what Event surfaces can reach in, but doing so
	// couples to ADK and should be avoided for new features.
	Raw *session.Event
}

// WorkerStreamEvent is the TUI-facing flavour of a worker's event
// bus output. It carries the same information but with the ADK
// session.Event already unpacked into typed slices, so the chat
// view does not need to reach into the workers package's internals.
//
// One field of {Text, ToolCalls, ToolResults, StatusChange} is set
// per event; the rest are zero. Subscribers switch on Kind.
type WorkerStreamEvent struct {
	// WorkerID identifies the producing worker. Useful when a
	// consumer multiplexes streams.
	WorkerID string

	// Index is a per-worker monotonic counter assigned by the event
	// bus. Used to de-duplicate the (history, live-stream) handoff.
	Index int

	// Timestamp is the moment the worker emitted the event.
	Timestamp time.Time

	// Kind classifies the event in a TUI-friendly way: text reply,
	// tool call, tool result, status change.
	Kind WorkerStreamEventKind

	// Text is populated for assistant-message and thought events.
	Text string

	// ToolCalls / ToolResults are populated for tool events. Both
	// slices use the same ToolCallInfo / ToolResultInfo types we
	// already publish on Event for the root.
	ToolCalls   []ToolCallInfo
	ToolResults []ToolResultInfo

	// StatusChange is set on EventStatusChange events. It is the
	// new worker status as a lowercase string ("running", "idle",
	// "done", ...).
	StatusChange string
}

// WorkerStreamEventKind classifies a WorkerStreamEvent at the chat
// renderer level. Mirrors workers.EventKind but is decoupled so the
// TUI does not import the workers package directly.
type WorkerStreamEventKind int

const (
	// WorkerStreamText is an assistant message or a thought chunk.
	WorkerStreamText WorkerStreamEventKind = iota

	// WorkerStreamToolCall corresponds to one or more tool calls
	// the worker just invoked.
	WorkerStreamToolCall

	// WorkerStreamToolResult is the response of a previously issued
	// tool call (already redacted by the secrets pipeline).
	WorkerStreamToolResult

	// WorkerStreamStatus marks a Status transition. The TUI uses
	// it to update the header of the chat view ("running" → "idle")
	// without polling.
	WorkerStreamStatus
)

// WorkerLifecycleEventKind classifies a WorkerLifecycleEvent. The
// set is intentionally narrow: only "this worker has reached a
// state I as the root might want to know about." Fine-grained per-
// tool events stay on the per-worker stream you get from
// SubscribeWorker.
type WorkerLifecycleEventKind int

const (
	// WorkerLifecycleSpawned fires once when a worker enters the
	// registry. Useful to surface "a worker was spawned" in the
	// root chat passively (without the root having to call any
	// list/inspect tool).
	WorkerLifecycleSpawned WorkerLifecycleEventKind = iota

	// WorkerLifecycleDone fires when a worker terminates normally.
	WorkerLifecycleDone

	// WorkerLifecycleFailed fires when a worker terminates with an
	// error. LastEvent on the event carries the error message.
	WorkerLifecycleFailed

	// WorkerLifecycleKilled fires when a worker is killed (by the
	// agent via kill_agent, by the user, or by baifo shutdown).
	WorkerLifecycleKilled
)

// WorkerLifecycleEvent is the broadcast a Facade consumer receives
// on SubscribeWorkerLifecycle for every worker birth or terminal
// transition in the system. It complements SubscribeWorker (per-
// worker chat events) by giving the consumer a passive, system-
// wide feed: "anything new happen with any of my workers?"
//
// The intended consumer is the TUI's root chat: when a spawned
// worker finishes in the background while the user is typing,
// the root chat shows a small system row "worker worker_xxx
// (name) is done - call collect_agent for the output" so the
// user notices without having to switch to the Workers tab.
type WorkerLifecycleEvent struct {
	Kind WorkerLifecycleEventKind

	// WorkerID is the unique id of the worker that transitioned.
	WorkerID string

	// Name is the worker's display name (the LLM-supplied name
	// for dynamic workers, the template name for static).
	Name string

	// Kind of the worker: "static" or "dynamic". Lets the
	// consumer pick the right entity colour when rendering.
	WorkerKind string

	// Status, as a human-readable string ("done", "failed", ...).
	// Mirrors WorkerInfo.Status's String() output so consumers
	// don't need to depend on internal/workers.
	Status string

	// LastEvent is a short context string for the transition.
	// For WorkerLifecycleFailed it carries the error message.
	// For WorkerLifecycleKilled it carries the kill reason. For
	// WorkerLifecycleSpawned and WorkerLifecycleDone it's
	// usually empty.
	LastEvent string

	// Timestamp records when the transition was observed.
	Timestamp time.Time
}
