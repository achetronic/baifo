# AGENTS.md
Terminal-native, agentic harness in Go. Single-binary TUI that runs a root agent capable of spawning, supervising and collecting sub-agents on the fly. Built on top of Google ADK + adk-utils-go. Exposes the root agent via the A2A protocol so external systems can talk to it.

## Project overview

**baifo** (pronounced *ái-do*, from *Agent I Do*) is a personal multidisciplinary
assistant that lives in your terminal:

- **TUI-first**: no web UI, no admin frontend. Everything is configured from
  `baifo.yaml` / `agents.yaml`, slash commands inside the TUI, or by the root
  agent editing those files through its filesystem tools.
- **Agentic but flat**: no flow editor, no DAGs. Orchestration is decided by
  the root LLM at runtime through spawn/query/collect tools.
- **Single user, local-first**: one operator, one machine, one `.baifo/`
  directory per project (or one global one).
- **Built on ADK**: the only client is the local TUI (or `baifo chat`) and the
  only external surface is A2A.
- **Multidisciplinary**: coding is one capability among many. The root prompt
  positions baifo as a generalist assistant (research, planning, writing,
  shell operations, file triage, code) with the same tool surface for all of
  them.

This document is **descriptive**: it describes the codebase as it stands
today. When code and doc disagree, code wins and the doc is stale.

## Core mental model

```
┌──────────────────────────────────────────────────────────────────┐
│                          baifo binary                             │
│                                                                  │
│   ┌──────────────┐    ┌─────────────────────────────────────┐    │
│   │              │    │           Root agent (ADK)          │    │
│   │  TUI         │◄──►│  prompt · llm · skills · mcps       │    │
│   │  (BubbleTea) │    │  contextguard · tools (spawn,       │    │
│   │              │    │  todos, memory, skill, MCP)         │    │
│   └──────┬───────┘    └────────────────┬────────────────────┘    │
│          │                             │                         │
│   ┌──────▼─────────┐                   ▼                         │
│   │  facade.Facade │       ┌──────────────────────┐              │
│   │  (interface)   │       │   Worker Manager     │              │
│   └──────┬─────────┘       │  (goroutine pool)    │              │
│          │                 └──────┬───────────────┘              │
│   ┌──────▼─────────┐              │                              │
│   │  app.App       │  ┌───────────┼────────────┐                 │
│   │  (impl)        │  ▼           ▼            ▼                 │
│   └─────┬──────────┘  ┌────────┐ ┌────────┐ ┌────────┐           │
│         │             │worker A│ │worker B│ │worker C│           │
│         │             │static  │ │dynamic │ │dynamic │           │
│         │             └────────┘ └────────┘ └────────┘           │
│         │                                                        │
│         │     ┌──────────────────────────────────┐               │
│         └────►│  Event Bus (root + all workers)  │               │
│               └──────────────────────────────────┘               │
│                                                                  │
│   ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐    │
│   │  Secrets     │  │  Storage     │  │  HTTP server        │    │
│   │  (yaml,enc)  │  │  (sqlite)    │  │  /api/a2a/ + healthz│    │
│   └──────────────┘  └──────────────┘  └─────────────────────┘    │
└──────────────────────────────────────────────────────────────────┘
```

- You launch `baifo` and land in a conversation with **the root agent**.
- The root has tools to **spawn workers** (static, dynamic, or both,
  controlled by `spawn.mode`).
- Each worker is a **complete ADK agent** running in its own goroutine: own
  session, own skills/MCPs, own filesystem sandbox (by default).
  ContextGuard is wired only for the root and for static templates that
  opt in; ephemeral dynamic workers run un-guarded.
- The root can `query_agent`, `kill_agent`, `list_running_agents`,
  `inspect_agent`, `collect_agent` workers asynchronously.
- The TUI shows the root chat **and** a separate tab where you can spy on
  any running worker in real time (Workers overlay).
- The HTTP daemon (`baifo server`) mounts the A2A endpoints under
  `/api/a2a/<agent-id>` and a `/healthz` probe.

## Repository layout

