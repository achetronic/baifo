# CONFIG.md
Complete reference for every YAML file under `.baifo/`. Single source of
truth for the loader in `internal/config/`. Every field listed exists in
the loader today.

## File index

| File | Purpose | Hot-reloadable |
|---|---|---|
| `.baifo/baifo.yaml` | Top-level config: providers, MCPs, spawn, theme, A2A, runtime, secrets policy | Yes |
| `.baifo/agents.yaml` | Root agent (`root: true`) + spawnable agent templates | Yes |
| `.baifo/secrets.yaml` | Encrypted or plaintext secret store | Re-read on demand |
| `.baifo/skills/{slug}/SKILL.md` | Skill packages | Yes (always read from disk) |

> The root agent is defined in `agents.yaml` as the entry flagged
> `root: true`. See the `agents.yaml` section below.

## `baifo.yaml`

Full annotated example, every field shown with its default. Required
fields are marked `(required)`.

```yaml
# Schema version, used by future migrations. Always 1 today.
version: 1

# Encryption key for secrets at rest. Empty disables AES mode (the store
# falls back to plaintext mode: each value is plain:v1:<base64>).
# Recommended: source from environment, e.g. ${BAIFO_ENCRYPTION_KEY}.
encryption_key: ""

# Runtime
runtime:
  log_level: info          # debug | info | warn | error
  log_format: console      # console | json
  log_file: ""             # optional path; empty logs to stderr
  redact_logs: true        # apply secret redactor to every log record
  auto_resume_session: true # if true, the last root session is restored on boot
  chat_auto_scroll: true   # if true, the chat view pins to the bottom on new events
  chat_keep_tools_expanded: false # if true, expanded tool rows remain open when navigating away
  retry:                   # how LLM provider API calls are retried on failure
    enabled: false         # default off; providing any other field implies on
    max_attempts: 4        # TOTAL tries incl. the first (1 = no retry)
    strategy: backoff      # backoff (exponential) | retry-after (honour 429s)
    backoff:
      initial: 1s          # wait before the first retry
      max: 30s             # cap a single wait so growth stays bounded
      multiplier: 2.0      # exponential growth factor between retries
      jitter: true         # randomise each wait in [delay/2, delay]

guardrails:
  trim_oversized_user_text:
    enabled: false           # OPT-IN: default false when absent
    max_chars: 30000         # per-part cap; 0 = default (30000)

# Theme
theme:
  nerd_fonts: true         # set to false if your terminal font has no Nerd Font
                           # glyphs; they degrade to ASCII. No code default (false
                           # when omitted); set true explicitly when your font supports it.

# A2A server
a2a:
  enabled: false           # default false. When true, A2A is hosted by both
                           # `baifo server` and the interactive TUI on boot.
  host: 127.0.0.1          # bind address; 0.0.0.0 exposes beyond localhost
  port: 7777               # default 7777
  public_url: ""           # advertised in agent cards; defaults to http://host:port
  credentials:
    token: ""              # empty = no auth. Set (literal or ${secret:NAME})
                           # to require "Authorization: Bearer <token>" on
                           # every /api/a2a request. /healthz stays open.

# Providers
# LLM provider definitions. Reference by `name` from root.llm and agent specs.
providers:
  - name: anthropic-main           # (required) unique slug
    type: anthropic                # (required) anthropic | openai | gemini
    url: ""                        # optional; defaults per type
    auth: api_key                  # api_key (default) | oauth. "oauth" is anthropic-only:
                                   # run `baifo provider auth <name>` once (PKCE browser
                                   # flow); tokens persist per provider name and refresh
                                   # transparently, and api_key is then ignored.
    api_key: ${secret:ANTHROPIC_API_KEY}  # supports secret expansion at config-load time
    headers: {}                    # optional extra headers (also secret-expandable)
    streaming: true                # SSE streaming; set false only for OpenAI-compatible
                                   # endpoints that do not implement SSE

  - name: openai-main
    type: openai
    api_key: ${secret:OPENAI_API_KEY}

  - name: local-ollama
    type: openai
    url: http://localhost:11434/v1

  - name: openrouter
    type: openai
    url: https://openrouter.ai/api/v1
    api_key: ${secret:OPENROUTER_API_KEY}

# MCPs
# Built-in MCPs (filesystem, browse) come pre-configured but you must
# declare them here to register them.
mcps:
  - name: filesystem               # (required) unique slug
    type: builtin                  # builtin | http | stdio
    builtin: filesystem            # required when type=builtin; one of: filesystem | browse
    # options: per-entry tuning for builtin MCPs. Flat by design: option
    # keys are prefixed by category (limit_*) so the block stays one level
    # deep and easy to autocomplete. Future non-limit options (e.g. browse's
    # download_dir) will live as siblings here. Only meaningful for
    # type=builtin; each builtin reads the keys it knows and ignores others.
    options:
      # limit_exec_output_chars: caps stdout and stderr (each, independently)
      # returned by the exec and process_status tools, so a single exec of
      # `cat /large-binary` cannot flood the agent's context window.
      # The effective cap is advertised in the tool descriptions so the model
      # knows to use pipes or ranges.
      #   0 / absent = default (48000 chars)
      #   positive   = explicit cap in characters
      #   -1         = unlimited (no cap)
      limit_exec_output_chars: 48000

      # limit_read_file_chars: caps the total characters returned by a single
      # read_file call, counted across all its fragments' numbered lines.
      # When the budget is exhausted, remaining lines / ranges are dropped;
      # the result carries truncated=true and a truncation_note pointing
      # the model to use offset+limit ranges for the rest.
      #   0 / absent = default (120000 chars)
      #   positive   = explicit cap in characters
      #   -1         = unlimited (no cap)
      limit_read_file_chars: 120000

      # limit_search_output_chars: caps the total characters returned by a
      # single search call, summed across all matches and their context
      # lines. Independent of max_results (which bounds the match COUNT):
      # this bounds the match SIZE, so a broad pattern over long lines
      # cannot flood the context even with few matches. When the budget is
      # exhausted, remaining matches are dropped at whole-match granularity
      # and the result carries truncated=true with a truncation_note.
      #   0 / absent = default (50000 chars)
      #   positive   = explicit cap in characters
      #   -1         = unlimited (no cap)
      limit_search_output_chars: 50000

  - name: browse
    type: builtin
    builtin: browse

  - name: github
    type: http
    endpoint: https://api.githubcopilot.com/mcp
    headers:
      Authorization: "Bearer ${secret:GITHUB_TOKEN}"
    insecure: false                # skip TLS verification
    auth:
      kind: none                   # none | oauth

  - name: github-oauth
    type: http
    endpoint: https://api.githubcopilot.com/mcp
    auth:
      kind: oauth
      # Leave both empty to opt into discovery: baifo uses Dynamic
      # Client Registration (RFC 7591) and/or CIMD (Client ID Metadata
      # Document), per the registration field below. Set them to use a
      # pre-registered client instead.
      client_id: ""
      client_secret_ref: ""          # name of a secrets.yaml entry, never the value
      registration: auto             # auto (default) | cimd | dcr. Forces the
                                     # client-registration method when no
                                     # client_id is set. Use "dcr" when the IdP
                                     # supports CIMD but rejects our client_id
                                     # URL (domain not whitelisted).
      # NOTE: authorize_url, token_url, scopes and redirect_url are NOT
      # configurable. Endpoints come from authorization-server metadata
      # and scopes from the protected-resource metadata (RFC 9728),
      # both discovered at /mcp auth time.

  - name: kubernetes
    type: stdio
    command: kubernetes-mcp
    args: ["--kubeconfig", "~/.kube/config"]
    env: {}
    workdir: ""

# Spawn settings
spawn:
  mode: both                       # none | static | dynamic | both
  collect_timeout: 5m              # default timeout for collect_agent

# Secrets (redaction policy)
secrets:
  redact_in_logs: true             # scrub stored secret values from logs
  redact_in_tui: true              # redactor runs before painting tool args/results

  # Comprehensive AfterToolCallback pass that scans every tool result
  # for raw secret values, even those the model never asked for.
  # Defends against tools that leak values they were not given (an
  # MCP echoing a debug header, a file that contains a stored secret,
  # exec output, etc.). Default true; disable only for debugging when
  # you need raw tool output and accept the leak risk.
  scrub_tool_results: true

  # Minimum byte length a stored value must have to be eligible for
  # the comprehensive pass. The trade-off is DIRECT:
  #
  #   HIGHER values → FEWER false positives, MORE short secrets
  #                   bypassed by the comprehensive pass.
  #   LOWER values  → MORE values protected, MORE chance of redacting
  #                   unrelated substrings inside legitimate tool
  #                   output.
  #
  # Reference points:
  #   - 8 (default): protects every real API key (always 16+ bytes);
  #                  short PINs and 4-7 byte passwords escape the
  #                  comprehensive pass. They remain protected by the
  #                  targeted pass when the model explicitly writes
  #                  ${secret:NAME}.
  #   - 12: filters out short personal secrets; covers most modern
  #         API key formats (ghp_*, sk-*, AKIA…).
  #   - 16: only long, high-entropy values are scanned.
  #
  # 0 or negative disables the floor entirely; not recommended.
  min_scrub_length: 8
```

