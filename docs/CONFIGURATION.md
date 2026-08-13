# Configuration

baifo stores all of its configuration in a single directory. Three YAML files control everything: global settings (providers, MCP servers, runtime knobs), agent definitions, and the secret store. This guide walks through each piece, starting with the fastest way to get running and building up to the full field reference at the end.

> For the exhaustive field-level reference aimed at maintainers and the agent itself, see [`.agents/CONFIG.md`](../.agents/CONFIG.md).

## Where the configuration lives

### First-run wizard

When you launch `baifo` for the first time and no configuration directory exists, a wizard walks you through creating one. It first confirms the directory, then offers to set up one LLM provider (type, authentication, API key or OAuth, and model) so you land in a working chat instead of a degraded boot. You can skip the provider step and configure it later. The wizard creates:

- `baifo.yaml` (global settings, fully populated with defaults and inline comments; the provider you chose, if any, is filled in)
- `agents.yaml` (a single root agent; its model is set from the wizard, or left blank if you skipped)
- `secrets.yaml` (your API key is stored here when you provide one; otherwise an empty store)
- `data/` (the SQLite database goes here)

If you pick an OAuth provider, the wizard finishes by printing the exact `baifo provider auth <name>` command to run before starting baifo.

Existing files are never overwritten. If you later add a file that the wizard skipped, baifo picks it up on the next boot.

### Discovery order

baifo looks for its configuration directory in this order. The first match wins:

1. `--config-dir` flag (must exist if given)
2. `$BAIFO_HOME` environment variable (must exist if set)
3. Walk upward from `$PWD`, looking for a `.baifo/` child directory
4. `$XDG_CONFIG_HOME/baifo/` (falls back to `$HOME/.config/baifo/`)
5. `$HOME/.baifo/`

When the wizard creates the directory, it targets `--config-dir` if given, then `$BAIFO_HOME` if set, then `$HOME/.baifo/`.

### The three files

| File           | Purpose                                                                                               |
| -------------- | ----------------------------------------------------------------------------------------------------- |
| `baifo.yaml`   | Global knobs: providers, MCP servers, spawn settings, secrets policy, runtime, guardrails, theme, A2A |
| `agents.yaml`  | The root agent, spawnable agent templates, and the utility agent                                      |
| `secrets.yaml` | The secret store (plaintext or AES-256-GCM encrypted)                                                 |

You can edit any of these by hand in your editor, through slash commands in the TUI (`/agent`, `/provider`, `/mcp`, `/secret`, `/settings`), or with the built-in editor that ships with syntax highlighting and validation.

## The minimum to get running

Two things are needed before baifo stops showing a degraded-boot prompt:

1. Declare at least one provider in `baifo.yaml`.
2. Set `llm.provider` and `llm.model` on the root agent in `agents.yaml`.

Here is the smallest working setup. Add a provider. With an API key:

```yaml
# baifo.yaml (only the relevant section)
providers:
  - name: gemini
    type: gemini
    api_key: your-api-key-here
```

Or, if you have a Claude subscription and prefer OAuth over an API key, declare an `anthropic` provider with `auth: oauth` and no key:

```yaml
providers:
  - name: anthropic
    type: anthropic
    auth: oauth
```

Then run `baifo provider auth anthropic` once. The browser-based PKCE flow stores tokens that refresh transparently, so you never paste an API key.

Either way, point the root agent at the provider you declared:

```yaml
# agents.yaml (only the relevant section)
agents:
  - root: true
    name: coordinator
    llm:
      provider: gemini
      model: gemini-2.5-flash
```

That is enough. Everything else has sensible defaults. Until `llm.provider` and `llm.model` are set, baifo boots in a degraded state and asks you to finish the setup.

## `baifo.yaml`, section by section

### Providers

Each entry declares an LLM backend. Reference it by `name` from any agent's `llm.provider`.

```yaml
providers:
  - name: anthropic
    type: anthropic
    api_key: ${secret:ANTHROPIC_API_KEY}

  - name: anthropic-oauth
    type: anthropic
    auth: oauth                # no api_key; run `baifo provider auth anthropic-oauth` once

  - name: gemini
    type: gemini
    api_key: ${secret:GEMINI_API_KEY}

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
```

Accepted values for `type`: `anthropic`, `openai`, `gemini`. Any endpoint that speaks the OpenAI API (a local Ollama server, OpenRouter, vLLM, LocalAI, and so on) is declared as `type: openai` with `url` pointing at the endpoint. There is no separate type for them; the `url` is what distinguishes a custom endpoint from the canonical OpenAI API.