```
baifo/
├── cmd/
│   └── baifo/                       # entrypoint + CLI dispatch
│       ├── main.go                 # flag parsing, sub-command dispatch
│       ├── tui.go                  # `baifo` (default) → TUI launcher
│       ├── chat_cmd.go             # `baifo chat` → TUI-free REPL harness
│       ├── server_cmd.go           # `baifo server` → headless HTTP daemon
│       ├── secrets_cmd.go          # `baifo secrets ...` sub-commands
│       ├── wizard.go               # first-run wizard (no .baifo/ found)
│       └── prompt.go               # shared interactive prompts
├── internal/                       # everything internal/, single-binary
│   ├── facade/                     # PUBLIC CONTRACT
│   │   └── facade.go               # Facade interface + every DTO
│   ├── app/                        # COMPOSITION ROOT
│   │   ├── app.go                  # New(), Close(), ReloadFromDisk(),
│   │   │                           # buildRoot(), buildWorkerAgent(),
│   │   │                           # combinedRootTools()
│   │   ├── agents.go               # agent-template CRUD methods
│   │   ├── providers.go            # provider CRUD methods
│   │   ├── secrets.go              # secret CRUD methods
│   │   ├── skills.go               # skill CRUD methods
│   │   ├── facts.go                # fact CRUD methods
│   │   ├── contextguard.go         # ContextGuardStatus (TUI gauge: reads
│   │   │                           # the plugin's session-state keys)
│   │   ├── server_view.go          # Agents()/SessionService()/MemoryService()
│   │   │                           #, implements server.Core
│   │   ├── workers_stream.go       # SubscribeWorker translation
│   │   ├── debug_llm.go            # opt-in LLM request dumper
│   │   └── yaml_helpers.go         # tiny yaml.Marshal/Unmarshal wrappers
│   ├── agent/                      # ADK AGENT CONSTRUCTION (chokepoint)
│   │   ├── builder.go              # Spec → ADK agent. NOTHING else in
│   │   │                           # the repo calls llmagent.New directly.
│   │   ├── callbacks.go            # secrets Expand / Redact, audit
│   │   ├── instruction.go          # InstructionProvider wrapper
│   │   ├── contextguard.go         # BuildContextGuardConfig (runner plugin)
│   │   │                           # + GuardContextWindow/GuardThreshold
│   │   │                           # helpers + state-key constants the TUI
│   │   │                           # gauge reads back
│   │   ├── resilient_toolset.go    # external-MCP toolset guard (timeout/
│   │   │                           # error/panic → no tools, never blocks)
│   │   ├── prefixed_toolset.go     # namespaces external-MCP tools as
│   │   │                           # <mcp>__<tool> (provenance + no collisions)
│   │   └── resolve.go              # ResolveMCPs/Skills/Secrets (allowlist
│   │                               # resolution: empty list = all)
│   ├── tools/                      # AGENT-FACING TOOL SOURCES
│   │   ├── spawn/                  # spawn_static_agent, spawn_dynamic_agent,
│   │   │                           # spawn_dynamic_agents, query_agent,
│   │   │                           # list_agents, list_running_agents,
│   │   │                           # inspect_agent, collect_agent, kill_agent
│   │   ├── todos/                  # todos_list/write/update/clear (state
│   │   │                           # key "todos", survives contextguard)
│   │   ├── memory/                 # search/save/update/delete_memory
│   │   │                           # (wraps adk-utils-go memory toolset)
│   │   ├── models/                 # list_models (provider model catalogue
│   │   │                           # from catwalk; always on the root)
│   │   └── skills/                 # list_skills, load_skill,
│   │                               # load_skill_resource (wraps ADK
│   │                               # skilltoolset)
│   ├── mcps/                       # MCP CLIENT ORCHESTRATION
│   │   ├── registry.go             # name → toolset resolution
│   │   ├── toolset.go              # external-MCP toolset adapter
│   │   ├── oauth.go                # OAuth flow for HTTP MCPs
│   │   ├── authenticate.go         # /mcp auth driver
│   │   ├── dcrclientstore.go       # Dynamic Client Registration cache
│   │   ├── branding.go             # CIMD client-metadata document
│   │   ├── testconnection.go       # MCP reachability probe
│   │   ├── tokenstore.go           # SQLite-backed OAuth token cache
│   │   └── builtin/
│   │       ├── filesystem/         # filesystem builtin (read/write/exec/
│   │       │                       # search/diff/edit/process/scratch/undo)
│   │       └── browse/             # browse builtin (web_fetch, web_search)
│   ├── providers/                  # LLM PROVIDER REGISTRY
│   │   ├── provider.go             # Spec, Registry types
│   │   ├── registry.go             # Register(type, build), Model(ctx, ...)
│   │   ├── expansion.go            # ExpandSecrets, turns ${secret:NAME}
│   │   │                           # into real values at boot
│   │   ├── allproviders/           # blank-imports the four below
│   │   ├── openai/                 # also serves openai-compatible + ollama
│   │   ├── anthropic/
│   │   └── gemini/
│   ├── secrets/                    # AES-256-GCM + plaintext secrets store
│   │   ├── store.go                # Load/Save, per-value seal/unseal
│   │   ├── pipeline.go             # Expand / Redact callback factories
│   │   └── sort.go                 # Entry slice sorting helpers
│   ├── audit/                      # APPEND-ONLY TOOL-CALL AUDIT
│   │   └── audit.go                # SQLite audit table + Recorder
│   ├── embeddings/                 # IN-PROCESS TEXT EMBEDDINGS
│   │   ├── engine.go               # nomic-embed-text-v1.5 forward pass
│   │   ├── tokenizer.go            # BERT WordPiece tokenizer
│   │   ├── weights.go              # int8 weight blob loader (NMB1)
│   │   └── assets/                 # go:embed'd quantized weights + vocab
│   ├── facts/                      # LONG-TERM MEMORY STORE
│   │   ├── store.go                # memory.Service + ExtendedMemoryService
│   │   ├── methods.go              # Search/Add/Update/Delete entries
│   │   └── internals.go            # semantic (cosine) + substring search
│   ├── sessions/                   # SQLite SESSION.SERVICE FOR ADK
│   │   ├── sqlite.go               # SQLite session.Service for ADK
│   │   └── internals.go            # session/state/event SQLite helpers
│   ├── skills/                     # SKILL LOADER (on-disk)
│   │   ├── loader.go               # List/FrontmatterOf/LoadAll
│   │   ├── source.go               # adk skill.Source adapter
│   │   ├── tolerant.go             # tolerant frontmatter parser
│   │   └── installer/              # unzip/untar skill packages
│   ├── workers/                    # WORKER RUNTIME
│   │   ├── manager.go              # Spawn/List/Query/Inspect/Collect/Kill
│   │   ├── worker.go               # Spec + per-worker state
│   │   ├── worker_driver.go           # default Driver: an ADK runner
│   │   ├── events.go               # per-worker + global event bus
│   │   ├── history.go              # per-worker ring buffer of events
│   │   └── sandbox.go              # SandboxAllocator (per-worker dir)
│   ├── storage/                    # SQLite WRAPPER
│   │   └── db.go                   # Open/Close + bucket constants
│   ├── config/                     # YAML LOADERS + DISCOVERY
│   │   ├── config.go               # Config struct + Load()
│   │   ├── agents.go               # agents.yaml loader (AgentTemplate)
│   │   ├── discover.go             # --config-dir / $BAIFO_HOME / PWD walk
│   │   │                           # / XDG / $HOME
│   │   ├── env_expand.go           # ${ENV_VAR} expansion at load time
│   │   └── yamledit/               # comment-preserving YAML edits used by
│   │                               # /agent add, /mcp add, ...
│   ├── scaffolds/                  # YAML TEMPLATE STRINGS FOR THE EDITOR
│   │   ├── agent.go                # agents.yaml entry scaffold
│   │   ├── mcp.go                  # baifo.yaml mcps[] scaffold
│   │   └── provider.go             # baifo.yaml providers[] scaffold
│   ├── server/                     # HTTP DAEMON (decoupled from app)
│   │   ├── server.go               # Server + Core interface
│   │   └── a2a/                    # A2A protocol handler
│   │       └── handler.go          # /api/a2a/<id> + agent cards
│   ├── tui/                        # BubbleTea v2 TUI (flat)
│   │   ├── model.go                # root tea.Model
│   │   ├── theme.go                # palette, accents, glyphs
│   │   ├── layout.go               # responsive breakpoints
│   │   ├── header.go               # top header / status render
│   │   ├── tabs_views.go           # per-view renderers
│   │   ├── palette.go              # slash-command autocomplete popup
│   │   ├── palette_tree.go         # slash-command tree + suggester
│   │   ├── overlay_chrome.go       # renderModal primitive
│   │   ├── overlays.go             # overlay routing
│   │   ├── chips.go                # status chips
│   │   ├── chat.go                 # chatView component
│   │   ├── components.go           # composer, status bar, splash, help
│   │   ├── commands.go             # slash dispatcher
│   │   ├── editor_overlay.go       # embedded YAML editor overlay
│   │   ├── secret_overlay.go       # secret-prompt overlay glue
│   │   ├── interlocutor.go         # talk-to-worker view
│   │   ├── overlays/               # self-contained overlay components
│   │   │   ├── secret_prompt.go    # masked-input modal
│   │   │   ├── editor_validators.go# YAML validators per kind
│   │   │   └── skill_styler.go     # SKILL.md syntax highlighter
│   │   └── components/
│   │       └── editor/             # reusable multiline editor + mdhl/yamlhl
│   ├── watcher/                    # fsnotify config hot reload
│   ├── logging/                    # slog wrapper with redaction sink
│   └── server/...                  # (see above)
├── .agents/                        # docs for AI coding assistants (you)
├── config/                         # example .baifo/ tree (usable fixture)
│   ├── baifo.yaml
│   ├── agents.yaml
│   ├── secrets.yaml
│   └── data/                       # gitignored runtime data
├── .github/workflows/ci.yml        # CI: golangci-lint + make test on Go 1.25
├── Makefile
├── go.mod / go.sum
└── README.md
```