> **Not in `baifo.yaml`.** The root agent is defined in `agents.yaml`
> (entry flagged `root: true`), not here. There is no `meta_tools:`
> block, no `output_dir` / `meta_tools_enabled` scalars, and no
> `internal/tools/meta` package.

### Field-by-field notes

- `version`, bumped only on breaking loader changes. Migrations are centralised in `internal/storage/migrate.go` (see ARCHITECTURE.md, Storage layer).
- `encryption_key`, supports `${VAR}` env expansion. When empty, the
  secrets store runs in **plaintext mode** (each value is
  `plain:v1:<base64>`). `/secret encode` and `/secret decode` flip the
  mode once a key is configured.
- `runtime.log_format`, `console` (default) or `json`.
- `runtime.auto_resume_session`, when `true`, boot resumes the
  most-recently-used session; when `false`, every boot creates a fresh
  one. Default `true`.
- `runtime.chat_auto_scroll`, controls whether new events pin the
  chat viewport to the bottom.
- `runtime.chat_keep_tools_expanded`, controls whether expanded tool
  rows remain open when navigating away. Default `false`.
- `runtime.retry`, exponential-backoff retry policy for failing LLM
  provider API calls (rate limits, overloads, transient network/transport
  errors). The block is **off** when absent; supplying any field turns it
  on (or set `enabled` explicitly). Fields:
  - `max_attempts`, total tries including the first; `1` disables retry.
    Default `4`.
  - `strategy`, `backoff` (default) always uses the exponential backoff
    below; `retry-after` honours the provider's `Retry-After` header when
    the failure carries one (e.g. a 429 rate limit) and falls back to the
    backoff when it does not.
  - `backoff.initial`, wait before the first retry (Go duration string,
    e.g. `1s`, `500ms`). Default `1s`.
  - `backoff.max`, upper bound on a single wait. Default `30s`.
  - `backoff.multiplier`, growth factor applied each retry (`delay *=
    multiplier`, capped at `backoff.max`). Values `<= 1` fall back to the
    default `2.0`.
  - `backoff.jitter`, randomise each wait in `[delay/2, delay]` to avoid
    retry storms across concurrent workers. Default `true`.
  Retries are skipped once a turn has already streamed real content (a
  mid-stream failure is surfaced as-is, since retrying would duplicate
  output). With the defaults a flaky provider waits roughly 1s, 2s, 4s
  before the turn finally fails.