When a `url` matches an endpoint baifo recognizes (OpenRouter, Groq, DeepSeek, and other well-known OpenAI-compatible hosts), the `list_models` tool and the agent editor's model autocomplete surface that endpoint's real model catalogue instead of the canonical OpenAI one, flagged with a note that the live endpoint may serve a different subset. A `url` baifo does not recognize (a local Ollama, a private proxy) gets no catalogue: use whatever model id the endpoint serves.

`auth: oauth` is supported for `anthropic` providers only. Declare the provider with `auth: oauth` and no `api_key`, then run `baifo provider auth <name>` once to complete the browser-based PKCE login. Tokens persist per provider name and refresh transparently; when OAuth is active the `api_key` field is ignored. This is how a Claude subscription is used without an API key. Setting `auth: oauth` on a `gemini` or `openai` provider is rejected at startup, so the mistake surfaces immediately instead of silently falling back to an API key.

Fields on a provider entry:

| Field       | Default          | Notes                                                                    |
| ----------- | ---------------- | ------------------------------------------------------------------------ |
| `name`      | (required)       | Unique slug, referenced by agents                                        |
| `type`      | (required)       | `anthropic`, `openai`, or `gemini`                                       |
| `url`       | per-type default | Override only when the endpoint differs from the standard                |
| `auth`      | `api_key`        | `api_key` or `oauth`. OAuth is anthropic-only                            |
| `api_key`   |                  | Literal string or `${secret:NAME}`                                       |
| `headers`   | `{}`             | Extra headers; values also support `${secret:NAME}`                      |
| `streaming` | `true`           | Set `false` only for OpenAI-compatible endpoints that lack SSE support   |

### MCP servers

MCP (Model Context Protocol) servers give agents their tools. Three types exist: `builtin`, `http`, and `stdio`.

The scaffold pre-declares the two built-in servers, `filesystem` and `browse`. These are the only valid values for the `builtin` field.

```yaml
mcps:
  - name: filesystem
    type: builtin
    builtin: filesystem

  - name: browse
    type: builtin
    builtin: browse
```

The `filesystem` builtin accepts optional output caps through an `options` block:

```yaml
- name: filesystem
  type: builtin
  builtin: filesystem
  options:
    limit_exec_output_chars: 48000   # 0 or absent = default (48000); -1 = unlimited
    limit_read_file_chars: 120000    # 0 or absent = default (120000); -1 = unlimited
    limit_search_output_chars: 50000 # 0 or absent = default (50000); -1 = unlimited
```

These limits prevent a single large command output, file read or search from flooding the model's context window. The model is told about the caps and works around them (reading in chunks, filtering output, narrowing a search). `limit_search_output_chars` is separate from the search tool's own `max_results`: that one bounds the number of matches, this one bounds their total size, so a broad pattern over long lines cannot blow the context even when the match count stays low.

**HTTP MCPs** connect to remote tool servers:

```yaml
- name: github
  type: http
  endpoint: https://api.githubcopilot.com/mcp
  headers:
    Authorization: "Bearer ${secret:GITHUB_TOKEN}"
  insecure: false
```

HTTP MCPs optionally support OAuth. Leave `client_id` and `client_secret_ref` empty to opt into automatic discovery (Dynamic Client Registration or CIMD, controlled by `registration`):

```yaml
- name: github-oauth
  type: http
  endpoint: https://api.githubcopilot.com/mcp
  auth:
    kind: oauth
    client_id: ""
    client_secret_ref: ""
    registration: auto # auto | cimd | dcr
```

**stdio MCPs** launch a local binary:

```yaml
- name: kubernetes
  type: stdio
  command: kubernetes-mcp
  args: ["--kubeconfig", "~/.kube/config"]
  env: {}
  workdir: ""
```

The binary must be on `$PATH`.

### Spawn

Controls sub-agent orchestration.

```yaml
spawn:
  mode: both # none | static | dynamic | both
  collect_timeout: 5m # how long collect_agent waits for a worker
```

`mode` determines which spawn tools the root agent sees: `none` removes them entirely, `static` allows only pre-defined templates, `dynamic` allows only runtime-composed agents, `both` enables both. `collect_timeout` is a Go duration string.

### Secrets policy

The `secrets` block in `baifo.yaml` controls redaction behavior. The actual secret values live in `secrets.yaml`, never here.

