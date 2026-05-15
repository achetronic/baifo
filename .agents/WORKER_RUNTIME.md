# WORKER_RUNTIME.md
How sub-agents are spawned, supervised, queried, killed and collected.
Reference for `internal/workers/` and the spawn tools in
`internal/tools/spawn/`.

## Glossary

- **Root**, the single agent the user talks to in the TUI. Always alive
  while baifo is running.
- **Worker**, any agent spawned by the root. Has its own runner,
  session (in-memory only), skills, MCPs and a workspace directory.
- **Static worker**, a worker built from a template in `agents.yaml`.
  Same shape every time.
- **Dynamic worker**, a worker built from a spec the root produces at
  runtime, using pieces baifo already knows.
- **Spec**, JSON object passed to a spawn tool: prompt, description,
  llm override, skills, mcps, allowed_secrets, initial_message.
- **Manager**, `internal/workers.Manager`, owner of every live worker.
- **Sandbox**, the filesystem workspace directory allocated per worker under
  `<data_dir>/workspaces/<worker_id>/`. This is a hygiene boundary, not a
  security boundary: a process running inside the worker can escape with
  `exec(2)`. Real isolation is the operator's responsibility (containers,
  namespaces, VMs).

## Lifecycle

```
spec ──► Manager.Spawn ──► new Worker (idle) ──┐
                                                  │
   query_agent ◄──────────── live events ──◄──────┤
                                                  │
                             collect / kill ──────┘
                                                  │
                                  ▼
                          (removed from registry)
```

States (`internal/workers/worker.go::Status`):

- `idle`, waiting for the next `query_agent`. This is the initial state at
  construction; if `InitialMessage` is non-empty, `Spawn` immediately calls
  `send()`, which transitions the worker to `running` before returning.
- `running`, actively processing. Has an event stream.
- `done`, terminated normally; output is available via `collect_agent`.
- `failed`, terminated with an error.
- `killed`, terminated by `kill_agent` or baifo shutdown.

Transition rules:

- `Spawn` → `idle`, then → `running` immediately if `spec.InitialMessage`
  is non-empty. The worker is already running by the time `Spawn` returns
  its ID.
- `idle` → `running` when `query_agent` sends a new input.
- `running` → `idle` when `driver.WaitIdle` returns (the ADK runner finished
  the current turn).
- any → `done` when `collect_agent` completes and the worker was not already
  terminal.
- any → `failed` on unrecoverable driver error.
- any → `killed` on `kill_agent` or shutdown.

A worker stays in the registry until `collect_agent` is called or
shutdown happens. `done` / `failed` / `killed` workers keep their
sandbox until `collect_agent` or `Shutdown` removes them from the
registry (both call `unregister`, which calls `SandboxAllocator.Cleanup`).
A killed-but-not-yet-collected worker retains its sandbox.

## Ending a worker

A worker reaches `done` in one of these ways:

1. The root calls `collect_agent(worker_id)`. The manager waits for the
   worker to reach `idle` / `done`, captures the last assistant message
   as the output, and removes the worker.
2. `kill_agent` cancels the context regardless of state.

There is no automatic timeout, the worker does not time out by itself.
The root is responsible for not leaking workers.

## Spawn tools (registered on the root)

Exact JSON schemas live in `internal/tools/spawn/`. Summary below.

### `spawn_static_agent`

```json
{
  "name": "deep-researcher",
  "initial_message": "Find every blog post about A2A protocol",
  "allowed_secrets": ["GITHUB_TOKEN"]
}
```

- `name` (required), must match an entry in `agents.yaml`. Must also not
  collide with a currently live worker name; `Spawn` returns
  `ErrWorkerNameConflict` if it does.
- `initial_message`, first user message sent to the worker after
  construction.
- `allowed_secrets` (optional), overrides the template's
  `allowed_secrets`. When omitted, the template's value is used.

Returns `{ "worker_id": "w_..." }` immediately. The worker starts
processing in a goroutine.

### `spawn_dynamic_agent`

