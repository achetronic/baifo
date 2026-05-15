# ARCHITECTURE.md
Bird's-eye technical design. Read after `AGENTS.md`. Cross-references
`WORKER_RUNTIME.md`, `SECRETS.md`, `CONFIG.md`, `TUI_DESIGN.md`, `A2A.md`.

## Process model

baifo is a single Go binary. One process, multiple goroutines:

```
main goroutine        BubbleTea event loop (baifo) OR REPL scanner (baifo chat)
                      OR HTTP server (baifo server). Blocks on user input.

worker goroutines     one per live worker; spawned/cancelled by the
                      workers.Manager.

config watcher        fsnotify on the active .baifo/ directory (250ms
                      debounce); calls App.ReloadFromDisk.

http server           `baifo server` always starts one. The TUI also starts
                      one when `a2a.enabled: true` in baifo.yaml. In both
                      cases: http.Server with `/api/a2a/<id>` and `/healthz`.

audit recorder        per-call goroutine: each Recorder.Record opens its
                      own short SQLite transaction.

stream translator     one goroutine per live worker subscriber: pipes the
                      workers.WorkerEvent bus into a typed
                      facade.WorkerStreamEvent channel for the TUI.
```

No background daemons, no IPC. When baifo exits, every goroutine is
cancelled via context propagation from the root `context.Context` owned
by `cmd/baifo/main.go` (or its sub-commands).

## Boot sequence

`internal/app.New(ctx, cfg, configDir)` runs, in order:

1. Open `baifo.db` (SQLite) under `<configDir>/data/baifo.db`. The data
   subdirectory and the tables are created on first open.
2. Wire the **audit recorder** (`internal/audit.NewRecorder`) directly
   against the DB; it is always available even during degraded boot.
3. Build the **sessions service** (`internal/sessions.New`). Wire a
   session titler that fires an async LLM call to name untitled
   sessions after a few turns.
4. Build the **secrets store** from `secrets.yaml` (encrypted or plaintext).
5. Build the **providers registry** from `cfg.Providers`, after
   expanding `${secret:NAME}` placeholders in `api_key` and `headers`
   via `internal/providers.ExpandSecrets`.
6. Build the **MCP registry** (`internal/mcps.NewRegistry`) wired with
   the secrets store, the OAuth token store, and the DCR client store.
7. Load `agents.yaml` and build the static-agent template index.
8. Build the **skills loader** rooted at `<configDir>/skills/`.
9. Load the **embeddings engine** (`internal/embeddings.New`) and build
   the **facts store** (`internal/facts.New`) on top of SQLite. The
   engine uses a compiled-in nomic-embed-text model; if it fails to
   load, the facts store falls back to substring search.
10. Start the **workers manager** (`workers.NewManager`) with a driver
    factory that goes through `app.newA2AWorkerDriverFactory`. The driver itself
    is an A2A executor (`adka2a`); worker sessions are in-memory only.
11. Build the **root agent** via `internal/agent.Builder.Build` and
    wrap its instance in an `a2asrv.RequestHandler` for the A2A path.
    Boot succeeds even when the root fails to build (e.g. unknown
    provider); the error is stored on the App and surfaced to the TUI.
12. Resolve the active session: auto-resume the most recent session or
    create a new one depending on `runtime.auto_resume_session`.
13. Start the **config watcher** (`internal/watcher.New`) that calls
    `App.ReloadFromDisk` on change.

Entry points after `New`:

- **TUI**: `cmd/baifo/tui.go` constructs `internal/tui.NewModelWithAutoScroll(facade, ...)`
  and starts the BubbleTea program.
- **`baifo chat`**: `cmd/baifo/chat_cmd.go` constructs its own runner from
  `app.RootAgent()` and `app.SessionService()` and drives a stdin/stdout
  REPL. Bypasses A2A and the TUI entirely.
- **`baifo server`**: `cmd/baifo/server_cmd.go` constructs
  `server.New(core, cfg)` where `core = app.App` (satisfies
  `server.Core` implicitly) and blocks on `Server.Run(ctx)`.

Shutdown is `App.Close()`: stop the watcher, drain workers (5 s grace
via `workers.Manager.Shutdown`), close the log file, close the database.
Context cancellation is the caller's responsibility (the sub-command that
constructed the App cancels the root context before calling Close).

## Core types