```yaml
secrets:
  redact_in_logs: true
  redact_in_tui: true
  scrub_tool_results: true
  min_scrub_length: 8
```

| Field                | Default | Notes                                                                                                                                                                                          |
| -------------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `redact_in_logs`     | `true`  | Scrub stored secret values from log output                                                                                                                                                     |
| `redact_in_tui`      | `true`  | Scrub stored secret values before painting the TUI                                                                                                                                             |
| `scrub_tool_results` | `true`  | Scan every tool result for any secret value and replace it with `${secret:NAME}` before the agent sees it                                                                                      |
| `min_scrub_length`   | `8`     | Minimum byte length a secret must have to be eligible for the comprehensive scan. Higher values reduce false positives but let short secrets through. `0` disables the floor (not recommended) |

### Runtime

```yaml
runtime:
  log_level: info # debug | info | warn | error
  log_format: console # console | json
  log_file: "" # empty = stderr
  redact_logs: true
  auto_resume_session: true # false = fresh session on every launch
  chat_auto_scroll: true
  chat_keep_tools_expanded: false
```

`auto_resume_session` controls whether baifo restores the most recent session on boot or starts a new one.

#### Retry

The `retry` block governs retries for failing LLM API calls. Only calls that fail before producing any streamed text are retried, so a partial answer is never duplicated.

```yaml
runtime:
  retry:
    enabled: false
    max_attempts: 4 # total tries including the first; 1 = no retry
    strategy: backoff # backoff | retry-after
    backoff:
      initial: 1s
      max: 30s
      multiplier: 2.0
      jitter: true
```

Setting any field inside `retry` implicitly enables it; you can also set `enabled: true` explicitly. The `retry-after` strategy honors the provider's `Retry-After` header on 429 responses and falls back to exponential backoff when the header is absent. With the defaults, a flaky call waits roughly 1s, 2s, 4s before giving up.

### Guardrails

Guardrails are off by default. baifo does not alter what your messages look like to the model unless you opt in.

```yaml
guardrails:
  trim_oversized_user_text:
    enabled: false
    max_chars: 30000
```

`trim_oversized_user_text` truncates very large user-role text parts before each model call. This protects the context window when, for example, a resumed session replays enormous command outputs as context. The trim is ephemeral: your session history keeps the full text on disk. `max_chars` sets the per-part character cap; `0` or absent selects the default (30000). The `enabled` flag controls activation.

### A2A

The A2A (Agent-to-Agent) server exposes your root agent over the A2A protocol so other systems can drive it programmatically.

```yaml
a2a:
  enabled: false
  host: 127.0.0.1
  port: 8090
  public_url: ""
  credentials:
    token: ""
```

When `enabled` is `false`, `baifo server` refuses to start. Set it to `true` and configure `host`/`port` to activate.

`public_url` is advertised in the agent card; when empty, it derives from `host` and `port`. `credentials.token` sets a bearer token required on every `/api/a2a` request (the `/healthz` endpoint stays open). It accepts a literal string or a `${secret:NAME}` reference. Empty means unauthenticated.

The scaffold writes `port: 8090`. The loader's built-in default when the field is omitted entirely is `7777`.

### Encryption key

```yaml
encryption_key: ""
```

This field controls encryption for `secrets.yaml`. When empty, secrets are stored in plaintext-at-rest mode (each value is `plain:v1:<base64>`). Set any passphrase to switch to AES-256-GCM encryption. A `${secret:NAME}` reference is not valid here (this key unlocks the store itself). Environment variable expansion works: `${BAIFO_ENCRYPTION_KEY}`.

Use the `/secret encode` command to re-seal existing plaintext entries after setting a key, and `/secret decode` to reverse the process.

## `agents.yaml`

### The root agent

Exactly one entry in `agents.yaml` must set `root: true`. The loader rejects the file if more than one root is declared. The root agent is the one you talk to in the TUI and over A2A. It is always running.

The scaffold seeds the root with a name, description, a generalist prompt, and the context guard pre-configured. Only `llm.provider` and `llm.model` are left blank for you to fill in.

### Spawnable templates

Every non-root, non-utility entry is a static worker template. The root invokes them by name via the `spawn_static_agent` tool. Define as many as you need:

```yaml
agents:
  - name: deep-researcher
    description: |
      Web-research specialist that follows citation chains.
    prompt: |
      You are a deep research agent. ...
    llm:
      provider: openai-main
      model: gpt-5
    skills: [web-research]
    mcps: [browse]
    context_guard:
      enabled: true
      strategy: sliding_window
      max_turns: 20
    allowed_secrets: []
```

