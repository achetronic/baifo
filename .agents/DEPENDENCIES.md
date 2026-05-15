# DEPENDENCIES.md
Operational guide for adding, upgrading and pinning third-party
dependencies in baifo. Read before touching `go.mod`. Skim before
running `go get -u`.

baifo lives in a Go module where two parallel Charm ecosystems
coexist as transitive dependencies: the legacy
`github.com/charmbracelet/...` and the new `charm.land/...`. Most
"why did this fail to compile" stories in this repo come from
mixing the two carelessly. The rest of this document is the
playbook to keep them apart.

## The two ecosystems

| Namespace | Version | What it is | baifo uses it directly? |
|---|---|---|---|
| `charm.land/bubbletea/v2` | v2.0.6 | Event loop, the BubbleTea v2 stack | yes |
| `charm.land/bubbles/v2` | v2.1.0 | UI components (textarea, viewport, spinner, etc.) | yes |
| `charm.land/lipgloss/v2` | v2.0.3 | Styling, layout | yes |
| `charm.land/catwalk` | v0.44.3 | Embedded LLM model catalogue (model ids, context windows, costs, reasoning levels). Used directly by `internal/tools/models` (`list_models`) and `internal/tui/overlays` (model + reasoning autocomplete). `internal/agent/contextguard.go` uses `adk-utils-go/plugin/contextguard`, which in turn depends on catwalk. | **yes (direct)** |
| `github.com/charmbracelet/glamour` | v0.7.0 | Markdown rendering for the chat | yes |
| `github.com/charmbracelet/ultraviolet` | v0.0.0-20260416155717-489999b90468 | Input/output engine that **bubbletea v2 itself uses internally** | indirect |
| `github.com/charmbracelet/colorprofile` | v0.4.3 | Terminal colour profile detection, shared by both v1 and v2 | indirect |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | Low-level ANSI parsing | indirect |
| `github.com/charmbracelet/x/etag`, `x/term`, `x/termios`, `x/windows` | various | Low-level utility libraries | indirect |

The **clean rule**: baifo depends directly only on `charm.land/v2`
+ `glamour v0.7` + `catwalk`. Everything else is indirect.

> **Toolchain note.** `charm.land/catwalk` tracks recent Go releases
> closely: v0.44.x requires Go >= 1.25.10. `go.mod` pins `go 1.25.10`
> to match, and CI's `go-version: '1.25'` resolves to the latest 1.25.x
> patch, so it stays compatible. Bumping catwalk again may require
> bumping the `go` directive too, check the version's go.mod first
> (`go mod download -json charm.land/catwalk@<v>`).

## What can NOT be done

### Don't `go get -u ./...` indiscriminately

Most of the "Charm v2" libraries pin their dependencies on a
specific minor of `x/ansi`. A naive global upgrade brings them out
of sync and the build breaks instantly with errors like:

```
b.Strikethrough, wants (bool), got ()
b.DoubleUnderline, undefined
```

This is what happens when `x/ansi` is one minor ahead of where the
consuming library expects it: the ANSI parser is missing methods the
caller expects to find.

The safe pattern is to upgrade one module at a time and
`go build ./... && go test ./...` after each step.

### Don't pull anything from the legacy `charmbracelet` namespace
### that isn't already in `go.mod`

`github.com/charmbracelet/huh`, `github.com/charmbracelet/wish`,
etc. are **not** drop-in compatible with the v2 stack. They pin
older `x/ansi` versions that clash with what `bubbletea/v2` pulls.
Importing them is not "just add the package", it can require
pinning every Charm package to a compatible older version,
defeating the v2 stack we already have.