## Public contract: the `Facade` interface

`internal/facade.Facade` is the single contract every client of baifo talks
to: the TUI, the `baifo chat` REPL, the HTTP daemon's health probe, future
A2A clients. It groups methods by concern:

- **Agent loop**: `SendMessage`, `RootName`, `RootBuildError`, `ConfigDir`,
  `ModelName`, `SessionID`, `ContextGuardStatus`.
- **Sessions**: `ListSessions`, `NewSession`, `SwitchSession`,
  `RenameSession`, `DeleteSession`.
- **Workers**: `ListWorkers`, `KillWorker`, `CollectWorker`,
  `SubscribeWorker`, `SubscribeWorkerLifecycle`, `SendToWorker`.
- **Skills**: `ListSkills`, `SkillDetails`, `SkillContent`, `SkillScaffold`,
  `UpsertSkill`, `DeleteSkill`, `InstallSkill`.
- **MCPs**: `ListMCPs`, `MCPDetails`, `MCPYAML`, `MCPScaffold`,
  `UpsertMCPFromDisk`, `DeleteMCPFromDisk`, `AuthenticateMCP`.
- **Providers**: `ListProviders`, `ProviderDetails`, `ProviderYAML`,
  `ProviderScaffold`, `UpsertProvider`, `DeleteProvider`.
