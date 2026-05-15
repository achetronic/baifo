// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package main

// scaffold.go holds the file bodies the first-run wizard writes into a
// fresh .baifo/ directory. They are kept here (rather than inline in
// wizard.go) so the YAML stays readable and is easy to keep in sync
// with internal/config defaults.
//
// Design notes:
//   - baifo.yaml is written FULLY POPULATED: every section the loader
//     understands appears with its default value already filled in, so
//     the file doubles as living documentation. providers, mcps and
//     secrets are the only collections that start empty: the user adds
//     their own. Values here must mirror internal/config.applyDefaults.
//   - agents.yaml is seeded with a single root agent that is complete
//     except for the model: name, description, a generalist persona
//     prompt and the tool wiring are all present, but llm.provider /
//     llm.model are left blank for the user to choose via /agent edit.
//     internal/config.validateAgents permits an empty LLM specifically
//     for the root entry; App.buildRoot treats it as a degraded boot
//     until a model is set.

// defaultBaifoYAML is the baifo.yaml written on first run.
//
// IMPORTANT: this template is the user's primary documentation: it must
// mention EVERY field the loader understands so a fresh user can discover
// every knob without reading the source. TestDefaultBaifoYAMLCoversSchema
// fails the build if a field is added to config.Config (or a nested
// struct) without being mentioned here. Commented-out is fine; the goal
// is "the user can see it exists". Values must mirror
// internal/config.applyDefaults and the accessor defaults.
const defaultBaifoYAML = `# baifo configuration - see https://github.com/achetronic/baifo
#
# The root agent (and any sub-agents) live in agents.yaml. This file
# holds the global knobs. Every section below is pre-filled with its
# default value and documented inline; edit freely. providers, mcps and
# secrets start empty and are yours to populate.

version: 1

# Encryption key for the secrets store (secrets.yaml). Leave empty to
# keep secrets in plaintext-at-rest mode (fine for a single-user local
# box). Set any passphrase to switch the store to AES-256-GCM; existing
# entries are migrated with the /secret encode and /secret decode
# commands. A ${secret:NAME} reference is NOT valid here - this is the
# key that unlocks the store, so it must be a literal (or an env var).
encryption_key: ""

# Runtime
runtime:
  log_level: info            # debug | info | warn | error
  log_format: console        # console | json
  # log_file: ""             # empty logs to stderr
  redact_logs: true          # scrub secret values from logs
  auto_resume_session: true  # false starts a clean session each launch
  chat_auto_scroll: true     # pin the chat to the newest message
  chat_keep_tools_expanded: false  # keep tool-call panels open in the chat

  # Retry failed LLM provider calls with an exponential backoff before
  # giving up on a turn. Only calls that fail before producing any text
  # are retried, so a partial answer is never duplicated.
  retry:
    enabled: false           # turn retries on (default off)
    max_attempts: 4          # total tries, including the first (1 = no retry)
    strategy: backoff        # backoff (exponential) | retry-after (honour
                             # the provider's Retry-After header on 429s,
                             # falling back to the backoff when absent)
    backoff:
      initial: 1s            # wait before the 1st retry
      max: 30s               # cap on a single wait, so it can't grow forever
      multiplier: 2.0        # each wait = previous * this (1s → 2s → 4s ...)
      jitter: true           # randomise each wait to avoid retry storms

# Guardrails — optional protections that keep long conversations healthy.
# They are OFF by default: baifo never alters what your messages look
# like to the model unless you ask it to. Turn one on by setting its
# enabled: true; each one can be toggled independently.
guardrails:

  # trim_oversized_user_text: when enabled, if a single message is
  # enormous (for example you paste a huge file into the chat, or baifo
  # replays an old conversation that contains very large command
  # outputs), only the first part is sent to the model and a note marks
  # where it was cut. This keeps one giant message from eating the
  # model's whole memory window. Nothing is lost on disk: your session
  # history keeps the full text; the cut only affects what travels to
  # the model. Recommended if you work with multiple agents over the
  # same long-lived sessions.
  trim_oversized_user_text:
    enabled: false           # opt-in: set true to activate
    max_chars: 30000         # size limit per message; 0 = default (30000)

  # cap_external_tool_output: RESERVED — not implemented yet.
  # Will limit how much output third-party (http/stdio) MCP tools can
  # send back, the same way the built-in filesystem tools already do.
  # (Do not set this field; the loader ignores unknown keys.)

# Theme
# The colour palette is fixed (the Canarias theme) and not configurable.
theme:
  nerd_fonts: true           # if your terminal font lacks Nerd Font
                             # glyphs, set false and they degrade to ASCII

# A2A server
# Exposes the root agent over the Agent-to-Agent protocol.
a2a:
  enabled: false
  host: 127.0.0.1
  port: 8090
  public_url: ""             # URL advertised in the Agent Card; empty = derive
  credentials:
    token: ""                # bearer token required on every request; a literal
                             # or ${secret:NAME}. Empty = server is unauthenticated

# Spawn (sub-agent orchestration)
spawn:
  mode: both                 # none | static | dynamic | both
  # collect_timeout: 5m      # how long collect_agent waits for a worker

# Secrets
# Runtime behaviour of the secrets pipeline. The secret VALUES live in
# secrets.yaml (managed via 'baifo secrets' or the /secret command),
# never here.
secrets:
  redact_in_logs: true       # scrub stored secret values out of log output
  redact_in_tui: true        # scrub stored secret values out of the TUI

  # Scan every tool result for ANY value stored in secrets.yaml and
  # replace it with its ${secret:NAME} placeholder before the agent sees
  # it - even values the model never asked for (e.g. a tool that echoes a
  # token it found). Leave on unless you are debugging raw tool output.
  scrub_tool_results: true

  # Minimum length (bytes) a stored secret must have to be eligible for
  # the scan above. Floors out short, low-entropy values ("1234") that
  # would otherwise rewrite unrelated text. Higher = fewer false
  # positives but short secrets bypass the scan; lower = more coverage
  # but more risk. 8 covers every real API key; 0 disables the floor
  # (not recommended).
  min_scrub_length: 8

# Providers (LLM backends)
# Add at least one, then point the root agent's llm.provider at it.
# Every field a provider entry accepts:
#   - name: anthropic              # unique id referenced by agents' llm.provider
#     type: anthropic              # anthropic | openai | gemini | ollama
#     url: ""                      # override the endpoint; empty = provider default
#     api_key: ${secret:ANTHROPIC_API_KEY}  # literal or ${secret:NAME}
#     headers:                     # extra headers on every request (values
#       X-Org-ID: "acme"           # support ${secret:NAME})
#     streaming: true              # SSE streaming; set false only for
#                                  # OpenAI-compatible endpoints without SSE
providers: []

# MCP servers
# Built-in, HTTP or stdio MCP tool servers. Every field an entry accepts:
#   - name: my_http_mcp            # unique id referenced by agents' mcps
#     type: http                   # builtin | http | stdio
#     endpoint: https://mcp.example.com  # (type: http) server URL
#     insecure: false              # (type: http) skip TLS verification
#     headers:                     # (type: http) sent on every request
#       Authorization: "Bearer ${secret:MY_API_KEY}"
#     command: /usr/local/bin/mcp  # (type: stdio) executable to launch
#     args: ["--flag", "value"]    # (type: stdio) its arguments
#     env:                         # (type: stdio) extra environment
#       FOO: bar
#     workdir: /tmp                # (type: stdio) working directory
#     auth:                        # (type: http) optional OAuth
#       kind: none                 # none | oauth
#       client_id: ""              # pre-registered client; empty = discover
#       client_secret_ref: ""      # name of a secrets.yaml entry, not the secret
#       registration: auto         # auto | cimd | dcr (how to obtain a client)
mcps:
  - name: filesystem
    type: builtin
    builtin: filesystem
    # Optional tuning — sensible limits apply by default, nothing to
    # configure. The filesystem tools cap how much text a single command
    # or file read can return, so one accidental 'cat' of a huge file
    # cannot flood the model's memory. The model is told about these
    # limits and works around them (reading in chunks, filtering output).
    # 0 or absent = default; positive = your own limit; -1 = no limit.
    # options:
    #   limit_exec_output_chars: 48000   # max text per command output (default 48000)
    #   limit_read_file_chars: 120000    # max text per file read (default 120000)

  - name: browse
    type: builtin
    builtin: browse
`

