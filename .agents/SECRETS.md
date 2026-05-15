# SECRETS.md

The complete pipeline for storing, exposing, and protecting secrets.
Reference for `internal/secrets/` and the agent callback wiring in
`internal/agent/builder.go` + `internal/agent/callbacks.go`.

## Threat model

What we protect against:

1. **The LLM seeing raw secrets in its context.** Even if the LLM is
   trusted, sending raw API keys to a model provider is operationally
   bad: keys appear in their logs, in tool-call args, in failure traces.
2. **Tools that reflect their input in their output.** Many HTTP-style
   tools return the request they made (including headers) on error or
   for debugging. That output goes back to the LLM as a `tool_result`.
3. **Tools that emit secrets they were not given.** An MCP echoing a
   debug page that prints a token in plain text, a file the
   filesystem MCP reads that happens to contain a stored secret, an
   exec command whose stdout includes a credential - these reach the
   model without ever going through the expander. Defended by the
   comprehensive AfterToolCallback pass that scans every result for
   any value in the store; see "Redaction pipeline" below.
4. **Local logs containing secrets.** A library that logs a request URL
   with a query-string secret would leak the value to disk.

What we explicitly do **not** protect against:

- A malicious or buggy tool that uses an expanded secret to call out to
  an attacker-controlled URL. Mitigation lives at the MCP / sandbox
  layer.
- An attacker with read access to `.baifo/secrets.yaml`. In encrypted
  mode, the file is sealed, but `baifo.encryption_key` comes from the
  user's environment or config; if both are compromised, secrets are
  revealed. Filesystem perms are the real defence (`0600`).
- A compromised model provider replaying our prompts with the
  (already-redacted) names. Names + descriptions are visible; that's
  accepted.

## Storage format and modes

Single file: `.baifo/secrets.yaml`. Permissions `0600` enforced on write.

The store has **two modes**, controlled by `encryption_key` in
`baifo.yaml`:

### Encrypted mode (`encryption_key` set)

```yaml
version: 1
encrypted: true
secrets:
  OPENAI_API_KEY:
    description: "OpenAI API key, used by the openai-main provider"
    created_at: "2026-05-14T22:00:00Z"
    rotated_at: "2026-05-14T22:00:00Z"
    value: "AES256GCM:v1:bm9uY2VfYjY0:Y2lwaGVydGV4dF9iNjQ="
```

`value` format: `AES256GCM:v1:<base64 nonce>:<base64 ciphertext>`. Each
secret has an independent nonce, so rotating one doesn't require
re-encrypting the others.

Key derivation: `key = PBKDF2-SHA256(encryption_key, salt="baifo-secrets-v1",
iter=200000, len=32)`. The salt is fixed for v1 because baifo is
single-user and the goal is "encrypt at rest", not "resist offline
brute-force from a different secret per install". When/if we add
per-install salts we bump the prefix to `AES256GCM:v2:...` and migrate.

### Plaintext mode (`encryption_key: ""` or absent)

```yaml
version: 1
encrypted: false
secrets:
  OPENAI_API_KEY:
    description: "OpenAI API key, used by the openai-main provider"
    created_at: "2026-05-14T22:00:00Z"
    rotated_at: "2026-05-14T22:00:00Z"
    value: "plain:v1:<base64>"
```

`value` is the base64 of the raw secret. The on-disk flag `encrypted:
false` makes the mode explicit so misconfiguration is obvious.

### Switching modes

`store.Encode()` re-seals every plaintext entry once an `encryption_key`
is configured (and flips the `encrypted` flag). `store.Decode()` does
the inverse: unwraps every encrypted entry into plaintext (and flips the
flag back). Decoding is destructive of confidentiality.

The TUI drives these through `/secret encode` and `/secret decode`
(`internal/tui/commands.go::handleSecretCommand`), which call the facade
methods `EncodeSecrets(ctx)` / `DecodeSecrets(ctx)` (`internal/app/secrets.go`).
`/secret encode` refuses when no `encryption_key` is set; the guard lives
inside `store.Encode()`, which returns an error that the TUI surfaces as
an error message. Both report how many entries were converted.

## What the agents see

Secrets reach the agent through **two surfaces**:

1. **The dynamic-spawn tool description.** When the spawn tool catalogue
   is built, every secret in the live universe is listed with name +
   description so the root can decide which to grant a worker. The
   value is never included. See `internal/tools/spawn/dynamic.go`.