The tool **description is composed at boot** from the live universe
(see decision #23): every available skill with its frontmatter
description, every MCP with the tool names it exposes, every provider,
every secret with its description. The LLM reads the menu and picks.

Args:

```json
{
  "name": "htmx-reddit-scout",
  "description": "Web scout for htmx 2.0 discussions",
  "prompt": "You are a research scout...",
  "llm": { "provider": "openai-main", "model": "gpt-5", "reasoning": "low" },
  "skills": ["web-research"],
  "mcps": ["browse"],
  "allowed_secrets": [],
  "initial_message": "Search reddit for htmx 2.0 discussions"
}
```

Field notes:

- `name` (required), free text; metadata only. Collides with live worker
  names: `Spawn` returns `ErrWorkerNameConflict` if a worker with that name
  is already live. The manager assigns its own `worker_id`.
- `description` (optional), forwarded into the worker's
  `agent.Spec.Description` and used to label the spawned agent in
  listings.
- `prompt` (required), full system prompt for the worker.
- `llm.provider` (optional), must match a `providers[].name` in
  `baifo.yaml`. Defaults to the root's provider+model when omitted.
- `llm.model` (optional), provider-specific model id.
- `llm.reasoning` (optional), one of `off` / `minimal` / `low` / `medium` / `high`
  (empty = model default). `off` normalises to empty (no reasoning config sent).
  Only valid for models that support it.
- `skills`, must be a subset of skills loaded in baifo.
- `mcps`, must be a subset of MCPs declared in `baifo.yaml`.
- `allowed_secrets`, validated against the **global** secret universe
  and against the parent's allowlist. The parent is sovereign (no
  restriction) when `ParentAllowedSecrets` is nil, which is always the
  case for the root in production.

The `DynamicSpawnArgs` struct carries `name`, `description`,
`prompt`, `llm` (`provider`, `model`, `reasoning`), `skills`, `mcps`,
`allowed_secrets` and `initial_message`. There is no `sandbox` or
`output_schema` field; `workers.Spec` carries neither.

Returns `{ "worker_id": "w_..." }` immediately.

### `spawn_dynamic_agents`

```json
{ "agents": [ <DynamicSpawnArgs>, <DynamicSpawnArgs>, ... ] }
```

Convenience batch for spawning multiple dynamic workers in one LLM
turn. Exists because composing a full dynamic spec (prompt,
description, MCPs, skills, secrets) is
expensive in tokens; emitting four of them in four separate tool
calls would mean four LLM roundtrips and four copies of the system
prompt. With the batch, the model assembles the four specs once and
pays the roundtrip once.

Semantics:

- **Validation and launch**: specs are processed in order. Each spec is
  validated and then immediately passed to `Manager.Spawn`. If spec N
  fails validation or spawning, the tool returns an error naming the
  offending index (`agents[N]: ...`) and stops. Specs 0..N-1 that already
  spawned are NOT rolled back, the caller must kill them explicitly if
  it treats the batch as atomic.
- **Execution**: every spawned worker runs in its own goroutine
  concurrently. From the moment the batch tool returns to the LLM,
  all workers are already running in parallel, the "batch" only
  collapses the LLM-side roundtrip, not the runtime concurrency.
  Workers spawned via N independent `spawn_dynamic_agent` calls run
  with exactly the same parallelism; the batch is purely a turn /
  token optimisation for the model.

Returns `{ "worker_ids": [...] }`.

There is no equivalent batch for static spawn. A static spec is
just `{name, initial_message, allowed_secrets?}`, three small
fields, so emitting N of them in N tool calls is cheap enough that
adding another tool to the catalogue isn't worth the noise.

## Supervision tools

### `list_agents`

No args. Returns the **catalogue of static templates** declared in
`agents.yaml`:

```json
{
  "templates": [
    { "name": "deep-researcher", "description": "...",
      "provider": "openai-main", "model": "gpt-5" }
  ]
}
```

These are names accepted by `spawn_static_agent`.

### `list_running_agents`

No args. Returns the **live workers** currently in the registry:

```json
{
  "workers": [
    { "id": "w_a3f9", "name": "htmx-reddit-scout", "kind": "dynamic",
      "status": "running", "elapsed": "42s", "last_event": "browse.fetch" }
  ]
}
```

### `query_agent`

```json
{ "worker_id": "w_a3f9", "message": "Focus on critical posts only" }
```

Sends a new user message to the worker's session. Returns immediately
with `{ "ok": true }`. The worker transitions to `running`. To read the
response, the root calls `collect_agent` once status is `idle` again,
or `inspect_agent` for a non-consuming peek.

### `inspect_agent`

```json
{ "worker_id": "w_a3f9", "since_event": 47 }
```

Returns a snapshot of the worker's public state (`id`, `name`, `kind`,
`status`, `elapsed`, `last_event`). `since_event` is accepted but
currently ignored (`_ = since` in `Manager.Inspect`); the `Inspect`
cursor (returning only events newer than `since`) is a TODO tracked in
`manager.go`. The ring buffer itself is already wired and populated
(see `goBufferHistory` below); `SubscribeWorker` uses it correctly.

### `collect_agent`

```json
{ "worker_id": "w_a3f9", "timeout_ms": 30000 }
```

Waits (up to `ManagerConfig.CollectTimeout`, default `5m`; overridden
per-call via `timeout_ms`) for the worker to reach `idle` / `done`, then:

- Returns `{ "output": "...", "status": "done" }` (or `"status": "failed"` /
  `"status": "killed"` for workers that were already terminal). The `error`
  field carries `WorkerInfo.Err` (the failure or kill reason) when set.
