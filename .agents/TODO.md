# TODO.md
Open work only. Each item is confirmed missing in the code right now.

## Security / hardening

- Move the Anthropic OAuth token out of the plaintext JSON file into the
  encrypted secrets store. Today `internal/providers/anthropic/oauth.go` persists
  the `access_token` + `refresh_token` to `~/.config/baifo/anthropic_oauth.json`
  at mode `0600` (written by `RunOAuthFlow` and re-written by `OAuthTransport`
  after each refresh). This is inconsistent with the rest of baifo, where API
  keys live encrypted in the SQLite secrets store (`internal/secrets`): a
  long-lived `refresh_token` in plaintext is more sensitive than an API key and
  bypasses the redact-in-logs / referenced-by-name pipeline. Proposed shape: give
  `OAuthTransport` a small `tokenStore` interface (get/set) backed by the secrets
  store instead of a file path, so it never touches the filesystem. Deferred by
  owner decision (clean the feature first, migrate later).

## Low / nice-to-have

- Re-evaluate migrating the StripStaleThoughts wrapper from the provider
  decorator pattern to an ADK plugin (`BeforeModelCallback`, prepended like
  the context-trim guardrail). Today the strip lives as a `model.LLM`
  decorator applied in `internal/providers/{anthropic,gemini,openai}` via
  the shared `providers.WrapStripThoughts`. Arguments for migrating: single
  wiring point, declarative pipeline order ([strip, trim, contextguard]),
  one consistent answer to "where does request hygiene live". Arguments
  against (why it was NOT done in jun 2026): the decorator protects EVERY
  consumer of a provider model (e.g. the contextguard summariser receives a
  bare `model.LLM`, no runner plugins), and the strip is a correctness fix
  (400 "Corrupted thought signature" after provider switch) rather than
  optional policy, so it wants to be on the inevitable path. Revisit if/when
  every model consumer goes through a runner with a shared plugin pipeline.

- Decide the streaming-bar spinner behaviour when the active interlocutor is a worker.
  Today `renderStreamingBar` keys only off `m.streamCancel != nil`, so a live root stream
  keeps painting "thinking…" under a worker chat even though the user is not looking at the
  root. Open question: hide the bar when `m.activeInterlocutor != rootInterlocutor`, or leave
  it as a background-activity cue. Lives in `internal/tui/model.go` (`renderStreamingBar`).

- Status-bar "newer on disk" indicator after a config change detected by the watcher
  (`internal/watcher`). No UI signal exists today.

- Reload error toast when `ReloadFromDisk` fails in the background. The error is now
  logged via `slog.Error("config reload failed", "err", err)` in `startWatcher`, but no
  TUI toast exists; the user has to tail the log file to see watcher-triggered reload
  failures.

- `WARN` log when A2A server binds to a non-loopback address. No such check in
  `internal/server` or `cmd/baifo/server_cmd.go`.

- Live refresh of the `/session` overlay when the auto-titler sets a new title. Today
  the updated title is only visible on next open; no broadcast channel from
  `internal/app/title.go` to the TUI overlay.

- Per-titler model override (`runtime.titler.provider/model`) so title summarisation
  can use a cheap model instead of the chat's root LLM. `internal/app/title.go`
  hardcodes the root's provider/model.

- Tool-card `view` link in the worker chat that jumps to the Workers overlay.
  No link rendering or navigation exists in the tool-card rows.

- `teatest`-driven smoke tests for the TUI. Current tests dispatch `tea.Msg`s directly;
  no `teatest` integration exists in `internal/tui/`.

- `baifo secrets rekey` to rotate the encryption key in place. No CLI subcommand or
  store method exists in `internal/secrets/` or `cmd/baifo/`.