### The utility agent (optional)

baifo runs small internal chores in the background: naming sessions and writing context-guard compaction summaries. By default there is no separate utility agent, so these chores use the root agent's model. That works, but it spends your expensive model on trivial work.

To save tokens, add one agent with `utility: true` pointing at the cheapest, fastest model you have. It never chats and gets no tools; only its `llm` block is read. At most one entry may set `utility: true`. If its `provider` and `model` are left empty, the chores fall back to the root's model.

```yaml
- utility: true
  name: utility
  description: Cheap model for internal chores (titles, compaction).
  prompt: You are an internal utility agent.
  llm:
    provider: anthropic
    model: claude-haiku-4-5-20251001
```

### The `llm` block

```yaml
llm:
  provider: anthropic # name of a provider from baifo.yaml
  model: claude-sonnet-4-20250514
  reasoning: high # minimal | low | medium | high (empty = model default)
  reasoning_api: "" # anthropic-only: enabled | adaptive (empty = auto-detect)
```

`reasoning` controls how hard the model thinks. It only works on models whose `list_models` entry reports `reasoning_levels`. Setting it on a non-reasoning model makes the API reject the call. The agent's `list_models` tool reports which models support reasoning and at which levels; the `/agent edit` editor also autocompletes only the levels the chosen model supports.

`reasoning_api` is relevant only for Anthropic models. `enabled` selects classic budget-based extended thinking (Claude 3.7, Sonnet 4, Opus 4). `adaptive` selects effort-based thinking (Opus 4.5 and newer). Empty auto-detects from the model catalogue, which is the right choice in nearly every case.

### Root vs. template defaults

The root agent and spawnable templates have different defaults for three fields. This is intentional (least privilege for sub-agents, full access for the user-facing root):

| Field             | Root (empty list)                                | Template (empty list)                 |
| ----------------- | ------------------------------------------------ | ------------------------------------- |
| `skills`          | All visible skills                               | None                                  |
| `mcps`            | All visible MCP servers                          | None                                  |
| `allowed_secrets` | Unrestricted (always has access to every secret) | None (no secrets may be dereferenced) |

A spawn call may narrow a template's `allowed_secrets` list via the `spawn_static_agent` tool's arguments, but it can never exceed what the template declares.

### Context guard

```yaml
context_guard:
  enabled: true
  strategy: threshold # threshold | sliding_window
  max_tokens: 0 # 0 = auto-detect from the model's context window
  max_turns: 0 # 0 = strategy default
```

`threshold` compacts the conversation when token usage approaches the model's context window. `sliding_window` keeps only the last `max_turns` events and summarizes older ones. When the context guard fires, the TUI shows a notice in the transcript and a live gauge in the footer.

### Full `AgentTemplate` fields

`root`, `utility`, `name`, `description`, `prompt`, `llm` (`provider`, `model`, `reasoning`, `reasoning_api`), `skills`, `mcps`, `context_guard`, `allowed_secrets`. There is no `sandbox` block and no `output_schema` field.

## `secrets.yaml`

The secret store runs in one of two modes depending on whether `encryption_key` is set in `baifo.yaml`.

**Plaintext mode** (`encryption_key` empty):

```yaml
version: 1
encrypted: false
secrets:
  ANTHROPIC_API_KEY:
    description: "API key for Anthropic"
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-01T00:00:00Z
    value: "plain:v1:<base64>"
```

**Encrypted mode** (`encryption_key` set):

```yaml
version: 1
encrypted: true
secrets:
  ANTHROPIC_API_KEY:
    description: "API key for Anthropic"
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-02T00:00:00Z
    value: "AES256GCM:v1:<base64 nonce>:<base64 ciphertext>"
```

Manage secrets through commands, not by editing the file directly. Hand-editing an encrypted store corrupts it.

**In the TUI:**

- `/secret list` (names only, never values)
- `/secret set NAME` (masked prompt)
- `/secret delete NAME`
- `/secret encode` (seal all plaintext entries after setting an encryption key)
- `/secret decode` (decrypt all entries back to plaintext)

**On the CLI:**

- `baifo secrets set NAME`
- `baifo secrets unset NAME`
- `baifo secrets list`
- `baifo secrets rotate NAME`

## Skills

Each skill is a directory under `.baifo/skills/<slug>/` containing a `SKILL.md` file and optional subdirectories:

```
.baifo/skills/web-research/
  SKILL.md
  references/
    examples.md
  assets/
    template.md
  scripts/
    post-install.sh
```