- **Secrets**: `ListSecretNames`, `SetSecret`, `DeleteSecret`,
  `SecretsEncrypted`, `EncodeSecrets`, `DecodeSecrets`.
- **Agent templates**: `ListAgentTemplates`, `AgentDetails`, `AgentYAML`,
  `AgentScaffold`, `UpsertAgent`, `DeleteAgent`.
- **Facts (long-term memory)**: `ListFacts`, `FactDetails`, `AddFact`,
  `FactContent`, `UpdateFact`, `DeleteFact`.
- **Lifecycle**: `ReloadFromDisk`, `SubscribeReload`, `Close`.

The concrete implementation is `internal/app.App`. Clients depend only on
`internal/facade`; they do not import `internal/app`. This lets the TUI be
tested against stub Facades without dragging in app's transitive boot
graph (15+ packages).

## Core concepts

### Root agent

The single agent you talk to from the TUI (or `baifo chat`). The root and
the spawnable sub-agents share one schema and live together in
`.baifo/agents.yaml`; the root is the entry flagged `root: true` (exactly
one entry must set it).

```yaml
# .baifo/agents.yaml
agents:
  - name: baifo
    root: true                # the always-on entry point
    description: Multidisciplinary local assistant.
    prompt: |
      You are baifo, a multidisciplinary assistant that lives in the
      user's terminal. Coding is one of the things you do well, but
      not your only job: research, planning, writing, summarising,
      triaging files on the user's machine, running shell commands,
      sketching ideas, treat them all as first-class work.
      ...
    llm:
      provider: gemini
      model: gemini-3.5-flash
    skills: []                # empty = root sees every skill installed
    mcps: []                  # empty = root sees every MCP registered
    context_guard:
      enabled: true
      strategy: threshold
      max_tokens: 900000
      max_turns: 20
    allowed_secrets: []       # ignored for the root: it always gets AllowAll
```

The root is always present, never spawned, never killed; it receives the
spawn / memory / todos / skills / list_models tools and an AllowAll secrets allower
regardless of `allowed_secrets`. Its session persists in SQLite across
restarts when `runtime.auto_resume_session: true`. `/session new` starts
a fresh root session; `/session` (or its overlay) lists past ones and lets
you resume / rename / delete.

### Workers (sub-agents)

A **worker** is an ADK agent running in its own goroutine with its own
session (in-memory only; not persisted across baifo restarts), own skills,
MCPs and secrets allowlist. Every worker also gets a per-worker workspace
directory under `<data_dir>/workspaces/<id>/`, allocated at spawn and
removed on collect / shutdown (kept when `runtime.keep_workspaces: true`).
Two flavours:

**Static workers**, predefined templates in `.baifo/agents.yaml` (the
non-root entries). The root invokes them by name through
`spawn_static_agent`. Same spec every time. Example:

```yaml
# .baifo/agents.yaml
agents:
  - name: code-reviewer
    description: "Reviews code files and returns a structured review."
    prompt: "You are a code reviewer..."
    llm:
      provider: gemini
      model: gemini-3.5-flash
    skills: []
    mcps: [filesystem]
    allowed_secrets: []
    context_guard:
      enabled: true
      strategy: threshold
```

The `AgentTemplate` schema is `name`, `description`, `prompt`, `llm`,
`skills`, `mcps`, `context_guard`, `allowed_secrets` (plus `root`). There
is no per-agent `sandbox:` block and no `output_schema:`; `workers.Spec`
carries neither.

**Dynamic workers**, composed by the root at runtime via
`spawn_dynamic_agent`. The root chooses prompt, description, MCPs,
skills, allowed_secrets and provider+model. Names must validate against
the live universe (every skill, MCP, secret, provider must already exist
in baifo.yaml). The
spawn-tool description is **dynamically composed at boot** to enumerate
the live universe with metadata: every skill with its frontmatter
description, every MCP with the tool names it exposes, every provider,
every secret with its description.

The `spawn.mode` config knob (`none` / `static` / `dynamic` / `both`)
controls which spawn tools are exposed.

### Skills

On-disk packages:

```
.baifo/skills/{slug}/
├── SKILL.md             # frontmatter (name, description) + markdown body
├── references/          # optional reference docs
├── assets/              # optional templates, configs
└── scripts/             # optional executable scripts
```

