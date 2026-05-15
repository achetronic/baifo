# DECISIONS.md

Numbered architecture decisions. Each entry is irreversible without notice:
if you need to revisit, leave the old one and add a new one with the same
number suffixed by a letter (e.g. `#3a`).

This document is **descriptive of code**: items marked "**Status:**" call
out whether the decision is fully in force, partially implemented, or still
planned. When code and a decision disagree, code wins.

## #1, `.baifo/` directory discovery: local-first, then global

**Context.** baifo can be useful both as a per-project assistant (different
agents/skills/MCPs for each codebase) and as a global personal harness.

**Decision.** Configuration lives in a `.baifo/` directory resolved in this
order:

1. `--config-dir <path>` flag.
2. `$BAIFO_HOME` environment variable.
3. `$PWD/.baifo/` walking up the tree (like git).
4. `$XDG_CONFIG_HOME/baifo/` (falls back to `$HOME/.config/baifo/` when `XDG_CONFIG_HOME` is unset).
5. `$HOME/.baifo/` as final fallback.

The first hit wins; baifo never merges multiple directories.

**Status.** In force. See `internal/config/discover.go`.

**Rationale.** Matches `direnv` / `git` / `mise` mental model. Lets users
keep a global default and override per project without YAML inheritance,
which always becomes painful.

**Implication.** If none is found, baifo offers to create one in
`$HOME/.baifo/` interactively on first run (`cmd/baifo/wizard.go`).

## #2, TUI framework: Bubble Tea v2 + Bubbles + Lipgloss v2

**Decision.** Use Charm's stack:

- `charm.land/bubbletea/v2` for the event loop and `tea.Model` lifecycle.
- `charm.land/bubbles/v2` for text input, viewport, list, spinner.
- `charm.land/lipgloss/v2` for styling, layout, borders.

**Status.** In force. Everything is keyboard-driven; no mouse-zone library is used.

**Rationale.** Most mature Go TUI stack. v2 fixes the major v1 gotchas
around mouse + alt-screen.

## #3a, Storage: SQLite

**Decision.** Persistence uses **`modernc.org/sqlite`** (pure-Go SQLite
driver, no CGO). One file: `.baifo/data/baifo.db`, opened in WAL mode
with `busy_timeout=5000`. Tables (see `internal/storage/db.go`):
`meta`, `sessions`, `session_events`, `facts`, `audit`, `oauth_tokens`,
`oauth_clients`.

**Status.** In force. Migrations are centralised in `internal/storage/migrate.go`: `migrateV1` applies the baseline schema, `ensureEmbeddingColumn` handles the ad-hoc embedding backfill on existing v1 databases, and `setVersion` stamps `meta.schema_version`. Future migrations slot in as sequential `if version < N` blocks.

## #4, No flows, no transfer agents

**Decision.** baifo does **not** implement ADK flows (sequential / parallel
/ loop / nested). It does **not** use ADK's `transfer_to_agent` or
`AgentTool` as the primary mechanism for sub-agents.

**Status.** In force.

**Rationale.** Flows are a precondition for orchestrating multiple agents
only when the orchestration is **predefined and visual**. In baifo
orchestration is decided at runtime by the root LLM through
spawn/query/collect tools that are richer than `AgentTool` (async,
supervisable, killable, queryable).

**Implication.** "Parallel agents now" is solved by the root issuing
multiple `spawn_dynamic_agent` (or `spawn_dynamic_agents`) calls in one
turn. "Sequential agents" is solved by sequential spawn/collect cycles.
The cost is that the LLM, not the engine, owns the control flow.

## #5, Worker manager owns the lifecycle, not ADK

**Decision.** ADK provides per-worker building blocks (`Runner`,
`Session`, tool callbacks). baifo provides a **Worker Manager** on top
that:

- Holds the registry of live workers (map `worker_id → *Worker`).
- Owns each worker's cancellable `context.Context`.
- Exposes an event channel per worker so the TUI can subscribe.
- Implements `spawn`, `query`, `inspect`, `list`, `collect`, `kill`.

**Status.** In force. Implementation in `internal/workers/`.

**Rationale.** ADK's `AgentTool` is synchronous and stateless. We need
asynchronous, supervisable, queryable workers.

**Implication.** Worker sessions live **in-memory only**
(`session.InMemoryService()` inside `adk_driver.go`). On baifo restart,
dynamic workers are gone (by design); static workers are not re-spawned
automatically. Persisting worker sessions is **not implemented** and is
not on the short-term roadmap.

## #6, Spawn modes: `none | static | dynamic | both`