`SKILL.md` is Markdown with a YAML frontmatter. Required keys: `name` and `description`. `version` is optional.

```markdown
---
name: web-research
description: |
  Deep web research protocol with citation tracking.
version: 1
---

# Web research

Instructions for the agent go here in Markdown.
```

The `references/`, `assets/`, and `scripts/` subdirectories are optional and exposed to the agent through the skill toolset. Agents opt into skills via their `skills` list in `agents.yaml`.

## Full reference

The two complete annotated files below show every field the loader understands; use them as a copy-paste starting point. The first-run wizard writes a subset: the `baifo.yaml` is reproduced as-is (minus any provider you configure), and in `agents.yaml` it writes only the root agent. The optional `utility` entry shown below is **not** created by the wizard — add it yourself to route internal chores to a cheaper model.

### `baifo.yaml`

```yaml
# baifo configuration
# The root agent (and any sub-agents) live in agents.yaml. This file
# holds the global knobs. Every section below is pre-filled with its
# default value. providers, mcps and secrets start empty.

version: 1

# Encryption key for the secrets store (secrets.yaml). Empty = plaintext
# mode. Set a passphrase to switch to AES-256-GCM. ${secret:NAME} is NOT
# valid here; use a literal or env var like ${BAIFO_ENCRYPTION_KEY}.
encryption_key: ""

# Runtime
runtime:
  log_level: info # debug | info | warn | error
  log_format: console # console | json
  # log_file: ""               # empty = stderr
  redact_logs: true # scrub secret values from logs
  auto_resume_session: true # false = fresh session each launch
  chat_auto_scroll: true # pin chat to newest message
  chat_keep_tools_expanded: false

  # Retry failed LLM calls. Only calls that fail before streaming any
  # text are retried; partial answers are never duplicated.
  retry:
    enabled: false
    max_attempts: 4 # total tries incl. the first; 1 = no retry
    strategy: backoff # backoff | retry-after
    backoff:
      initial: 1s
      max: 30s
      multiplier: 2.0
      jitter: true

# Guardrails (off by default; opt-in)
guardrails:
  trim_oversized_user_text:
    enabled: false # set true to activate
    max_chars: 30000 # per-part cap; 0 = default (30000)

# A2A server
a2a:
  enabled: false
  host: 127.0.0.1
  port: 8090
  public_url: "" # empty = derive from host:port
  credentials:
    token: "" # bearer token; literal or ${secret:NAME}; empty = no auth

# Spawn (sub-agent orchestration)
spawn:
  mode: both # none | static | dynamic | both
  # collect_timeout: 5m        # how long collect_agent waits for a worker

# Secrets (redaction policy; values live in secrets.yaml)
secrets:
  redact_in_logs: true
  redact_in_tui: true
  scrub_tool_results: true # scan tool results for secret values
  min_scrub_length: 8 # min bytes for scan eligibility; 0 = no floor

# Providers (LLM backends)
# Add at least one, then set the root agent's llm.provider to its name.
# Fields per entry:
#   name: (required) unique slug
#   type: (required) anthropic | openai | gemini
#   url: override endpoint; empty = provider default. Any OpenAI-compatible
#        endpoint (Ollama, OpenRouter, vLLM, ...) is type: openai + url
#   auth: api_key (default) | oauth (anthropic-only)
#   api_key: literal or ${secret:NAME}
#   headers: extra headers (values support ${secret:NAME})
#   streaming: true (default); false for OpenAI-compatible endpoints without SSE
providers: []

# MCP servers
# Fields per entry:
#   name: (required) unique slug
#   type: builtin | http | stdio
#   builtin: filesystem | browse (when type=builtin)
#   options: (type=builtin) limit_exec_output_chars, limit_read_file_chars, limit_search_output_chars
#   endpoint: (type=http) server URL
#   insecure: (type=http) skip TLS verification
#   headers: (type=http) extra headers
#   auth: (type=http) kind: none|oauth, client_id, client_secret_ref, registration
#   command: (type=stdio) executable
#   args: (type=stdio) arguments
#   env: (type=stdio) environment
#   workdir: (type=stdio) working directory
mcps:
  - name: filesystem
    type: builtin
    builtin: filesystem
    # options:
    #   limit_exec_output_chars: 48000
    #   limit_read_file_chars: 120000
    #   limit_search_output_chars: 50000

  - name: browse
    type: builtin
    builtin: browse
```

### `agents.yaml`