### Guardrails

The `guardrails:` block (top-level, sibling of `runtime:`) groups the
optional, per-request protections baifo can apply around every model call.
Each guardrail **names the threat it mitigates**, has its own `enabled`
flag, and its own tuning knobs. Disabling one guardrail never affects the
others.

Guardrails are **OPT-IN** (default `false` when absent). Rationale: a
guardrail may alter what the user's own content looks like to the model,
and that must never happen silently on a fresh install. The always-on
protection layer is the producer-side one instead (`mcps[].options`
`limit_*` caps), which only bounds tool outputs — data the model knows
how to re-fetch in pieces.

#### `guardrails.trim_oversized_user_text`

Truncates oversized **user-role** text parts in the `LLMRequest` before
each model call. "User-role text" covers more than what the human types:
when a session is resumed under a different root-agent name the ADK's
`ConvertForeignEvent` injects the **entire old transcript** — including
unbounded tool results — as user-role text prefixed with
`"For context: [agent] \`exec\` tool returned result: ..."`. Megabytes of
binary data can enter the parent's context window and instantly trigger
context-guard compaction, which distorts the token accounting the guard
relies on. **Model turns and native tool-result blocks are never touched.**

- `enabled`, default `false` when absent — guardrails are opt-in. Set
  `true` to activate. Pointer-bool (`*bool`) so a future migration can
  distinguish an explicit `false` from an absent block.