2. **Tool argument expansion at call time.** The model writes
   `${secret:NAME}` literally inside tool-call arguments. The
   BeforeToolCallback expands it before the tool runs.

The model never sees raw values.

## Allowlists

Secret visibility is **per agent**. The two roles use distinct
mechanisms:

- **Root** is the user-facing coordinator and the frontier of trust.
  It receives `AllowAll` unconditionally and can dereference every
  secret declared in `secrets.yaml`. There is no `root.allowed_secrets`
  field in `baifo.yaml` -- restricting the root would force the operator
  to maintain a redundant allowlist every time they add a secret.
  Implemented via `agent.Spec.UnrestrictedSecrets = true` at
  `internal/app/app.go::buildRoot`.

- **Static templates** (`agents.yaml`) and **dynamic workers**
  (`spawn_dynamic_agent` args) go through `secrets.AllowerFor` with an
  **explicit** allowlist. The slice semantics are least-privilege:

  - `nil` or `[]` → `AllowNone{}`. The agent cannot dereference any
    secret. This is the default for templates that omit
    `allowed_secrets` and for dynamic spawns that omit the field or
    pass `[]`.
  - non-empty list → `AllowList`. Exactly those names are allowed.

### Subset rule on spawn

A spawn call cannot grant secrets the spawning agent itself cannot
dereference. Enforced in two places:

- **Static spawn** (`internal/tools/spawn/spawn.go::
  resolveStaticAllowedSecrets`):
  - The template's `allowed_secrets` must be a subset of the parent's.
  - The LLM-supplied override (when not nil) must be a subset of the
    template's (so the LLM can only *narrow*, never *expand*).
  - The override is also independently checked against the parent's
    allowlist. Redundant when override ⊆ template ⊆ parent, but
    cheap and explicit.
- **Dynamic spawn** (`internal/tools/spawn/dynamic.go::
  buildDynamicSpec`):
  - `args.AllowedSecrets` must (a) exist in the global universe and
    (b) be a subset of the parent's allowlist.

For both paths the parent's allowlist is held on
`spawntools.Tools.ParentAllowedSecrets`. Two encodings:

- `nil`           → parent is **sovereign** (no restriction). The
  global-universe check is the only gate. This is what the root sees
  today: `App.spawnToolsForRoot` sets the field to `nil` explicitly.
- non-nil slice  → parent's explicit allowlist. Sub-agent allowlists
  must be a subset of this list, and an empty list means the parent
  cannot delegate any secret. Reserved for the future case where a
  sub-agent receives its own spawn tools.

### Spawn tools are opaque to the expander

The BeforeToolCallback walks tool-call arguments looking for
`${secret:NAME}` placeholders and substitutes them with the real
value. Most tools want this: a Bearer header, an env var, a curl
URL - all are external-facing args that need the secret to do their
job, and the redactor scrubs the result before the model sees it.

Spawn tools are different. Their arguments carry the **child
agent's full spec**: prompt, initial message, allowed_secrets list,
sandbox path. If the expander rewrote a placeholder appearing inside
that spec, the raw value would be baked into the child agent's
prompt at construction time and bypass the child's own allowlist
(since the root holds `AllowAll`).

The fix is structural and lives in two places:

- `internal/tools/spawn.OpaqueToolNames()` lists the spawn tool
  names whose args must not be walked (`spawn_static_agent`,
  `spawn_dynamic_agent`, `spawn_dynamic_agents`).
- `agent.Builder.OpaqueTools` accepts that list and threads it
  through `makeBeforeExpand`. The callback now checks the tool
  name and short-circuits without touching the args when the tool
  is opaque.

With the guard in place, `${secret:NAME}` placeholders propagate
verbatim into `workers.Spec.Prompt` / `InitialMessage` /
`AllowedSecrets`. The child agent is built with those placeholders
in its prompt. When the child later calls a real tool with a
placeholder in the args, **its own** BeforeToolCallback expands it
- or refuses, if the child's allowlist denies the name. That is
what makes the `allowed_secrets` subset check load-bearing: there
is no longer any path by which a child can see a raw secret value
the parent expanded.