```go
// internal/facade
type Facade interface {
    SendMessage(ctx, text) iter.Seq2[*Event, error]
    /* ...full surface documented in AGENTS.md... */
}

// internal/app
type App struct {
    mu          sync.RWMutex     // protects everything below
    cfg         *config.Config
    configDir   string

    db          *storage.DB
    secrets     *secrets.Store
    providers   *providers.Registry
    mcps        *mcps.Registry
    audit       *audit.Recorder
    sessions    *sessions.Service
    titler      *sessionTitler   // async auto-title for untitled sessions
    workers     *workers.Manager
    agentTmpl   *agentTemplateIndex
    skills      *skills.Loader
    facts       *facts.Store

    root               *agent.Instance       // nil while ErrNoRoot
    rootRequestHandler a2asrv.RequestHandler // for SendMessage's A2A path
    rootBuildErr       error                 // last buildRoot() error

    watcher  *watcher.Watcher
    reloadCh chan facade.ReloadEvent

    userID    string  // fixed "user" for now
    sessionID string  // active root session id
}

// internal/agent
type Spec struct {
    Name           string
    Description    string
    Prompt         string
    Provider       string
    Model          string
    Reasoning      string            // "minimal"|"low"|"medium"|"high"; empty = model default
    AllowedSecrets []string
    UnrestrictedSecrets bool        // root only: AllowAll instead of the list
    MCPs           []string
    Skills         []string
    ExtraTools     []tool.Tool       // wiring hook for spawn / todos / ...
}

type Instance struct {
    ID    string
    Spec  Spec
    Agent agent.Agent      // built via Builder
    LLM   model.LLM        // exposed so contextguard can summarise
}

// internal/workers
type Spec struct {
    Kind           Kind          // KindStatic | KindDynamic
    Name           string
    Description    string
    Prompt         string
    Provider       string
    Model          string
    Reasoning      string        // "minimal"|"low"|"medium"|"high"; empty = model default
    Skills         []string
    MCPs           []string
    AllowedSecrets []string
    InitialMessage string
}

type Worker struct { /* ID, Kind, Status, sandbox path, events, cancel */ }

type Manager struct {
    /* registry, global event bus, sandbox allocator, driver factory */
}
```

## Builder (the chokepoint)

`internal/agent.Builder.Build(ctx, id, spec)` is the **single place** the
codebase constructs an ADK agent. It enforces every cross-cutting concern:

1. Resolves `Spec.Provider + Spec.Model` to an `model.LLM` through the
   providers registry.
2. Applies the optional `Builder.ModelWrapper` (used in debug mode to
   dump every LLM request to disk).
3. Resolves `Spec.MCPs` via the MCP registry, splitting into:
   - built-in MCPs → a concrete `[]tool.Tool` (eager).
   - external HTTP/stdio MCPs → a `tool.Toolset` (lazy: ADK connects on
     first `ListTools`).