- `max_chars`, per-part character cap. `0` / absent = default (`30000`).
  Positive = explicit cap. A value of `0` does **not** disable — it
  selects the default cap; the `enabled` flag owns activation.
- The trim is **ephemeral per-request**: the `LLMRequest` is rebuilt from
  session events each turn, so the stored session keeps full fidelity.
- Runs **before** the context-guard plugin so the guard's `BeforeModel`
  token counting sees the already-trimmed view.

#### RESERVED: `guardrails.cap_external_tool_output`

**Not implemented yet.** Future guardrail that will cap tool outputs from
external (HTTP / stdio) MCPs, which have no producer-side output caps unlike
the built-in `filesystem` MCP. Do not set this key; the loader ignores
unknown YAML fields.

- `theme.nerd_fonts`, when `false`, glyphs degrade to ASCII. The colour
  palette is fixed (Canarias) and has no config knob; see `TUI_DESIGN.md`.
- `a2a.enabled`, default `false`. When `true`, A2A is hosted by both
  `baifo server` (headless) and the interactive TUI (in-process, on
  boot). `baifo server` refuses to start when it is `false`.
- `a2a.port`, default `7777` (set by `defaultA2APort` in config.go). The
  repo's own `config/baifo.yaml` overrides this to `8090`; that is a local
  choice, not the default. A fresh install with no explicit `port:` gets `7777`.
- `providers[].type`, one of `anthropic`, `openai`, `gemini`. Any
  endpoint that speaks the OpenAI API (LocalAI, OpenRouter, vLLM, a
  local Ollama server) is declared as `type: openai` with `url` set;
  the openai adapter serves all of them. There is no dedicated
  `openai-compatible` or `ollama` type. The `url` is the marker of a
  custom endpoint (see `list_models` below).
- `list_models` and the agent editor's `model:` autocomplete resolve a
  provider against the catwalk catalogue via `internal/modelcatalog`.
  Resolution order: empty `url` matches by type (`anthropic`, `openai`,
  `gemini`); a non-empty `url` matches catwalk's `APIEndpoint` index,
  first by exact normalized URL, then by unique host. Canonical endpoints
  are stored as `$VAR` placeholders in catwalk and are excluded from the
  URL index, so they never match a user url by accident; ambiguous hosts
  (two catwalk products on one host) do not match either. A URL match
  returns that endpoint's real catalogue plus a note that the live
  endpoint may serve a different subset. `internal/modelcatalog/`
  `catwalk_endpoints_test.go` is a regression guard: it fails if a future
  catwalk bump empties the `APIEndpoint` of known OpenAI-compatible
  providers (openrouter, groq, deepseek, xai) or turns a canonical
  endpoint into a real URL.