`App.buildRoot` and `App.buildWorkerAgent` both populate
`Builder.OpaqueTools` so the rule applies uniformly to every agent
that has spawn tools (today only the root; tomorrow possibly a
sub-agent that spawns its own workers).

## Expansion pipeline (BeforeToolCallback)

Implemented in `internal/secrets/pipeline.go`. Registered on every
agent's runner as the **only** `BeforeToolCallback`, ahead of any tool
execution.

Behaviour:

- Walks recursively through `map[string]any`, `[]any`, and every string.
  Numbers/bools/nil pass through.
- Regex anchor: `\$\{secret:([A-Za-z0-9_\-]+)\}`. No nesting, no
  defaults, no escaping.
- If the secret name is in the agent's allowlist and exists in the
  store, the placeholder is replaced with the raw value.
- Unknown / disallowed names are **left in place**. The tool call
  usually fails with a clear error ("Authorization header malformed:
  literal `${secret:foo}`"), which the LLM can recover from.
- The expander records which `(name → raw_value)` pairs it actually
  substituted in a per-call context value so the redactor can find
  them on the way out.

## Redaction pipeline (AfterToolCallback)

Implemented in `internal/agent/callbacks.go::makeAfterRedact`.
Registered as the **first** `AfterToolCallback`, so audit (the
second after-callback) always logs the redacted view.

The callback runs two passes per tool result:

**Targeted pass.** Scrubs every raw value the BeforeToolCallback
just substituted for *this* tool call. The pairs are stashed in a
process-wide map keyed by FunctionCallID and consumed once. Always
on; covers the "tools that reflect their input in their output"
threat.

**Comprehensive pass.** Snapshots every secret currently in the
store and scrubs any value that appears in the result, regardless
of whether the model ever referenced it. Covers the "tools that
emit secrets they were not given" threat (e.g. an MCP debug page
that echoes a token, a file whose contents include a stored value).

The comprehensive pass is guarded by two knobs in `baifo.yaml`:

- `secrets.scrub_tool_results` (default `true`) -- toggles the pass.
  Disable only for debugging when you need raw tool output and
  accept the leak risk.
- `secrets.min_scrub_length` (default `8`) -- minimum byte length a
  stored value must have to be eligible. Short values are skipped
  to avoid catastrophic false positives (a stored `password:
  "1234"` would otherwise redact every `1234` substring in every
  tool result). The trade-off is direct: higher floor → fewer
  false positives, more short secrets bypassed. Real API keys are
  16+ bytes, so the default protects them while letting unrelated
  4-7 byte substrings through.

**False-positive policy.** A stored secret value that legitimately
appears as a substring of tool output (e.g. you're reading a file
that contains the same byte sequence) will be redacted to its
placeholder. Two mitigations:

- The min-length floor cuts the worst tail by raising the bar for
  the substring lottery.
- Real API keys are high-entropy: the probability of a 30-byte API
  key appearing unintentionally in unrelated tool output is
  effectively zero.

If the operator sees the placeholder where they expected the raw
value, the message is "the redactor matched - investigate the
source of the value," not "the tool is broken."

**Limits.**

- If a tool derives another value from the secret (e.g. signs a
  JWT), the derived token is **not** redacted. Same for any
  encoding (base64, URL, JSON-escaping). Documented; not a
  regression.
- Short secrets (below the floor) skip the comprehensive pass.
  They remain protected by the targeted pass when the LLM
  explicitly references them via `${secret:NAME}`.

### Callback ordering

`internal/agent/builder.go` wires (lines 275-281):

```go
BeforeToolCallbacks = [makeBeforeExpand]   // expands ${secret:NAME} before the tool runs
AfterToolCallbacks  = [makeAfterRedact, makeAfterAudit]
```

`makeAfterRedact` runs first, `makeAfterAudit` second. Audit logs the
already-redacted view and never stores raw values. This is the key invariant.

## Log redaction

`internal/logging` wires a `redactedHandler` (a `slog.Handler` decorator)
around the base text/JSON handler. Every log record's attributes pass through
a `Redactor` interface before being written to the sink.

The concrete implementation is `secrets.LogRedactor` (`internal/secrets/logredactor.go`).
`internal/app.New` constructs it with `secrets.NewLogRedactor(nil, cfg.Secrets.LogRedactionEnabled(), cfg.Secrets.EffectiveMinScrubLength())`
before the store is open (nil store = no-op at that point), passes it to
`logging.Init`, and then calls `logRedactor.SetStore(store)` once the store
is ready. `ReloadFromDisk` calls `logRedactor.SetConfig(...)` and `logRedactor.SetStore(...)`
on the same live instance already wired into the handler chain, so there is
no window where log lines go unredacted.