4. Builds a static `InstructionProvider` from `Spec.Prompt` (avoids
   ADK's `{var}` substitution clashing with prompt content).
5. Wires the callback chain:
   - `BeforeToolCallbacks = [secrets.Expand]`, expands `${secret:NAME}`
     in tool args; skips tools listed in `Builder.OpaqueTools` (the
     spawn tools) so child-agent prompts are not eagerly expanded.
   - `AfterToolCallbacks  = [secrets.Redact, audit.Record]`.
   The order matters: audit runs **after** redaction so the audit log
   never stores raw secret values. When `Builder.ScrubAllResponses` is
   true (the default), every tool result is also scanned for any raw
   secret value in the store, closing the "MCP echoes a token it was
   not given" gap.
6. Returns `*Instance{ID, Spec, Agent, LLM}`.

**Contextguard is NOT attached inside the Builder.** It is attached one
level up, in `internal/app.buildRoot`, by passing
`runner.Config.PluginConfig = agent.BuildContextGuardConfig(inst.LLM,
[]AgentGuardSpec{...})`. Each `AgentGuardSpec` names an agent and
carries its `config.ContextGuardConfig`. This keeps `agent.Builder`
free of session-lifecycle concerns; the contextguard plugin only makes
sense on a runner.

No code outside the Builder may call `llmagent.New` directly. This is
the invariant that guarantees every agent, root and workers, gets the
secrets pipeline and audit log uniformly.

## Memory model

Two distinct memory concerns:

**Session memory**, full event history of an active conversation. Each
session has an ID; the SQLite session service in `internal/sessions/` stores
events in the `session_events` table (keyed by app+user+session+index) and
metadata in the `sessions` table (keyed by app+user+session). The root has one
active session (selectable via `/session`). Worker sessions live in
memory only and die with the worker.

Critically: when a tool writes `ctx.State().Set("key", value)`, ADK
populates `EventActions.StateDelta`. The session service's `AppendEvent` merges
non-`temp:` keys into the persisted session state, so values like the
TODO list survive process restarts.

**Long-term facts**, `internal/facts.Store`, accessed by the agents via
`internal/tools/memory` (`search_memory`, `save_to_memory`,
`update_memory`, `delete_memory`). Stored in the SQLite `facts` table.
When the compiled-in embeddings engine loads, `SearchMemory` ranks
results by cosine similarity (nomic-embed-text, 768-dim); otherwise it
falls back to a case-insensitive substring match across content,
category, and author.

## MCP registry

`internal/mcps.Registry` keeps:

- A map `name → Spec` of declared MCPs.
- A lazy cache of built-in instances (filesystem, browse).
- An OAuth token store backed by SQLite (`internal/mcps/tokenstore.go`).
- An OAuth driver (`internal/mcps/oauth.go`) for HTTP MCPs with
  `auth.kind: oauth`.

For the Builder it exposes two methods:

- `Tools(name) ([]tool.Tool, error)`, used for built-ins; returns a
  concrete slice the Builder can append to `llmagent.Config.Tools`.
- `Toolset(name) (tool.Toolset, error)`, used for external HTTP/stdio
  MCPs; ADK connects on first `ListTools`.

`Tools(name)` is the canonical way to get the filesystem builtin's
`exec`, `read_file`, etc. The filesystem builtin does not clamp paths to
a per-agent sandbox: agents get full host filesystem access by design
(decision #9).

Hot reload rebuilds the registry; live workers keep their snapshot.

## Skill loader

`internal/skills.Loader` exposes:

- `List() ([]string, error)`, slug list.
- `FrontmatterOf(ctx, slug) (*adkskill.Frontmatter, error)`, strict
  ADK frontmatter struct.
- `LoadAll() ([]*Skill, error)`, baifo's `Skill` with `Slug`, `Name`,
  `Description`, `Extra`, `Body`, `Root`.
- `Source() (skill.Source, error)`, ADK's `skill.Source` adapter.

`internal/tools/skills` wraps `Source()` and ADK's `skilltoolset` to
expose `list_skills`, `load_skill`, `load_skill_resource` as agent tools.

No DB cache: every call reads from disk. Tolerant frontmatter parser
(`tolerant.go`) accepts unknown keys and stores them in `Skill.Extra`.

## Providers registry

`internal/providers.Registry` keeps a map `type → builder`. Each
provider sub-package (`openai`, `anthropic`, `gemini`) registers itself
via `init()`. Any OpenAI-compatible endpoint (Ollama, OpenRouter, vLLM,
...) is an `openai` provider with a custom `url`, served by the same
`openai` package.

`internal/providers/allproviders/` blank-imports the three sub-packages
so a single `_ "...allproviders"` in `main.go` registers everything.

`internal/providers.ExpandSecrets(entries, store)` runs at config-load
time and again on every reload, expanding `${secret:NAME}` in `api_key`
and headers values.

## Storage layer (SQLite)

`internal/storage/db.go` opens `.baifo/data/baifo.db` and creates the
SQLite tables via `internal/storage/migrate.go`:

- `meta`, key/value store for schema version.
- `sessions`, session metadata (app, user, session_id, title, timestamps, state).
- `session_events`, per-session event payloads (app, user, session_id, event_index, event_data).
- `facts`, long-term memory, including an `embedding` BLOB column.
- `audit`, append-only log of tool calls (redacted args/results).
- `oauth_tokens`, OAuth tokens per MCP.
- `oauth_clients`, DCR (Dynamic Client Registration) data per MCP.

Writes go through the package that owns the table
(`sessions.Service`, `facts.Store`, `audit.Recorder`,
`mcps.TokenStore`, `mcps.DCRClientStore`). No central batcher.

Migrations live in `internal/storage/migrate.go`. `migrate()` is called
from `DB.init()` on every `Open()`. `migrateV1` applies the baseline
tables, `ensureEmbeddingColumn` adds the `embedding` BLOB to `facts`
on databases that predate the column (idempotent), and `setVersion`
stamps `meta.schema_version`. Future schema changes slot in as
sequential `if version < N` blocks inside `migrate()`.

## Config hot reload

`internal/watcher.New()` runs fsnotify with a 250 ms debounce. On
change it calls `App.ReloadFromDisk(ctx)`:

1. Re-read `baifo.yaml`, `agents.yaml`. Validate.
2. Re-expand provider secrets.
3. Rebuild the providers registry, MCP registry, and agent-template index.
   The skills loader is not rebuilt: it reads directly from disk on every
   call, so edits to skill files take effect immediately without a reload.
4. Rebuild the root agent via `buildRoot`, which also sets
   `a.rootRequestHandler` to the new A2A request handler.
5. Publish a `facade.ReloadEvent` on the subscribe channel.

Live workers are **not** rebuilt, they keep their snapshot until they
finish.

Reload failures keep the previous state in place. The watcher callback
logs the error via `slog.Error("config reload failed", "err", err)`; there
is no TUI toast, so users must tail the log file to see watcher-triggered
reload failures.

## TUI ↔ Core coupling

The TUI imports `internal/facade` only. The concrete `*App` implements
the interface; the TUI does not see the `app` package.

The `Facade` includes a `SubscribeWorker(id)` method that returns a
typed `<-chan facade.WorkerStreamEvent` plus the recent history. The
underlying translation from `workers.WorkerEvent` to the typed channel
happens in `internal/app/workers_stream.go` (a one-shot goroutine per
subscription).

`SubscribeReload()` exposes the reload signal so the TUI can refresh
its overlays.

## HTTP daemon ↔ Core coupling

`internal/server` declares a `Core` interface:

```go
type Core interface {
    Agents() []a2a.AgentEntry
    SessionService() session.Service
    MemoryService() memory.Service
    RootName() string
    RootBuildError() error
}
```

`*app.App` implements it implicitly via `internal/app/server_view.go`.
`internal/server` does **not** import `internal/app`. The dependency
flows `app → server.Core`.

Endpoints:

- `POST /api/a2a/<agent-id>`, A2A JSON-RPC, SSE streaming.
- `GET /api/a2a/.well-known/agent-cards.json`, list of cards.
- `GET /api/a2a/<agent-id>/.well-known/agent-card.json`, per-agent.
- `GET /healthz`, `{status, root_name, exposed_agents[],
  root_build_error?}`.

`Server.Rebuild()` is called once at the start of `Server.Run()` to
mount the initial handlers. The headless `baifo server` command also
subscribes to reload events: a goroutine drains `a.SubscribeReload()` and
calls `srv.Rebuild()` on each event, so editing `baifo.yaml` while the
daemon runs takes effect live without a process restart.

## Testing strategy

- Unit tests next to every store, builder, tool source, and TUI
  component.
- Integration-flavoured tests in `internal/app/` (`app_test.go`,
  `example_boot_test.go`, `llm_request_real_test.go`) that boot an `App`
  with a minimal config dir.
- The agent.Builder is exercised against a `fakeModel`
  (`internal/agent/builder_test.go::fakeModel`) so tests don't hit the
  network.
- TUI tests dispatch `tea.Msg`s directly on the Model (no `teatest`).
- `go test -race ./...` is the default in `make test`.

## Concurrency invariants

- `App.mu` is held only during in-memory reads/writes of the
  long-lived fields. Never held while doing IO or waiting on a channel.
- Workers never write to TUI state directly. They publish to the event
  bus; the TUI consumes typed messages translated by
  `internal/app/workers_stream.go`.
- The Builder is safe to call concurrently; each call constructs an
  independent `*Instance` with no shared mutable state.
- The SQLite DB (WAL mode) is single-writer multi-reader; every package
  owns its own short transactions.
- session state (the in-memory state map) is mutex-protected per-session.

## Open questions

- **Worker completion notification.** The global event bus routing is
  implemented (`goReplay` + `publishStatusChange` in `manager.go`);
  the TUI chips receive `StatusChange` events. `App` does not currently
  subscribe to `Manager.GlobalBus()` to push terminal worker status
  into the root's session as a synthetic event, so the root cannot
  react to worker completion without polling.
- **Worker to worker direct communication.** Not in v1. If a worker
  needs another worker, the root spawns both and brokers.
- **Plugin system for external providers / MCPs.** Deferred. The MCP
  stdio path covers most cases; new providers are a `init()` registration.
- **Multi-user / multi-machine.** Out of scope. baifo is explicitly a
  personal harness.