- `providers[].auth`, `api_key` (default) or `oauth`. OAuth is
  implemented for `type: anthropic` only: `baifo provider auth <name>`
  resolves `<name>` against `providers[]`, runs a PKCE browser flow and
  stores access + refresh tokens in `~/.config/baifo/oauth_<name>.json`,
  refreshed transparently (`OAuthTransport` in
  `internal/providers/anthropic`). Tokens are keyed by provider name, so
  several providers of the same type (e.g. two OAuth orgs) keep separate
  credentials. With `auth: oauth` the `api_key` field is ignored.
  Setting `auth` to any mode other than `api_key` on a type with no
  registered OAuth flow (today: every type except `anthropic`) is
  rejected at registry construction with `ErrUnsupportedAuth`, so a
  `gemini` or `openai` provider with `auth: oauth` fails the boot loudly
  rather than silently falling back to `api_key`
  (`internal/providers.NewRegistry`).
- `providers[].api_key` and `providers[].headers`, both support
  `${secret:NAME}` expansion at **config-load time**
  (`internal/providers.ExpandSecrets`). This is different from the
  tool-call pipeline which expands per call.
- `providers[].streaming`, default `true`. Set `false` only for
  OpenAI-compatible endpoints that do not implement SSE; those reject
  or hang on a streaming request. Anthropic requires streaming on for
  high-reasoning turns (the API refuses non-streamed responses that may
  exceed 10 minutes).
- `mcps[].type = builtin`, only `filesystem` and `browse` accepted.
  Other slugs are rejected.
- `mcps[].type = stdio`, requires the binary on `$PATH`. Best when
  baifo is installed natively.
- `mcps[].auth.kind: oauth`, drives an OAuth flow on first use.
  Tokens persist in the SQLite `oauth_tokens` table and refresh
  transparently. The user can trigger the flow interactively via
  `/mcp auth <name>`. Authorization-server endpoints are
  discovered from server metadata; scopes from the protected-resource
  metadata (RFC 9728). Leaving `client_id` / `client_secret_ref` empty
  opts into discovery, Dynamic Client Registration (RFC 7591) and/or
  CIMD (Client ID Metadata Document), per `auth.registration` below.
  There are no `authorize_url` / `token_url` / `scopes` /
  `redirect_url` fields.
- `mcps[].auth.registration`, `auto` (default) | `cimd` | `dcr`. Selects
  the client-registration method when no `client_id` is set. In `auto` mode
  the MCP SDK prefers CIMD when the AS announces support for it, and falls
  back to DCR (RFC 7591) otherwise. `cimd` suppresses DCR. `dcr` suppresses
  CIMD, which is useful when an IdP announces CIMD but rejects our client_id
  URL. (CIMD only succeeds against a supporting AS once the brand
  document at `brandingCIMDURL` is actually served; until then use `dcr`.)
- `spawn.mode`, `none` removes spawn tools entirely. See decision #6.
  Every worker always gets its own workspace directory.
- `spawn.collect_timeout`, how long `collect_agent` waits for a worker
  to return a result before timing out. Go duration string (e.g. `5m`).
- The root's `skills: []` / `mcps: []` mean **"all visible"**
  (`internal/agent.ResolveSkills` / `ResolveMCPs`). This is the inverse
  of the per-template behaviour (where `[]` means "none"). The root's
  `allowed_secrets` is ignored: it always has access to every secret
  declared in `secrets.yaml` because it is the user-facing coordinator
  (see SECRETS.md, decision #10).
- `context_guard.strategy: threshold` (default, in `agents.yaml`), uses
  `adk-utils-go/plugin/contextguard` with a per-model context window
  from catwalk's embedded model database (`CrushRegistry`).
- `context_guard.strategy: sliding_window` (in `agents.yaml`), keeps only the last
  `max_turns` events; older content is summarised.

When `context_guard.enabled` is true on the root, the TUI footer shows a
live `guard` gauge chip (strategy + percentage toward the next
compaction) and drops a highlighted notice card into the transcript when
a compaction actually fires. The gauge is fed by
`Facade.ContextGuardStatus`, which reads the plugin's session-state
keys. See TUI_DESIGN.md (Zone 4, Footer).

## `agents.yaml`

Holds the root agent and every spawnable template under one `agents:`
list. Exactly one entry sets `root: true`. The loader rejects more than
one root (returns an error). Zero roots is allowed on disk (a fresh
install before the wizard runs); `App.buildRoot` converts that to
`ErrNoRoot` at boot. Non-root entries are static workers the
root invokes by `name` via `spawn_static_agent`.

At most one entry may set `utility: true`: the **utility agent**, a
model alias for baifo's internal chores (session titling and
context-guard compaction summaries). It never chats, gets no tools and
is not spawnable — only its `llm` block is read. It is exempt from the
prompt and llm requirements; when absent or incomplete, chores fall
back to the root's LLM. The default scaffold ships one named `utility`
with an empty model so the user can point it at something cheap. An
agent cannot be both root and utility.