- Removes the worker from the registry.
- Cleans the worker's sandbox directory (for all workers, not just
  dynamic ones).

### `kill_agent`

```json
{ "worker_id": "w_a3f9", "reason": "superseded by newer plan" }
```

Cancels the worker's context. Status transitions to `killed`. The
worker stays in the registry until `collect_agent` or `Shutdown`
removes it; the sandbox is not cleaned until then.

`reason` is a free-text note stored in `WorkerInfo.Err` and surfaced on the
subsequent `collect_agent` call as `CollectResult.Err`. Empty `reason`
defaults to `"killed by agent"`.

## Sandboxing

Every worker gets a single dedicated workspace directory at
`<data_dir>/workspaces/{worker_id}/`, allocated by
`workers.SandboxAllocator.Allocate` on spawn and removed by
`SandboxAllocator.Cleanup` inside `unregister` (called by
`finishCollect` and `Shutdown`). `Kill` alone does not clean the
sandbox; the directory persists until `collect_agent` or shutdown.
There are no per-worker sandbox modes: every worker is allocated the
same way, and the spec has no `sandbox` field.

The workspace path is passed to the worker's driver at construction.
This is a hygiene boundary, not a security boundary: a process running
inside the worker can escape with `exec(2)`. Real isolation is the
operator's responsibility (containers, namespaces, VMs).

The browse builtin does not write to disk and is not sandboxed at the
FS level.

## Event bus

The manager publishes events on a single `chan WorkerEvent` per worker,
plus a global multiplex (`Manager.GlobalBus()`). The routing is
implemented in `internal/workers/manager.go`:

- `goReplay`, goroutine wired at spawn time that forwards every
  non-`StatusChange` event from the per-worker bus to `globalBus`.
- `publishStatusChange`, called directly on spawn, query, idle
  transition, collect, kill and shutdown so status transitions always
  reach the global bus regardless of `goReplay`'s event filter.

Consumers today:

- **TUI**, subscribes per-worker via `App.SubscribeWorker(id)` (which
  calls `Manager.SubscribeWorker`). Returns a history snapshot plus a
  live channel of `facade.WorkerStreamEvent`, translated in
  `internal/app/workers_stream.go`.
- **TUI chips and Workers overlay**, receive `StatusChange` events
  via `App.SubscribeWorkerLifecycle()`, which filters the global bus
  down to spawn + terminal transitions (`done`/`failed`/`killed`)
  and translates them to `facade.WorkerLifecycleEvent`. Implemented in
  `internal/app/workers_stream.go`.

Event shape:

```go
type WorkerEvent struct {
    WorkerID  string
    Index     int       // monotonic per bus (assigned by EventBus.Publish)
    Timestamp time.Time
    Kind      EventKind // ToolCall, ToolResult, Thought,
                       // AssistantMessage, StatusChange
    Payload   any
}
```

Payload types by kind:

- `EventToolCall` → `ToolCallPayload{Name, ID, Args}`
- `EventToolResult` → `ToolResultPayload{Name, ID, Result}` (or a bare
  `string` for errors, which land here as best-effort taxonomy)
- `EventAssistantMessage` / `EventThought` → `string`
- `EventStatusChange` → `WorkerInfo` snapshot

The bus is in-process, non-persistent. `internal/app/workers_stream.go`
translates `WorkerEvent` into `facade.WorkerStreamEvent` before events
reach the TUI.

## Concurrency model

- One goroutine per worker owns the ADK runner (`Driver.Send` + `WaitIdle`).
- Two additional goroutines are attached to each worker at spawn time:
  `goReplay` (forwards non-StatusChange events to the global bus) and
  `goBufferHistory` (feeds every event into the per-worker ring buffer).
  Both exit when the worker's event bus is closed (`unregister` closes it).
- Per-subscriber channels are buffered at 256 entries (`busBufferSize`).
  Events are dropped (counted via `EventBus.Drops()`) for any subscriber
  whose buffer is full; the publisher never blocks.
- The manager uses a `sync.RWMutex` around the registry map; reads are
  frequent (every TUI render), writes rare (spawn/collect/kill).
- The root's spawn tool calls return quickly with a `worker_id` rather
  than blocking on completion.

## Persistence

- **Dynamic workers**: session state is in-memory only and disappears
  on baifo shutdown. The sandbox dir is removed on collect/shutdown.
- **Static workers**: definitions live in `agents.yaml` and persist;
  their session state is also in-memory only, re-spawning the same
  template gives a fresh conversation.
- **Audit log**: every tool call from any worker is appended to the
  the SQLite `audit` table with redacted args/results.

## Promoting a dynamic to static