If you absolutely need a v1 package, treat it the same way we did
glamour: pin to a version that predates the migration to `x/ansi`
(typically anything before late 2024 in glamour's case). Test the
compile carefully.

## What CAN be done

### Adding a new direct dependency

1. Run `go get pkg@latest` (or @specific-version if you have one).
2. `go build ./...`, must succeed cleanly.
3. `go test ./...`, must stay green.
4. `go mod tidy`, clean up the `indirect` block.

If any step fails:

- Look at the error. If it mentions `x/ansi` or
  `charmbracelet/lipgloss`, this is almost certainly a Charm
  v1 vs v2 split.
- Try older versions of the new dependency until you find one
  that predates the migration. `go list -m -versions <pkg>`
  lists every published version.
- If no version works, look for an alternative library or write
  the feature directly against `x/ansi` / `lipgloss/v2`.

### Upgrading a single dependency

`go get pkg@new-version` followed by `go build ./...`. If it
fails, `go get pkg@previous-version` to roll back. Don't tidy
until the new version works, `tidy` will pin transitives that
may make the rollback harder.

### Removing a dependency

Remove every import in the codebase, then `go mod tidy`. Don't
hand-edit the `require` block, let `tidy` figure it out.

## The glamour example

`glamour v1.0.0` declares `github.com/charmbracelet/x/ansi v0.10.2`
in its go.mod. baifo already has `x/ansi v0.11.7` pulled by
bubbletea v2. Go picks the higher version (0.11.7). Glamour v1
also pulls `charmbracelet/lipgloss v1.x` and `x/cellbuf v0.0.13`,
where cellbuf was compiled against an `x/ansi` between 0.10.2 and
0.11.7, expecting methods like `Strikethrough(bool)` and
`DoubleUnderline` that don't exist in 0.11.7. Result: it won't compile.

Solution applied: downgrade glamour to **v0.7.0**, which predates
the migration to `x/ansi` and uses `muesli/termenv` +
`muesli/reflow` directly. No interaction with the v2 stack. We
lose newer glamour features (better table rendering, etc.) but
gain a stable build.

Cost of glamour v0.7.0:
- No `WithChromaFormatter` for syntax highlighting in code blocks
  (acceptable: the default ASCII style is fine for our use).
- Older emoji DB (acceptable: rarely matters in the chat).
- API surface we use (`NewTermRenderer`, `WithStyles`,
  `WithWordWrap`, `WithEmoji`) is identical, so the migration
  back to a newer version is a one-line change if Charm
  publishes a `charm.land/glamour/v2`.

## Watchlist

These are the indirect dependencies whose upgrades have caused
us pain or could cause pain in the future. Don't bump them
unless you really need to:

- **`github.com/charmbracelet/ultraviolet`**, bubbletea v2's
  internal I/O engine. Major version bumps tend to flow into
  bubbletea v2 itself, which means we'd have to upgrade
  bubbletea v2 in lockstep.
- **`github.com/charmbracelet/x/ansi`**, the ANSI parser. Both
  ecosystems depend on it. Mismatched versions are the classic
  source of "missing method" errors. Pinning is essential.

## Decision: when do we migrate glamour back to a newer version?

When **all three** of these conditions hold:

1. Charm publishes a `charm.land/glamour/v2` (or any v1 of glamour
   that depends on the same `x/ansi` minor as `bubbletea/v2`).
2. The migration is a one-line change in `markdown.go::buildMarkdownRenderer`.
3. We have a concrete need for one of the newer glamour features
   (live syntax highlighting, better tables, etc.).

Until then, glamour v0.7.0 covers the use case. Don't migrate "to
stay current"; current with Charm v2 is what matters.

## Quick reference: when something breaks

| Symptom | Likely cause | Fix |
|---|---|---|
| `b.Strikethrough, wants (bool), got ()` | `x/ansi` desynchronised from a consuming library | Roll back to last working commit; reintroduce changes one at a time |
| `package X is not in std` | Module not downloaded | `go mod download` |
| `module declares its path as: github.com/A but was required as: github.com/B` | Repo moved, transitive still on old path | `go mod tidy`, then check no test file pinned the old path |
| `undefined: foo.Bar` after a Charm upgrade | Charm renamed a symbol between v1 and v2 | Roll the upgrade back, audit imports |
| Build works, tests fail with terminal weirdness | Terminal library mismatch pulled by an indirect | `go mod why github.com/charmbracelet/x/ansi` (and similar) to find who's pulling what |

## Quick reference: `go.mod` health

```bash
go list -m all | grep charm   # all charm deps and versions
go mod why <pkg>              # who is pulling this transitive in
go mod graph | grep <pkg>     # dependency edges through <pkg>
go mod tidy                   # housekeep the require block
go build ./... && go test ./... && go vet ./...   # full check
```