```yaml
version: 1
agents:
  - name: baifo                           # (required) unique slug
    root: true                           # the always-on entry point
    description: Multidisciplinary local assistant.
    prompt: |
      You are baifo, ...
    llm:
      provider: gemini
      model: gemini-3.5-flash
    skills: []                           # root: empty = all visible
    mcps: []                             # root: empty = all visible
    context_guard:
      enabled: true
      strategy: threshold
      max_tokens: 900000
      max_turns: 20
    allowed_secrets: []                  # ignored for the root (AllowAll)

  - name: deep-researcher                # (required) unique slug
    description: |                       # shown to the root in spawn-tool docs
      Web-research specialist that follows citation chains.
    prompt: |
      You are a deep research agent. ...
    llm:
      provider: openai-main              # Provider from baifo.yaml
      model: gpt-5                       # for backwards-compatibility only.
      reasoning: medium                  # optional: minimal|low|medium|high
                                         # (empty/off = model default). Only for
                                         # models whose list_models entry reports
                                         # reasoning_levels.
      reasoning_api: ""                  # optional, anthropic-only: enabled (classic
                                         # budget-based thinking, Claude 3.7/Sonnet 4/
                                         # Opus 4) | adaptive (effort-based, Opus 4.5+).
                                         # Empty = auto-detect from the catalogue.
    skills: [web-research]               # subset; empty = none
    mcps: [browse]                       # subset; empty = none
    context_guard:
      enabled: true
      strategy: sliding_window
      max_turns: 20
    allowed_secrets: []                  # empty = no secret may be dereferenced
```

The full `AgentTemplate` schema is: `root` (bool), `utility` (bool),
`name`, `description`,
`prompt`, `llm` (`provider` + `model` + optional `reasoning` +
optional `reasoning_api`),
`skills`, `mcps`, `context_guard`, `allowed_secrets`. There is **no**
`sandbox:` block and **no** `output_schema:` field; `workers.Spec`
carries neither.