To keep a dynamic worker around as a reusable template, the user adds
it to `agents.yaml`, via `/agent add` in the TUI or by editing the
file directly. There is no tool that does this automatically (config
mutation is not exposed as agent tools; see decision #8).

## ADK driver

Every worker is backed by an in-process `Driver` implementation
(`internal/workers/adk_driver.go`). The `Driver` interface is:

```go
type Driver interface {
    Send(ctx context.Context, message string, bus *EventBus, workerID string) error
    WaitIdle(ctx context.Context) error
    Output() string
    Close() error
}
```

The production implementation (`a2aWorkerDriver`, in `internal/app/worker_driver.go`) wraps an ADK `adka2a.Executor`
and a per-worker `session.InMemoryService` exposed via `a2asrv.Handler`. Workers are NOT Python
subprocesses and NOT HTTP agents: the A2A executor runs in-process
in a goroutine. `newA2AWorkerDriverFactory` builds the factory the Manager
calls once per `Spawn`.

**Why A2A for Workers?** Initially, workers used a raw ADK `runner.Runner`. However, ADK's raw `session.Event`
stream is cumulative, repeating past text and tool calls on every tick.
This led to complex, buggy client-side parsing attempting to extract deltas.
By switching workers to use `adka2a.NewExecutor` internally, workers now
benefit from the exact same intelligent, deduplicated event stream as the root
agent, completely eliminating UI glitches like duplicated tool calls or fragmented text bubbles.

- `buildWorkerAgent` wires the agent through the shared `agent.Builder` (providers, secrets, audit, MCPs).
- `streamingFor(provider string) bool`, resolves whether the provider
  uses SSE streaming or non-streaming mode. `nil` defaults to streaming.

`Send` starts a goroutine that calls `runner.Run`, publishes events to
the bus (tool calls/results via `publishNonTextParts`, text chunks as
`EventAssistantMessage`), then signals idle via a buffered `chan struct{}`
(size 1). In SSE streaming mode, partial (non-final) text chunks are
published to the bus immediately as they arrive so the TUI can stream
them in real time. The string variable `prevText` accumulates what has
already been published; on the final response event only the delta
(`strings.TrimPrefix(finalText, prevText)`) is published, preventing
duplication. `WaitIdle` blocks on that channel.

## History ring buffer

`goBufferHistory` attaches a goroutine to every worker's event bus at
spawn time. It feeds every event into a per-worker `eventHistory` ring
buffer (capacity 200, defined by `historyCapacity`). New events overwrite
the oldest when full.

`Manager.SubscribeWorker(id)` returns the buffered history snapshot plus
a live channel so late subscribers (e.g. the TUI opening a worker view
after the worker has been running for a while) can replay context.
Subscribers de-duplicate on `(WorkerID, Index)` to handle the small
race window where an event appears in both the snapshot and the live
channel.

`Manager.Inspect` currently ignores `since` (`_ = since` in the implementation);
the cursor-based history replay for `Inspect` is a TODO in `manager.go`.

## Manager API

Key method signatures in `internal/workers/manager.go`:

```go
func NewManager(cfg ManagerConfig) *Manager
func (m *Manager) GlobalBus() *EventBus
func (m *Manager) Spawn(ctx context.Context, spec Spec) (*Worker, error)
func (m *Manager) Query(ctx context.Context, id string, message string) error
func (m *Manager) List() []WorkerInfo
func (m *Manager) Get(id string) (WorkerInfo, error)
func (m *Manager) Inspect(id string, since int) (WorkerInfo, error)
func (m *Manager) SubscribeWorker(id string) (history []WorkerEvent, stream <-chan WorkerEvent, cancel func(), err error)
func (m *Manager) Collect(ctx context.Context, id string, timeout time.Duration) (WorkerInfo, error)
func (m *Manager) Kill(id string, reason string) error
func (m *Manager) Shutdown(grace time.Duration) error
```

`ManagerConfig` fields: `Sandbox` (`*SandboxAllocator`, optional; nil disables
workspace allocation), `DriverFactory` (required), `CollectTimeout` (optional;
defaults to 5 minutes).

`Spec` fields: `Kind`, `Name`, `Description`, `Prompt`, `Provider`,
`Model`, `Reasoning`, `Skills`, `MCPs`, `AllowedSecrets`, `InitialMessage`.

## Failure modes

- **Spec validation fails**, `spawn_*` returns an error immediately;
  the root sees it and can retry.
- **Runner crashes**, manager marks the worker `failed`, captures the
  error, keeps the worker in the registry for the root to collect.
- **Skill / MCP becomes unavailable mid-run**, the worker's tool call
  fails; the worker decides how to handle it (usually retries or asks
  the root).
- **baifo shutdown**, `Manager.Shutdown(grace)` cancels every worker
  context, waits up to `grace` for them to drain, then marks
  remaining workers `killed`. Persistent state (sessions, facts) is
  flushed; sandboxes are removed via `unregister`. The grace duration
  is passed by the caller (typically the App wiring).