**Decision.** A single config knob `spawn.mode` controls which spawn tools
are exposed to the root agent:

- `none`, root has no spawn tools at all (single-agent harness).
- `static`, `spawn_static_agent`, `query_agent`, `list_agents`,
  `list_running_agents`, `inspect_agent`, `collect_agent`, `kill_agent`.
- `dynamic`, `spawn_dynamic_agent`, `spawn_dynamic_agents`, plus the
  supervision tools.
- `both`, full set. **Default.**

**Status.** In force. See `internal/config/config.go::SpawnConfig`.

**Rationale.** Lets users dial down complexity for predictable behaviour.

## #7, Dynamic workers can only use pieces baifo already knows

**Decision.** When spawning dynamic workers, the root agent can only
reference:

- Skills already loaded in `.baifo/skills/`.
- MCPs already declared in `baifo.yaml`.
- Providers already declared in `baifo.yaml`.
- Secrets already present in `secrets.yaml` (referenced by name).

The root cannot fabricate a new skill body, MCP endpoint or provider URL
inside a spawn call.

**Status.** In force at the spawn-validator level
(`internal/tools/spawn/dynamic.go::buildDynamicSpec`). **The validator
checks names against the live universe, not against the parent agent's
allowlist**, see also #10.

**Rationale.** Safety. The operator always knows the universe of
capabilities; dynamic spawning becomes "compose from known pieces".