`llm.reasoning` accepts `minimal | low | medium | high` (empty, `off`, `none`, or `disabled` =
the model's default). It is validated at load time and applied per provider:
openai maps it to `reasoning_effort`, gemini to its native thinking config
(both request-level), and anthropic to an extended-thinking token budget
(construction-level). Setting it on a model that does not support reasoning
will make the provider API reject the call, check `list_models` first.

Only these four levels are accepted: catwalk lists extra ones (`xhigh`,
`max`) for some models but baifo cannot deliver them through
`genai.ThinkingLevel`, so they are not offered. For an `openai` provider
pointed at a custom endpoint the value is still sent as `reasoning_effort`, but whether the
endpoint honours it (vs ignoring or erroring) is up to that endpoint, baifo
has no catalogue for it. The agent editor autocompletes
`reasoning:` based on the model on the `model:` line above it (only the
levels that model supports; just `off` for non-reasoning models).

`llm.reasoning_api` is anthropic-only and selects which extended-thinking
API the request uses: `enabled` (classic token-budget thinking — Claude 3.7,
Sonnet 4, Opus 4) or `adaptive` (effort-based — Opus 4.5 and newer). Empty
auto-detects from the model catalogue, which is the right choice unless a
brand-new model is missing from the catalogue. Validated at load time;
other providers ignore the field.

Per-template defaults differ from the root:

- `skills` and `mcps`: empty list means **"none"** for templates,
  **"all"** for the root. Least-privilege for sub-agents,
  discoverability for the user-facing root.
- `allowed_secrets`: empty list (or field omitted) means **"none"**
  for templates and for dynamic spawns. The root has no
  `allowed_secrets` field at all, it is unconditionally
  `AllowAll` (see SECRETS.md). A spawn call may narrow the
  template's allowlist via `args.allowed_secrets` on
  `spawn_static_agent`, but it cannot exceed it; the override is
  validated as a subset of the template (and of the spawning
  agent's own allowlist).

## `secrets.yaml`

Managed via `/secret set|delete|encode|decode` in the TUI and via
`baifo secrets {set|unset|list|rotate|show-block}` on the CLI. Direct
editing is supported but discouraged. The store has two modes:

```yaml
# encrypted mode (encryption_key is set)
version: 1
encrypted: true
secrets:
  ANTHROPIC_API_KEY:
    description: "API key for Anthropic"
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-02T00:00:00Z
    value: "AES256GCM:v1:<base64 nonce>:<base64 ciphertext>"
```

```yaml
# plaintext mode (no encryption_key)
version: 1
encrypted: false
secrets:
  ANTHROPIC_API_KEY:
    description: "API key for Anthropic"
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-01T00:00:00Z
    value: "plain:v1:<base64>"
```

`/secret encode` re-seals every plaintext entry once an
`encryption_key` is configured; `/secret decode` does the inverse.

See `SECRETS.md` for the full pipeline.

## Skills

Each skill is a directory under `.baifo/skills/{slug}/`:

```
.baifo/skills/skill-creator/
├── SKILL.md
├── references/
│   └── examples.md
├── assets/
│   └── template.md
└── scripts/
    └── post-install.sh
```

`SKILL.md` is markdown with a YAML frontmatter:

```markdown
---
name: skill-creator
description: |
  Create new skills, modify and improve existing skills, and measure
  skill performance. Use when users want to create a skill from scratch.
version: 1
---

# Skill creator

Body of the skill in markdown. Loaded by the agent as additional
context when the skill is active.
```

- Required keys: `name`, `description`.
- Tolerant parser: extra keys are kept verbatim in `Skill.Extra`.
- `references/`, `assets/`, `scripts/` are optional and exposed to the
  agent through ADK's skill toolset.

## Resolution priority

### Config directory discovery (`internal/config/discover.go`)

The `.baifo/` directory is located in this order (first hit wins):

1. `--config-dir` flag (required to exist if given).
2. `$BAIFO_HOME` env var (required to exist if set).
3. Walk up from `$PWD` looking for a `.baifo/` child directory.
4. `$XDG_CONFIG_HOME/baifo/` (falls back to `$HOME/.config/baifo/`).
5. `$HOME/.baifo/`.

### Field-value precedence within a YAML file

When the same field can come from multiple sources:

1. Environment variable expansion (`${VAR}` inside YAML, resolved at load time).
2. Explicit value in the YAML file.
3. Built-in default (`applyDefaults` in config.go).

Secret expansion (`${secret:NAME}`) happens at two different times
depending on the field:

- **Provider `api_key` and `headers`**: expanded at **config-load time**
  (`internal/providers.ExpandSecrets`) because the underlying SDK
  caches the value.
- **Tool-call arguments**: expanded **per call** by the secrets
  BeforeToolCallback (`internal/secrets.Expand`).

The pipeline pass uses `placeholderRE = \$\{secret:([A-Za-z0-9_\-]+)\}` (in `internal/secrets/pipeline.go`). The env-expand pass at config-load time (`internal/config/env_expand.go`) uses `strings.HasPrefix(name, "secret:")` to preserve the same placeholders verbatim rather than erasing them.