// defaultAgentsYAML is the agents.yaml written on first run. It seeds a
// complete root agent minus the model choice.
const defaultAgentsYAML = `version: 1

# Exactly one entry must set 'root: true' (the agent you talk
# to in the TUI and over A2A. The root below is ready to go except for
# its model: set llm.provider and llm.model (via the /agent command or
# by hand) once you've added a provider to baifo.yaml. Until then baifo
# boots in a degraded state and prompts you to finish the setup.)

agents:
  - root: true
    name: baifo-geminoso
    description: Multidisciplinary local assistant.
    # Pick a provider declared in baifo.yaml and a model it offers.
    llm:
      provider: ""
      model: ""
      reasoning: high          # how hard the model thinks (for models that support it)
      # reasoning_api: ""      # enabled | adaptive (empty = auto-detect)
    prompt: |
      You are baifo, a multidisciplinary coordinator that lives in the user's terminal.
      You are a powerful generalist: coding, research, writing, planning, summarising, 
      triaging files, shell work and sketching ideas are all first-class jobs for you.

      You have a broad toolbox at your disposal — the filesystem, the shell, the web,
      long-term memory, todo lists, and a roster of sub-agents you can spawn.

      ## Memory and Facts (CRITICAL)
      Every time you initiate a conversation or resume work, you MUST call the search_memory tool BEFORE doing anything else to orient yourself.
      If you discover a new preference, learn a new architecture detail, or make a 
      decision with the user, call save_to_memory immediately. Your facts are your 
      source of truth for the user's preferences, so keep them fiercely updated and 
      always take them into account. For multi-step jobs, use the todos_* tools to never 
      lose track.

      ## Brainstorming and Delegation
      You are a coordinator, not just a worker. Brainstorm with the user, discuss 
      architecture details, and plan heavily before executing. 
      Once a plan is solid, DO NOT execute it entirely yourself in the main chat. 
      Instead, orchestrate: you MUST use the spawn_dynamic_agent tool to fabricate sub-agents for specific tasks or reuse 
      static templates with spawn_static_agent when they fit. Give these agents extremely well-crafted prompts, 
      and assign them efficient, smaller models that fit their specific task (always 
      check list_models first to ensure you only pick models from currently configured 
      providers), reserving larger models only for the hard parts. 
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
      You MUST prioritize specific MCP tools (read_file, ls, search, write_file, edit_file) for filesystem operations over using the generic shell (exec with cat, ls, grep).
      Save the shell (exec) strictly for real executions like builds, tests, git, scripts, and piping. Start 
      long-running processes in the background and poll their status.
      Reach for undo when a write goes wrong. 

      ## Researching on the web and Accuracy
      Go to the web when the answer depends on current or version-specific facts, 
      especially for specific technical projects. Search for official documentation 
      or source code, fetch the promising ones, and read them rigorously.
      Prioritize the absolute truth over guessing: never invent or hallucinate answers. 
      Always tell the user the exact sources of your truths.

      ## Safety and when things fail
      Pure reads (listing, fetching, reading, inspecting workers) need no permission. 
      Before any state change (writing, running commands with side effects, spawning 
      agents), give a one-line plan and ask whether to proceed.
      Never fabricate or echo a secret's value; use ${secret:NAME}.
      If something fails, say what failed in one line and offer a concrete next step. 
      Don't quietly retry the same thing.
    skills: []
    mcps:
      - filesystem
      - browse
    allowed_secrets: []
    context_guard:
      enabled: true
      strategy: threshold
      max_tokens: 0            # 0 = auto-detect from the model's window
      max_turns: 0             # 0 = strategy default

  # The utility agent: a cheap model for baifo's internal chores
  # (naming sessions, summarising conversations when the context
  # window fills up). It never chats and gets no tools — only its
  # llm block is used. Point it at the cheapest/fastest model you
  # have; leaving provider/model empty makes those chores fall back
  # to the root's model (which works, but burns expensive tokens on
  # trivial work).
  - utility: true
    name: utility
    description: Cheap model for internal chores (titles, compaction).
    prompt: You are an internal utility agent.
    llm:
      provider: ""
      model: ""
`

// defaultSecretsYAML is the secrets.yaml written on first run.
// It documents the format and points the user to the TUI commands
// as the primary way of managing it.
const defaultSecretsYAML = `# baifo secrets store
#
# DO NOT EDIT MANUALLY unless you are running in plaintext mode
# (encryption_key: "" in baifo.yaml). If the store is encrypted, manual
# edits will corrupt the file.
#
# Manage secrets via the TUI instead:
#   /secret add NAME
#   /secret edit NAME
#   /secret rm NAME
#   /secret list
#
# And in headless mode:
#   baifo secrets add NAME
#   baifo secrets rm NAME

version: 1
encrypted: false
secrets: {}
`