How it works:

- On each `Redact(attr)` call it checks `Store.Generation()` against a cached
  generation counter. When they differ (or on first call), it calls
  `store.Snapshot()` to decrypt all secrets, filters the result by
  `min_scrub_length`, and caches the `name -> raw_value` map. Subsequent
  log lines in the same generation hit the cache and do not decrypt.
- Attribute values are replaced with `${secret:NAME}` via
  `strings.ReplaceAll`. Group-typed attributes are recursed into so
  structured log calls are fully covered. Non-string values are only
  downgraded to string when a secret is actually found in them.
- Gated by `secrets.redact_in_logs` (default `true`). When disabled, or
  when the store is nil, `Redact` returns the attribute unchanged (cheap
  short-circuit).
- Floor: `secrets.min_scrub_length` (default `8`), same knob the
  AfterToolCallback comprehensive pass uses.

`NoopRedactor` still exists in `internal/logging` and is used by tests;
it is no longer wired in production code.

The config fields that affect log redaction:

- `secrets.redact_in_logs` (`*bool`, default `true`).
- `secrets.min_scrub_length` (int, default `8`) -- shared with tool-result scrubbing.
- `secrets.redact_in_tui` (`*bool`, default `true`) -- separate TUI-layer flag.
- `runtime.redact_logs` (`*bool`) exists in the struct (`RedactLogsEnabled()`) but nothing reads it; the log redactor is gated by `secrets.redact_in_logs`, not this one.

## CLI / TUI surface

### CLI

`baifo secrets` subcommand (see `cmd/baifo/secrets_cmd.go`):

- `baifo secrets set <NAME>` -- interactive masked prompt (no echo).
  Optional `--description`. Writes to `.baifo/secrets.yaml`.
- `baifo secrets unset <NAME>` -- removes the entry.
- `baifo secrets list` -- names + descriptions only. Values never
  printed.
- `baifo secrets rotate <NAME>` -- `set` plus the rotation metadata bump.
- `baifo secrets show-block` -- debug helper: prints the secrets block as
  agents would see it (names + descriptions only, no values).

### TUI / facade

The `/secret` family lives in `internal/tui/commands.go::handleSecretCommand`:

- `/secret` or `/secret list` - list names only, never values or descriptions.
- `/secret set [NAME]` - open the masked-input modal (`overlays.SecretPrompt`,
  `internal/tui/overlays/secret_prompt.go`) to set a value.
- `/secret delete NAME` - remove from `secrets.yaml`.
- `/secret encode` - re-seal every plaintext entry (needs `encryption_key`).
- `/secret decode` - unwrap every encrypted entry into plaintext.

Facade methods backing these: `SetSecret`, `DeleteSecret`,
`ListSecretNames`, `SecretsEncrypted`, `EncodeSecrets`, `DecodeSecrets`
(`internal/facade/facade.go`, implemented in `internal/app/secrets.go`).

## Edge cases

- **Secret value contains regex meta-characters.** Irrelevant: both
  expander and redactor use literal `strings.ReplaceAll`, not regex.
- **Two secrets share a value.** Both names get redacted to whichever
  placeholder appears first in iteration order (map iteration is random
  in Go). Acceptable; users should not share values across names.
- **Secret value is a substring of another string in the result.**
  False-positive redaction can happen. Recommendation: use long,
  distinctive values (which API keys already are).
- **Tool returns a streaming response.** The AfterToolCallback applies
  per chunk; the value table is small.
- **Encryption key changed mid-session.** Load happens at startup;
  rotating the key requires re-encrypting `secrets.yaml`. No `rekey`
  command exists. Edit `baifo.yaml`, restart, and re-create each entry.

## Audit

Every tool call is logged into the SQLite `audit` table with the
post-redaction view:

- Args after redaction (placeholders preserved).
- Result after redaction.
- Caller agent ID.
- Timestamp.
- Duration.
- Error string (if any), passed through the same redactor.

Audit logs are safe to ship to a colleague for debugging without
exposing raw secret values.