**Implication.** To extend the universe, the root must use **meta-tools**
(see #8) if implemented, which write back to `baifo.yaml` / `agents.yaml`
/ `.baifo/skills/`.

## #8, Config mutation is not exposed as agent tools

**Decision.** The root does not get tools that rewrite baifo's own
configuration (creating agents, skills, providers, attaching MCPs,
...). There is no `meta_tools:` config block and no `internal/tools/meta`
package.

**Rationale.** Such tools are powerful enough that an unsupervised LLM
could wreck the configuration, and they are not needed: the root
already has shell + filesystem access (`exec`, `write_file`,
`edit_file`) and the user has the per-entity slash commands (`/agent`,
`/mcp`, `/skill`, ...) plus direct YAML edits. Adding a dedicated,
gated meta-tool surface was judged unnecessary complexity.

## #9, Worker filesystem workspaces

**Decision.** Every worker is allocated one dedicated workspace
directory at `<data_dir>/workspaces/{worker_id}/` on spawn, removed
unconditionally on collect/kill. There are no per-worker sandbox modes:
the allocation is uniform and neither `workers.Spec` nor `AgentTemplate`
carries a `sandbox` field.

**Status.** There is no filesystem-MCP sandbox. The allocator hands each
worker a per-worker directory as a convenient default working dir, but the
filesystem builtin deliberately does not clamp tools to that root: agents
get full host filesystem access by design. This is a conscious decision,
not a gap. The workspace dir is hygiene (somewhere to put scratch files),
not a security boundary.

## #10, Secrets: `${secret:name}` + Before/After tool callbacks

**Decision.** Secrets follow this pipeline:

1. Stored in `.baifo/secrets.yaml`. Two modes: AES-256-GCM (when
   `encryption_key` is set; PBKDF2 with salt `baifo-secrets-v1`, 200k
   iters) or plaintext (when no key). Each entry has
   `description`, `created_at`, `rotated_at`, `value`.
2. Agents see names + descriptions of allowed secrets in their tool
   catalogue. **Raw values never enter the prompt.**
3. The model emits `${secret:NAME}` literally inside tool-call arguments.
4. A `BeforeToolCallback` walks the args (recursive) and expands every
   placeholder to the raw value.
5. An `AfterToolCallback` chain runs **redactor first, then audit**, so
   raw values are scrubbed before being audited or returned to the
   model.
6. The TUI log sink also redacts when `secrets.redact_in_logs: true`
   (default).

**Status, fully in force**:

- Pipeline expansion + redaction (`internal/secrets/pipeline.go`,
  wired in `internal/agent/builder.go`).
- Both encrypted and plaintext modes (`internal/secrets/store.go`).
- `/secret encode|decode` slash commands to flip the mode.
- **Slice semantics on `Spec.AllowedSecrets`**: nil/empty →
  `AllowNone{}`, non-empty → `AllowList`. The root bypasses this
  helper via `Spec.UnrestrictedSecrets = true` and gets `AllowAll{}`.
  This is the structural reason baifo.yaml has no
  `root.allowed_secrets` field: restricting the root would force the
  operator to maintain a redundant list every time they add a secret.
- **Subset enforcement on spawn**. Static and dynamic spawn each
  enforce that a sub-agent cannot grant secrets the spawning agent
  itself does not hold. Implemented at
  `internal/tools/spawn/spawn.go::resolveStaticAllowedSecrets` and
  the parent check inside
  `internal/tools/spawn/dynamic.go::buildDynamicSpec`. The
  spawn-tool struct holds the parent's allowlist on
  `spawntools.Tools.ParentAllowedSecrets` (nil = "sovereign"; today
  always nil because only the root has spawn tools).
- **Opaque spawn-tool args.** The BeforeToolCallback skips the
  expander entirely for tool names listed in
  `internal/tools/spawn.OpaqueToolNames()` (`spawn_static_agent`,
  `spawn_dynamic_agent`, `spawn_dynamic_agents`). Spawn args carry
  the child agent's spec, prompt, initial message, allowed
  secrets, sandbox, so eagerly substituting `${secret:NAME}` in
  the parent's expander would bake the raw value into the child's
  prompt and bypass the child's own allowlist. With the guard, the
  placeholder reaches the child untouched; the child's own
  BeforeToolCallback decides at its own tool boundary. This is
  what makes the subset-check above load-bearing: a child cannot
  see a raw secret value the parent expanded. Wired through
  `agent.Builder.OpaqueTools`, populated from
  `spawntools.OpaqueToolNames()` in both `App.buildRoot` and
  `App.buildWorkerAgent`.
- **Comprehensive AfterToolCallback redactor.** The targeted pass
  (scrub the values the BeforeToolCallback just substituted in this
  call) is complemented by a second pass that snapshots every secret
  in the store and scrubs ANY value that appears in the result. This
  closes the "tools that emit secrets they were not given" gap
  (debug pages echoing tokens, files that happen to contain a
  stored value, etc.). Two knobs in `baifo.yaml::secrets`:
  `scrub_tool_results` (default `true`) toggles the pass;
  `min_scrub_length` (default `8`) is the byte floor below which a
  stored value is excluded to avoid false positives on common
  short substrings. Short secrets remain protected by the targeted
  pass when the LLM references them explicitly via `${secret:NAME}`.

**Threat model.** A malicious or buggy tool can still write a raw
secret to disk if the model crafts an argument to do exactly that.
Mitigation lives at the filesystem MCP / sandbox layer, not here.

## #11a, Fixed Canarias theme, no user-selectable accent

**Decision.** The palette is **fixed and not user-configurable**. The
`theme.accent` config key is **removed** (no longer in `ThemeConfig`,
no default, not parsed). `internal/tui` exposes a single `canariasAccent`
and `NewTheme(useNerdFont bool)` takes no accent argument.

The only remaining `theme` knob is `theme.nerd_fonts`, which is a
terminal capability (glyphs vs ASCII), not an aesthetic choice.

**Status.** In force. See `internal/tui/theme.go`, `internal/config`
(`ThemeConfig`), and the swatch board at `docs/canarias-palette.html`.

## #12, A2A: root only in v1

**Decision.** The A2A server exposes **only the root agent** in v1.

**Status.** In force as a policy. The **implementation is multi-agent
capable**: `server.Core.Agents()` returns a slice; today the slice
contains only the root, but adding static or dynamic exposure is a
matter of populating that slice. URL pattern is `/api/a2a/<agent-id>`;
the root lives at `/api/a2a/root`.

**Rationale.** The Worker Manager keeps workers alive only while baifo
is interactive; we don't yet have a "warm pool of pre-spawned statics
ready to answer A2A calls".

## #13, Authentication for A2A: optional bearer token

**Decision.** The A2A daemon binds `127.0.0.1:7777` by default and is
unauthenticated unless `a2a.credentials.token` is set. When a token is
configured (a literal string or a `${secret:NAME}` reference resolved
at boot), every request under `/api/a2a/` must carry
`Authorization: Bearer <token>`; `/healthz` stays open.

**Status.** Implemented. `withBearerAuth` in `internal/server/auth.go`
(constant-time compare; missing-vs-invalid distinguished per RFC 6750).

## #14, Skills are on-disk packages, loaded live

**Decision.**

- Each skill lives at `.baifo/skills/{slug}/SKILL.md` with optional
  `references/`, `assets/`, `scripts/`.
- baifo keeps no DB cache; the loader reads SKILL.md on every access
  (`internal/skills/loader.go`).
- Per-agent allowlist; the skill toolset only exposes skills declared in
  `agent.skills` (empty list = all skills).
- Tolerant frontmatter parser: non-canonical keys are accepted and
  stored in `Extra`.

**Status.** In force.

## #15, MCPs: `filesystem` and `browse` built-in

**Decision.** Two MCPs ship as in-process implementations:

- `builtin: filesystem`, `system_info`, `ls`, `read_file`, `write_file`,
  `edit_file`, `search`, `diff`, `exec`, `process_status`, `process_kill`,
  `scratch`, `undo`.
- `builtin: browse`, `web_fetch`, `web_search`.

They appear in `baifo.yaml` with `type: builtin` so the user knows they're
not subprocesses.

**Status.** In force.

## #16, Hot reload of config without restart

**Decision.** Editing `baifo.yaml` (via `/settings edit` or externally)
triggers a watcher (`internal/watcher/`, fsnotify with 250 ms debounce)
that:

- Re-reads `baifo.yaml`, `agents.yaml`.
- Rebuilds provider / MCP / agent-template registries.
- Rebuilds the root agent's toolset.
- Publishes a `ReloadEvent` on `Facade.SubscribeReload()`.

Live workers are **not** rebuilt, they keep their snapshot until they
finish.

**Status.** In force.

## #17, Logging with mandatory redaction

**Decision.** All logs go through `internal/logging`, which wraps `slog`
with a redaction sink. Every log record passes through the secrets
redactor when `secrets.redact_in_logs: true` (default).

**Status.** In force.

## #18, Public contract: the `Facade` interface

**Decision.** The contract every client of baifo depends on lives in
`internal/facade.Facade`. All DTOs the contract exchanges
(`Event`, `WorkerInfo`, `SessionInfo`, `SkillDetail`, `MCPDetail`,
`AgentDetail`, `ProviderDetail`, `FactDetail`, `ToolCallInfo`,
`ToolResultInfo`, `WorkerStreamEvent`, `ReloadEvent`) live in the same
package.

The concrete implementation is `internal/app.App`. Clients import only
`internal/facade`; they do not import `internal/app`.

**Status.** In force.

**Rationale.** Without this split, the TUI imported `internal/app`
and inherited its 15-package transitive boot graph (storage, mcps,
providers, workers, ...). Tests of the TUI had to stub or boot the full
core. With the split, TUI tests build minimal stub Facades.

**Implication.** Adding a new Facade method is a two-step process: add
the method on the interface (`internal/facade/facade.go`) and implement
it on `*App`. The compiler enforces both.

## #19, HTTP daemon depends on `server.Core`, not on `*app.App`

**Decision.** `internal/server` declares a small `Core` interface
(`Agents() []a2a.AgentEntry`, `SessionService()`, `MemoryService()`,
`RootName()`, `RootBuildError()`) and constructs `Server` from it.
`internal/app.App` implements that interface implicitly via
`internal/app/server_view.go`.

**Status.** In force.

**Rationale.** The HTTP daemon should be able to live in a different
process or even a different binary from the core some day. Decoupling
through a narrow interface costs nothing today and makes that future
move trivial.

## #20, Tools live in `internal/tools/`

**Decision.** All tool sources the root agent receives live under
`internal/tools/`:

- `spawn/`, spawn / supervise / list / inspect / collect / kill.
- `todos/`, per-session checklist that survives context compaction.
- `memory/`, wrapper over `adk-utils-go/tools/memory`.
- `models/`, `list_models`: catwalk-backed model catalogue per
  configured provider (root-only, always wired).
- `skills/`, wrapper over ADK's `skilltoolset`.

(There is no `meta/` package; meta-tools were never implemented, see #8.)

`internal/agent/` is reserved for **agent construction** (`builder.go`,
`callbacks.go`, `instruction.go`, `contextguard.go`, `resolve.go`).
Tools are a peer concept, not a sub-component of agent construction.

**Status.** In force.

**Rationale.** Tool packages are produced independently and **composed** by the root, exactly like MCPs.

**Note.** MCPs still live in `internal/mcps/` (not under
`internal/tools/`) because they have their own lifecycle
(auth, token store, connection management) that pure tools don't have.
The mental model is: `internal/tools/` produces `[]tool.Tool`;
`internal/mcps/` is a tool source with lifecycle.

## #21, Per-session TODO list via `session.State["todos"]`

**Decision.** baifo does not implement its own TODO storage. The
`internal/tools/todos` toolset (`todos_list`, `todos_write`,
`todos_update`, `todos_clear`) reads and writes the ADK session-state
key literally named `"todos"`.

That same key is **already inspected** by `adk-utils-go`'s contextguard
plugin during summarisation: it forwards the items into the summariser
prompt and instructs the resuming model to restore them. The net
effect is that a multi-step plan written by the model survives context
compaction without baifo-side bookkeeping.

**Status.** In force. The SQLite session service merges
`evt.Actions.StateDelta` into persisted state on `AppendEvent`
(`internal/sessions/`), so todos also survive process restarts.

**Rationale.** Reuse a hook already designed by the upstream plugin
rather than inventing a parallel store.

**Item shape**:

```go
type TodoItem struct {
    Content    string `json:"content"`
    Status     string `json:"status"`      // pending | in_progress | completed
    ActiveForm string `json:"active_form,omitempty"`
}
```

## #22, Provider naming: `providers`, not `backends`

**Decision.** The top-level config block is `providers:`, the agent
spec field is `llm.provider`, the registry package is
`internal/providers/`. The legacy `backend:` / `backends:` vocabulary is dropped.

**Status.** In force. The loader strictly requires `provider`; `backend:` / `backends:` are not accepted.

## #23, Dynamic-spawn tool description is composed from the live universe

**Decision.** The description string of `spawn_dynamic_agent` is **not
hard-coded**. It is rebuilt every time the root agent is built by
inspecting the live universe and enumerating:

- Every skill with its frontmatter description.
- Every MCP with **the tool names it exposes** (so the model sees that
  `filesystem` ships with `exec`, not just `read_file` / `write_file`).
- Every provider name.
- Every secret with its description.

Sandbox modes are documented inline.

**Status.** In force. See
`internal/tools/spawn/dynamic.go::composeDynamicDescription`.

**Rationale.** Without this, the model hallucinates the universe.

## #24, Autocomplete Taxonomy and UX: Views vs. Commands

**Decision.** The TUI slash-command autocomplete popup has a clear semantic and visual distinction between full-screen visual overlays (Views) and immediate command shortcuts/metacommands (Commands):

1. **Top-Level Sorting**: The command list is alphabetical: `agent`, `fact`, `mcp`, `provider`, `root`, `secret`, `session`, `settings`, `skill`, `worker`. `help` and `quit` are placed at the bottom of the list, not the top.
2. **Color Coding**: 
   - **Views** (bare commands that switch a surface immediately: `/help`, `/root`) are rendered in the active **Accent color** and **Bold**.
   - **Commands/Metacommands** (everything whose bare form just prints usage and whose action lives in a sub-verb: `/session`, `/worker`, `/settings`, `/mcp`, `/skill`, `/agent`, etc., e.g. the sessions overlay opens via `/session list`, not `/session`) are rendered in **Amber** (`colorInfo`, `#C98A4B`) and **Bold**.
   - **Sub-verbs** (arguments/subcommands with no leading slash like `list`, `add`, `edit`) are rendered in **Dim Gray** (`colorTextDim`) and **Normal** weight.
3. **Double-Enter Autocomplete UX**: Hitting `Enter` or `Tab` when the suggestion list is open always autocompletes the selected command into the composer without submitting. If the accepted command is a leaf (meaning no further subcommands exist), the popup immediately closes, so the very next `Enter` submits and runs it directly.

**Status.** In force. See `internal/tui/palette.go`, `internal/tui/palette_tree.go`, and `internal/tui/model.go`.

**Rationale.** Avoids the UX friction of mixed, unsorted menus where different types of actions compete for attention. It prevents the infinite-loop completion of leaf commands where the user had to type a trailing space before hitting Enter.

## Unified A2A Engine for Root and Workers

**Context:** The root agent historically communicated via the A2A server (`adka2a`), which provides a clean, deduplicated stream of text deltas and tool calls. Sub-agents (workers), on the other hand, bypassed A2A and used the raw `runner.Runner` directly for performance reasons (zero network overhead, pure Go memory passing). 

**Decision:** We migrated the workers to also use the A2A engine (`adka2a.NewExecutor` + `a2asrv.Handler`) internally via `worker_driver.go`.

**Reasoning:**
The raw ADK `session.Event` stream is highly cumulative. On every tick, it re-emits the entire turn history (all text and tool calls generated so far). Maintaining a custom client-side `streamParser` to track previously emitted parts and manually compute text deltas proved extremely fragile and resulted in visual bugs in the TUI (duplicated tool bubbles, fragmented text). 
By running an isolated `adka2a.Executor` in-memory for each worker, we gain the robust, heavily-tested event deduplication of the Google A2A protocol for free. The root and workers now share the exact same stream interpretation logic (`eventFromA2A`), ensuring identical, glitch-free UI rendering across all chats. The slight overhead of in-memory JSON serialization is an acceptable trade-off for architectural consistency and rendering stability.