Loaded live from disk every time. Each agent can opt into a subset; empty
list means "all installed skills are visible". Tools are exposed via
`internal/tools/skills` (wrapping ADK's `skilltoolset`).

### MCPs

Two builtin in-process MCPs ship with baifo:

- **filesystem**, `ls`, `read_file`, `write_file`, `edit_file`, `search`,
  `diff`, `exec`, `process_status`, `process_kill`, `scratch`, `undo`,
  `system_info`.
- **browse**, `web_fetch`, `web_search`.

External MCPs are supported via HTTP (Streamable transport) or stdio
(Command transport). Defined in `baifo.yaml`. HTTP MCPs may declare
`auth.kind: oauth` to drive an OAuth flow; tokens persist in the SQLite
`oauth_tokens` bucket and refresh transparently. Leaving `client_id` and
`client_secret_ref` empty opts into discovery: baifo advertises Dynamic Client
Registration (RFC 7591) and CIMD (Client ID Metadata Document), and the
`auth.registration` selector decides which (see below). Scopes are
discovered from the protected-resource metadata (RFC 9728), never hardcoded.
The `baifo` TUI exposes `/mcp auth <name>` to drive the flow
interactively.

`auth.registration` (`auto` | `cimd` | `dcr`, default `auto`) controls the
CIMD/DCR choice per MCP. The MCP SDK decides in a single pass: if CIMD is
advertised AND the AS announces support, it picks CIMD and never falls back,
so an IdP that supports CIMD but rejects our client_id URL (domain not
whitelisted, brand document not served, etc.) would fail with no recovery.
Setting `registration: dcr` suppresses CIMD entirely so the SDK registers
dynamically; `cimd` suppresses DCR; `auto` advertises both. It is ignored when
`client_id`/`client_secret_ref` are set (those force client_credentials).
Note the brand CIMD document at `brandingCIMDURL` must be served for CIMD to
actually succeed against a supporting AS; until it is, prefer `dcr` (or `auto`
against ASs that don't advertise CIMD).

The tools an external MCP exposes are **namespaced with the MCP's configured
name** before reaching an agent: `images_upscale` from the `magnific` MCP
surfaces to the model as `magnific__images_upscale` (separator `__`). This
gives the model unambiguous provenance, it can tell which server a tool came
from, and prevents two MCPs that both expose, say, `list` from colliding. The
rename is presentation-only: the wrapped tool still calls the server with its
original name, so the prefix never crosses the wire. Implemented by
`prefixedToolset` in `internal/agent/prefixed_toolset.go`, applied in the
Builder to external (http/stdio) MCPs only, builtin MCPs (filesystem, browse)
keep their flat, well-known names. `/mcp test <name>` reports the server's raw
tool names, not the prefixed ones.

### Providers

LLM providers. Today the registry supports `openai`, `anthropic`, `gemini`,
plus `openai-compatible` and `ollama` (both served by the openai package).
Defined under `providers:` in `baifo.yaml`, referenced from each agent's
`llm.provider` in `agents.yaml`. `${secret:NAME}` placeholders
are expanded at config-load time by `internal/providers.ExpandSecrets`.

The root can introspect what models each configured provider offers via the
`list_models` tool (`internal/tools/models`), wired unconditionally so the
root can pick a model both when composing a dynamic worker and when the user
asks it to author a static agent template. It maps each provider's *type*
onto catwalk's embedded model catalogue (the same source contextguard and the
TUI autocomplete use) and returns, per provider, its `default_small_model` /
`default_large_model` plus every known model with context window, per-1M-token
cost and reasoning support (`can_reason`, `reasoning_levels`,
`default_reasoning_effort`), so the root can size an agent's model AND its
reasoning effort to the task. Types with no fixed catalogue
(`openai-compatible`, `ollama`) return a note rather than an empty list. The
lookup is offline; no provider `/models` API call.

#### Reasoning effort

Each agent's `llm.reasoning` (in `agents.yaml`) or a dynamic spawn's
`llm.reasoning` accepts `minimal | low | medium | high` (empty / `off` = the
model's default). The `agent.Builder` resolves it once
(`internal/agent/reasoning.go`) into two forms because the backends disagree on
where the knob lives:

- **openai** (o-series) and **gemini** read it request-side: the builder sets
  `llmagent.Config.GenerateContentConfig.ThinkingConfig` (ThinkingLevel for
  openai's `reasoning_effort`, ThinkingBudget for gemini's native thinking).
- **anthropic** reads it at construction time: the builder passes a thinking
  token budget through `providers.ModelOptions` to the anthropic adapter's
  `ThinkingBudgetTokens` (the registry cache key includes the budget so two
  agents on the same model with different efforts get distinct clients).

The setting is only applied when explicitly requested, so a non-reasoning model
is never sent a reasoning parameter it would reject. Invalid values are rejected
at the config / spawn boundary.

**Vocabulary is deliberately a subset.** baifo honours exactly
`minimal | low | medium | high`. catwalk's catalogue lists extra levels for
some models (`xhigh`, `max` on recent gpt-5.x / claude-opus); baifo does **not**
expose them because the request-level path for openai and gemini goes through
`genai.ThinkingLevel`, which only defines those four. Supporting `xhigh`/`max`
later means mapping them to a token budget (works for anthropic) and waiting for
genai to add the constants for openai/gemini.

**openai-compatible / ollama caveat.** These share the openai adapter, so a
`reasoning` value *is* sent as `reasoning_effort`. Whether it works depends on
the endpoint: some accept it, some ignore it, some error on an unknown field.
baifo cannot tell, because catwalk does not catalogue those provider types, it
is the operator's responsibility to know what their endpoint supports.

The agent editor autocompletes `reasoning:` (trigger `reasoning: `,
`overlays.ReasoningCompletionProvider`). It reads the nearest `model:` line
above the cursor and tailors the suggestions: a known reasoning model offers
only its supported levels (catwalk `reasoning_levels` intersected with the baifo
vocabulary, default flagged); a known non-reasoning model offers only `off` with
an explanation; an unknown model (openai-compatible, ollama, or nothing typed
yet) offers the full set. To make this possible the editor's `CompletionContext`
carries a `Lines []string` snapshot of the buffer so a provider can read sibling
lines.

### Memory

- **Session memory**, full conversation of the root, persisted in SQLite
  (`sessions` + `session_events` tables). Workers' sessions are in-memory only.
- **Long-term facts**, `internal/facts` store. Written by the root via
  `save_to_memory` and read via `search_memory`. Persisted in the SQLite
  `facts` table. Search is semantic: each fact carries a 768-dim embedding
  (nomic-embed-text-v1.5, computed in-process by `internal/embeddings`) and
  queries rank by cosine similarity, falling back to substring match when no
  engine is available. Reachable via the `/fact` slash command.
- **Per-session TODO list**, `internal/tools/todos` writes the list to the
  ADK session-state key `"todos"`. The contextguard plugin from
  `adk-utils-go` already inspects that key and forwards it through every
  summarisation, so a multi-step plan survives context compaction without
  baifo-side bookkeeping.

No pgvector, no Redis. The embedding model is compiled into the binary, so
facts search needs no external service. SQLite is sufficient for a
single-user local harness.

### Secrets

Single file: `.baifo/secrets.yaml`. The store has **two modes**:

- **Encrypted** (`encryption_key` set in `baifo.yaml`): each value is
  `AES256GCM:v1:<nonce_b64>:<ct_b64>` (per-value nonce, PBKDF2 with salt
  `baifo-secrets-v1`, 200k iters, 32-byte key).
- **Plaintext** (`encryption_key: ""`): each value is `plain:v1:<base64>`.
  The `/secret encode` and `/secret decode` slash commands flip the mode
  in place once an encryption key is configured.

The model **never sees raw values**:

1. The agent's tool catalogue surfaces each secret by name + description.
2. The model writes `${secret:NAME}` literally inside tool-call arguments.
3. **BeforeToolCallback** walks the args and expands placeholders.
4. **AfterToolCallback** runs first the redactor (raw values → placeholders),
   then the audit recorder. Audit therefore always logs the redacted view.
5. The TUI / log sinks redact through the same pipeline when
   `secrets.redact_in_logs: true` (default).

See `SECRETS.md` for the full pipeline and known limitations.

### A2A

The A2A server is gated on `a2a.enabled: true` in `baifo.yaml` and is
hosted in two interchangeable ways: the headless `baifo server` daemon,
and the interactive TUI (`baifo`), which spins up the same server in a
background goroutine on boot when enabled and stops it on exit. `baifo
server` refuses to boot when disabled. There is no in-TUI toggle
enable/disable is config-only (edit via `/settings edit`),
applied on next start.

It mounts the A2A protocol under `/api/a2a/<agent-id>`. Today only the
root is exposed (`/api/a2a/root`), but the implementation is
multi-agent capable: the daemon calls `Core.Agents()` and registers one
handler per agent entry. `root` is a stable slug for the *role*, not a
name, whichever template carries `root: true` in agents.yaml is what
A2A serves. The handler uses ADK's `adka2a.Executor` so the A2A path
and the in-process Facade share the same `session.Service` and
`memory.Service`.

`GET /healthz` returns a JSON snapshot: `status`, `root_name`,
`exposed_agents[]`, optional `root_build_error`.

Authentication is opt-in via `a2a.credentials.token` (literal or
`${secret:NAME}`): empty = unauthenticated, set = bearer token required
on `/api/a2a/` (constant-time check, `/healthz` stays open). See A2A.md.

## On-disk layout: `.baifo/`

```
.baifo/
├── baifo.yaml            # providers, mcps, root, spawn, theme, a2a, runtime
├── agents.yaml          # static agent templates
├── secrets.yaml         # AES-256-GCM or plaintext entries (mode-dependent)
├── skills/              # skill packages (SKILL.md + assets/scripts subtrees)
│   └── skill-creator/
│       └── SKILL.md
├── data/
│   ├── baifo.db          # SQLite: meta + sessions + session_events + facts
│   │                    #        + audit + oauth_tokens + oauth_clients
│   ├── llm-dumps/       # appears only when runtime.log_level: debug
│   └── workspaces/      # one subdir per live worker; cleaned on collect/shutdown
└── logs/
```

**Resolution order** (`internal/config/discover.go`):

1. `--config-dir <path>` flag.
2. `$BAIFO_HOME` environment variable.
3. `$PWD/.baifo/`, walking up.
4. `$XDG_CONFIG_HOME/baifo/` (Linux/macOS) or `%APPDATA%\baifo\` (Windows).
5. `$HOME/.baifo/` as final fallback.

First hit wins; baifo does **not** merge across locations.

## TUI design

See `TUI_DESIGN.md` for the full spec. Headline facts:

- **Framework**: `charm.land/bubbletea/v2` + `bubbles/v2` + `lipgloss/v2`.
- **Layout**: top tab strip · main area · bottom status bar · floating
  toasts · modal overlays (editor, secret prompt).
- **Tabs**: `Chat`, `Workers overlay`, `Sessions`. Order is fixed. Skills /
  MCPs / Providers / Agents / Facts / Secrets have no overlay; each is managed
  through its own singular slash command (`/skill`, `/mcp`, ...).
- **Responsive breakpoints**: `< 80 cols` → chat only, tabs collapse;
  larger widths gain the workers sidebar.
- **Theme**: single fixed dark theme (the Canarias palette, not
  user-configurable). Palette defined in `internal/tui/theme.go`.
- **Slash commands**: prefix `/`, with autocomplete + inline help.

## Slash commands

Commands are **singular** and take sub-verbs (`add`, `edit`, `delete`, ...).
A bare command with no sub-verb prints an inline listing where that makes
sense (`/mcp`, `/skill`, ...) or opens an overlay (`/session`, `/worker`).
Each entity is managed by its own command.

| Command | What it does |
|---|---|
| `/help` (or `/?`) | Toggle the help overlay |
| `/quit` | Exit baifo |
| `/session [list\|new\|switch\|rename\|delete]` | Manage root sessions (`list` opens the overlay) |
| `/worker [list\|talk\|kill\|collect]` | List / talk to / kill / collect live workers |
| `/root` | Switch the chat back to the root agent |
| `/settings [edit\|reload]` | View/edit/reload `baifo.yaml`. All runtime prefs (auto-scroll, keep-tools-expanded, theme, A2A) live in the file; `edit` opens it, `reload` re-reads it |
| `/mcp [add\|edit\|delete\|auth\|test\|logout] [name]` | Manage MCP entries / drive OAuth |
| `/skill [add\|edit\|delete\|install] [name\|url]` | Manage SKILL.md packages |
| `/agent [add\|edit\|delete] [name]` | Manage agent templates (root + sub-agents) |
| `/provider [add\|edit\|delete] [name]` | Manage provider entries |
| `/secret [set\|delete\|encode\|decode] [name]` | Set/delete a secret; flip store mode |
| `/fact [add\|edit\|delete] [id]` | Manage long-term memory entries |

## Sub-commands (CLI)

```
baifo                          # launch TUI (default)
baifo --version
baifo --config-dir <path>      # honoured by every sub-command
baifo chat [--message <text>] [-v]   # TUI-free REPL harness; -v dumps
                                    # tool calls/results to stderr
baifo server                   # headless: HTTP daemon + worker manager
baifo secrets set <NAME> [--description <text>]
baifo secrets unset <NAME>
baifo secrets list             # names + descriptions, never values
baifo secrets rotate <NAME>
baifo secrets show-block         # prints the secrets block as agents see it
                                 # (names + descriptions only, never values)
```

The TUI is the **default**, not a sub-command. Anything not listed above
is **not implemented** (e.g. `baifo config show` /
`baifo sessions ls` / `baifo a2a card` are not implemented; use the TUI
slash commands instead).

## Module path and naming

- Module path: `github.com/achetronic/baifo`.
- Repository: `github.com/achetronic/baifo`.

The mismatch is deliberate.

## Code patterns and conventions

### Go conventions

- **Single binary**, everything under `internal/`.
- **Go 1.25+** (`iter.Seq2`, `slog`, generics).
- **Pure Go, no CGO**. Storage uses `modernc.org/sqlite` (a pure-Go
  SQLite driver).
- **UUID v4 IDs** for sessions; worker IDs are `w_<8-hex>`. Slugs are
  kebab-case for skill / agent / MCP / provider names.
- **`InstructionProvider`** is used for every prompt, never the static
  `Instruction` field, avoids ADK's `{variable}` substitution clashing
  with user prompt content.
- **The agent.Builder is the single entry point** to ADK agent
  construction (`internal/agent/builder.go`). Nothing else may call
  `llmagent.New` directly. This is how every agent uniformly gets the
  secrets pipeline + audit wired in the right order.
- **Callback order matters**: `BeforeTool = [Expand]`; `AfterTool =
  [Redact, Audit]`. Audit runs after redaction so logged values are
  always scrubbed.
- **Hot reload**: editing `baifo.yaml` triggers a debounced reload (via
  `internal/watcher`) that rebuilds the registries and the root agent.
  In-flight runs complete on their existing snapshot.
- **Worker isolation**: each worker runs in its own goroutine with a
  dedicated cancellable context. The manager owns the registry; the
  agent layer only sees opaque `worker_id`s.
- **Event bus**: per-worker channel + a global mux (`Manager.GlobalBus()`). Routing implemented in `manager.go` via `goReplay` (forwards non-`StatusChange` events) and `publishStatusChange` (routes status transitions directly). The TUI worker chat tab subscribes per-worker (`Facade.SubscribeWorker`); the TUI chips and Workers overlay consume `StatusChange` events through `App.SubscribeWorkerLifecycle`, which subscribes to the global bus. Worker terminal states are not injected back into the root's own session.

### Frontend conventions (TUI)

- **Single `tea.Model`** at the top (`internal/tui/model.go`).
  Sub-components are plain types; key handling is centralised on the
  Model and overlays receive their own update branches.
- **One `theme.go`** owns colours, spacing, glyphs. No component
  declares its own colours.
- **`lipgloss.NewStyle()` cached** at package init in `theme.go`.
- **Streaming** via `tea.Cmd`s that produce `tea.Msg` chunks; goroutines
  never write to Model state directly.
- **Self-contained overlays** live in `internal/tui/overlays/`
  (secret prompt, editor validators, skill styler). Overlay glue that
  mutates Model state lives in the root package
  (`editor_overlay.go`, `secret_overlay.go`).

### Design philosophy

> **DRY, KISS, elegant, and decoupled does not mean over-engineered.**
> When two options exist, one simple and one cleverly abstracted
> prefer the simple one. Complexity is only justified when it removes
> real duplication or real coupling, not hypothetical ones.

Concretely for baifo:

- No flow engine. The root LLM is the orchestrator.
- No plugin system for skill loaders. SKILL.md + body + optional dirs.
- No generic ACL system. Per-agent allowlists for skills/MCPs/secrets
  are enough.
- No reinvented TUI primitives. `bubbles` has them.
- No public API outside `internal/`. The CLI and the Facade are the API.

## Build commands

```bash
make build              # bin/baifo (go build -ldflags ...)
make dev                # go run ./cmd/baifo (uses .baifo/ in PWD)
make test               # go test -v -race ./...
make lint               # golangci-lint run (v1.56.2 in CI)
make clean              # rm -rf bin/ dist/
make install            # cp bin/baifo $(GOPATH)/bin/baifo
make release            # cross-compile linux/darwin/windows × amd64/arm64
```

CI: `.github/workflows/ci.yml` runs `go mod verify`, `golangci-lint`,
`make test` and a `goreleaser check` + snapshot build on Go 1.25.
Tagged releases (`v*`) trigger `.github/workflows/release.yml`; see
`RELEASES.md` in this directory for the full pipeline.

## Dependencies

- `google.golang.org/adk` v1.2.0, Agent Development Kit.
- `google.golang.org/genai` v1.40.0, GenAI SDK.
- `github.com/achetronic/adk-utils-go` v0.17.0, providers, contextguard
  plugin, memory tools, request-level reasoning mapping for the openai
  adapter (ThinkingConfig to reasoning_effort).
- `github.com/a2aproject/a2a-go` v0.3.13, A2A protocol.
- `github.com/modelcontextprotocol/go-sdk` v1.6.0, MCP client.
- `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`,
  `charm.land/lipgloss/v2`, Charm's TUI stack.
- `modernc.org/sqlite` (storage driver), `gopkg.in/yaml.v3`,
  `github.com/fsnotify/fsnotify`, `golang.org/x/crypto`,
  `golang.org/x/oauth2`, `golang.org/x/term`, `github.com/google/uuid`.

## Sibling docs in `.agents/`

- `ARCHITECTURE.md`, deeper technical design, components, lifecycles.
- `TUI_DESIGN.md`, layout, palette, components, responsive rules.
- `OVERLAY_STYLE.md`, the modal-overlay design system (chrome,
  list primitive, keycap, palette, recipe for new overlays).
- `DEPENDENCIES.md`, `charm.land/v2` vs `github.com/charmbracelet`
  policy, watchlist, what to do when something breaks.
- `WORKER_RUNTIME.md`, spawn/query/collect/kill mechanics, sandboxes.
- `SECRETS.md`, expansion/redaction pipeline, edge cases, threat model.
- `CONFIG.md`, full reference of `baifo.yaml`, `agents.yaml`,
  `secrets.yaml`.
- `A2A.md`, A2A exposure model, agent card, auth.
- `RELEASES.md`, goreleaser pipeline, artefacts, secrets, the
  human bits of cutting a release.
- `DECISIONS.md`, numbered architecture decisions with rationale.
- `TODO.md`, work still missing in the code.