```yaml
version: 1

# Exactly one entry must set root: true. Until llm.provider and
# llm.model are filled in, baifo boots in a degraded state.

agents:
  - root: true
    name: coordinator
    description: Multidisciplinary local assistant.
    llm:
      provider: "" # name of a provider from baifo.yaml
      model: "" # model identifier
      reasoning: high # minimal | low | medium | high (empty = model default)
      # reasoning_api: ""      # anthropic-only: enabled | adaptive (empty = auto-detect)
    prompt: |
      You are baifo, a multidisciplinary coordinator that lives in the user's terminal.
      You are a powerful generalist: coding, research, writing, planning, summarising,
      triaging files, shell work and sketching ideas are all first-class jobs for you.

      You have a broad toolbox at your disposal: the filesystem, the shell, the web,
      long-term memory, todo lists, and a roster of sub-agents you can spawn.

      ## Memory and Facts (CRITICAL)
      Every time you initiate a conversation or resume work, you MUST call the
      search_memory tool BEFORE doing anything else to orient yourself.
      If you discover a new preference, learn a new architecture detail, or make a
      decision with the user, call save_to_memory immediately. Your facts are your
      source of truth for the user's preferences, so keep them fiercely updated and
      always take them into account. For multi-step jobs, use the todos_* tools to
      never lose track.

      ## Brainstorming and Delegation
      You are a coordinator, not just a worker. Brainstorm with the user, discuss
      architecture details, and plan heavily before executing.
      Once a plan is solid, DO NOT execute it entirely yourself in the main chat.
      Instead, orchestrate: you MUST use the spawn_dynamic_agent tool to fabricate
      sub-agents for specific tasks or reuse static templates with spawn_static_agent
      when they fit. Give these agents extremely well-crafted prompts, and assign them
      efficient, smaller models that fit their specific task (always check list_models
      first to ensure you only pick models from currently configured providers),
      reserving larger models only for the hard parts.
      Once delegated, stay on top of it: use inspect_agent to monitor their progress
      as much as you need without destroying them. Once they are done, use
      collect_agent to retrieve the final result and fold their output into one
      coherent synthesis.

      ## Voice and Style
      Answer directly, avoid unnecessary formalisms, and lead with the answer.
      Your tone should be slightly acidic and sharp. Mirror the user's language.
      Never assume the user's technical knowledge; explain from the ground up.
      Stay concise by default and go deeper when brainstorming or when the user asks
      for depth. Do not invent tool calls to look busy.

      ## Working with the user's machine
      Always read before you write. Keep changes small and easy to verify.
      You MUST prioritize specific MCP tools (read_file, ls, search, write_file,
      edit_file) for filesystem operations over using the generic shell (exec with
      cat, ls, grep). Save the shell (exec) strictly for real executions like builds,
      tests, git, scripts, and piping. Start long-running processes in the background
      and poll their status. Reach for undo when a write goes wrong.

      ## Researching on the web and Accuracy
      Go to the web when the answer depends on current or version-specific facts,
      especially for specific technical projects. Search for official documentation
      or source code, fetch the promising ones, and read them rigorously.
      Prioritize the absolute truth over guessing: never invent or hallucinate
      answers. Always tell the user the exact sources of your truths.

      ## Safety and when things fail
      Pure reads (listing, fetching, reading, inspecting workers) need no permission.
      Before any state change (writing, running commands with side effects, spawning
      agents), give a one-line plan and ask whether to proceed.
      Never fabricate or echo a secret's value; use ${secret:NAME}.
      If something fails, say what failed in one line and offer a concrete next step.
      Don't quietly retry the same thing.
    skills: [] # root: empty = all visible skills
    mcps: # root: empty = all visible MCPs
      - filesystem
      - browse
    allowed_secrets: [] # ignored for root (always has full access)
    context_guard:
      enabled: true
      strategy: threshold # threshold | sliding_window
      max_tokens: 0 # 0 = auto-detect from model window
      max_turns: 0 # 0 = strategy default

  # Utility agent (OPTIONAL, not written by the wizard): a cheap model
  # for internal chores (session titling, compaction summaries). Never
  # chats, gets no tools, only llm is read. Omit this entry entirely and
  # those chores use the root's model. Point it at a cheap model to save
  # tokens.
  - utility: true
    name: utility
    description: Cheap model for internal chores (titles, compaction).
    prompt: You are an internal utility agent.
    llm:
      provider: anthropic
      model: claude-haiku-4-5-20251001
```
