# OVERLAY_STYLE.md

The design language for modal overlays in the TUI. Read this
before adding a new overlay. When in doubt, copy an existing one
(Sessions is the shortest) and adapt.

This document is descriptive of the code as it stands -- every
primitive named here exists in `internal/tui/`. When code and doc
disagree, code wins and the doc is stale.

## Two invariants: non-negotiable

These are the two rules every modal in baifo must satisfy. They
exist because, without them, modals drift into half a dozen
slightly-different looks and the TUI feels like a patchwork.

### 1. One look. Always.

Every modal renders through the **single** primitive
`renderModal(theme, opts, body, screenW, screenH) string`
(declared in `internal/tui/overlay_chrome.go`). The primitive
owns:

- **Border**: rounded, `theme.PanelBorderFocused()` (accent).
- **Background**: no explicit `Background(...)` set on the modal frame. The inner area renders against the terminal's default background (the dark app background shows through). Outside the modal rectangle the compositor leaves the body cells intact.
- **Title band**: `theme.Accent.Subtle` background, `theme.Accent.Primary` bold
  foreground, `Padding(0, 1)`. Hint sits right-aligned in the band in
  `colorTextDim` tone.
- **Inner padding**: `0` rows top/bottom (handled by the border), `1` col left/right via `Padding(0, 1)`.
  Content width is `w - 2 (border) - 2 (padding) = w - 4`.
- **Footer**: `theme.FaintText()`, separated from the content by a `─` rule whenever either `Footer` or `ConfirmPrompt` is non-empty. When `ConfirmPrompt` is set it overrides `Footer` and renders in error-bold.

**No modal may**:
- Hardcode an `lipgloss.Color("147")` (or any literal palette
  number) for its frame. The theme provides every needed
  accessor.
- Redefine its own title bar. Use the primitive.
- Use `Reverse(true)` for selection. Use the cursor glyph (`› `) in
  accent colour via `theme.AccentText()`, same as everywhere else.

When you genuinely need a new variant (e.g. a destructive
confirmation that should pulse red), extend the primitive's
options struct, **don't fork the primitive**.

### 2. Centered. Always.

Every modal is centred horizontally and vertically over the
existing TUI body via `lipgloss v2`'s `Compositor` + `Layer`
model. The body stays visible underneath (no whitespace fill
that erases the chat behind). The primitive does this for you;
the caller only supplies the modal content + the screen size.

**No modal may**:
- Replace the body with itself. That includes the old
  "full-screen overlay" pattern where `body = renderHelp(…, m.width, m.height)`
  filled the whole screen. Help, Sessions, Workers
  -- they're all centred modals now.
- Anchor itself anywhere except the centre. The composer
  autocomplete strip (`palette.go`) is the one exception: it is
  **not a modal**, it is a contextual popup tied to the
  cursor position in the writing box. Everything else: centre.
- Use `lipgloss.Place(...)` for positioning: `Place` fills the
  whole area with a whitespace background, erasing what was
  below. Always use `Compositor` + `Layer` through the primitive.
  Note: `Canvas.Compose` does NOT honour `Layer.X/Y/Z` -- use
  `NewCompositor(...)` instead.

The cursor-anchored completer inside the embedded editor
(`components/editor/completer.go`) is also not a "modal" -- it's
a per-component autocomplete owned by the editor and positioned
relative to the cursor, not the screen. Same logic as the
composer strip: tied to a cursor position, opted out of
centring on purpose.


## Where overlays live

baifo has **one** main view (the chat) and a fixed set of slash
commands that open transient modal overlays on top of it. There
are no tabs: every alternate view is an overlay.

| Slash command | Renderer | File |
|---|---|---|
| `/session` | `renderSessions` | `internal/tui/tabs_views.go` |
| `/worker` | `renderWorkers` | `internal/tui/tabs_views.go` |
| `/help` / Ctrl+/ / F1 | `renderHelp` | `internal/tui/components.go` |

Plus three special-purpose overlays that don't follow the
"list of things" pattern but still share the chrome rules:

| Trigger | Renderer | File |
|---|---|---|
| `/secret set` etc. (masked input) | `overlays.SecretPrompt` wrapped by `viewSecretPromptOverlay` | `internal/tui/overlays/secret_prompt.go`, `internal/tui/secret_overlay.go` |
| Embedded YAML editor | `editor.Model` | `internal/tui/components/editor/` |

The chrome primitives below cover the "list" overlays. The special
ones use the same colour palette and key bindings, but their layout is
dictated by their content (a text input, an editor with line numbers).


## The chrome: `renderModal`

`renderModal(theme Theme, opts overlayOpts, body string, screenW, screenH int) string`
lives in `internal/tui/overlay_chrome.go`. It is the **only**
place every overlay in the table above goes through. It sizes the
frame to the content, calls `renderOverlay` (the inner fixed-size
painter), then composes the result over `body` centred on screen
using `lipgloss.NewCompositor` + `Layer`.

### Anatomy

```
╭────────────────────────────────────────────────────────────────╮
│ ████████████████ Sessions ████████████████████████████████████│  ← title band
│                                                                │  ← blank
│   › ● Onboarding chat                                         │  ← selected row (cursor + marker + label)
│     ● Refactor planning                                        │  ← active marker, no cursor
│       another session                                          │  ← plain row
│                                                                │
│  ────────────────────────────────────────────────────────────  │  ← separator
│  ↑/↓ select · ⏎ resume · n new · d delete · r rename · esc close  │  ← footer
╰────────────────────────────────────────────────────────────────╯
```

### Mandatory rules

1. **Border**: always focused (`PanelBorderFocused()` → rounded,
   accent.Primary). An overlay always has the focus by definition.
2. **Padding**: `0, 1` (from `PanelBorderFocused()`).
   Content width is `w - 2 (border) - 2 (padding) = w - 4` and
   `overlayContentSize(w, h)` returns exactly that.
3. **Title band**: full-width row at the top with `accent.Subtle`
   as background and `accent.Primary` bold as foreground. The hint
   (e.g. `(encrypted)`) sits right-aligned in `text_dim`.
4. **One blank line between the title band and the body.**
5. **Footer separator**: `─` faint, full inner width, painted only
   when the footer or a confirm prompt is non-empty.
6. **Footer line**: keybindings faint (`FaintText`), separated by `·`. The
   convention is `↑/↓ select · ⏎ <verb> · <letter> <verb> · esc close`.
7. **Destructive confirmation**: replaces the footer text with an
   error-coloured bold prompt (`opts.ConfirmPrompt`). No nested
   overlay, ever.

### `overlayOpts` fields

```go
type overlayOpts struct {
    Title         string // bold accent on the band; required
    Hint          string // dim text on the right of the band; optional
    Content       string // multi-line, already styled
    Footer        string // keybindings hint; faint
    ConfirmPrompt string // replaces Footer when non-empty; error tone
    MinWidth      int    // 0 = use defaultModalMinWidth (32)
    MaxWidth      int    // 0 = use defaultModalMaxWidth (96)
    MinHeight     int    // 0 = use defaultModalMinHeight (6)
}
```

`Content` is whatever the overlay decided to render -- usually a
call to `renderList`, sometimes raw text (Help is a two-column
table).


## The list primitive: `renderList`

Lives in `internal/tui/overlay_chrome.go`. Every overlay that
shows a list of selectable rows uses it. **Do not invent your own
row renderer** unless the overlay has a fundamentally different
shape (e.g. the editor).

### Anatomy

```
› ● my-session              now    a3f9-…   ← selected row
  ● yesterday session       18:02  b2c3-…   ← active marker only
    another session         today   ...     ← plain row
```

### Row anatomy

- Col 1-2: cursor -- `› ` accent on the selected row, two blanks
  otherwise (column width is constant for alignment).
- Col 3-4: marker -- `●`/`○` in severity colour when the row has
  one (active session, running worker), two blanks otherwise.
- Col 5+: label, coloured by entity (`EntityKind` field). Empty
  kind → primary text.
- Suffix: `  ` (2 spaces) + faint text. Used for timestamps, ids,
  brief status. Optional.

### `listItem` fields

```go
type listItem struct {
    Label       string // shown after the cursor + marker columns
    EntityKind  string // "root" | "static" | "dynamic" | "skill" | "mcp" | "provider" | "secret" | "session" | "fact" | "" (neutral)
    MarkerGlyph string // "" hides the marker column
    MarkerKind  string // "ok" | "warning" | "error" | "info" | "" (dim)
    Suffix      string   // faint text after the label
    MetaLines   []string // extra faint rows under the label, indented; selection moves by item
}
```

### Empty state

`renderList` accepts an `emptyHint` string and uses it when
`items` is empty:

```go
renderList(theme, []listItem{}, -1, "no sessions stored yet", listOverlayMinRows, listOverlayContentWidth)
```


## Inline keystroke references: `keycap`

When an overlay's body text mentions a literal key the user is
meant to press, wrap it with `keycap`:

```go
"press " + keycap(theme, "n") + " to add your first MCP"
```

Renders as `press [n] to add your first MCP` with `[` and `]` in
faint and the letter in accent. Use it consistently -- that's how
prose and the footer hints stay coherent.


## Keyboard contract

Every overlay implements at minimum the keys below, in the same way. If
your overlay can't, talk to the maintainers; you probably don't
want a new overlay, you want something else (a toast, a chat
message, an inspector).

| Key | Behaviour |
|---|---|
| `Esc` | Close the overlay. Returns to the chat. |
| `↑/↓` | Move the selection. |
| `PgUp/PgDn/Home/End` | Page / jump moves. Sessions and Workers implement these. |
| `⏎` | Primary action on the selected row (resume session / open worker chat). |
| `n` | Start a new entry (Sessions: new session). |
| `d` | Delete / destructive action. Sessions uses `d` (delete). Workers uses `k` (kill) and `c` (collect). All gated by `y/N` confirmation in the footer. |
| `r` | Rename. Sessions: prints `rename via /session rename <id> <new title>` as a system message (the rename UX is not wired into the overlay; the key is caught so it is not silently swallowed). |

The destructive `y/N` flow is the same everywhere:

1. User presses the destructive key (`d`, `k`, or `c`). The overlay sets a `confirm<Thing>` field on
   the Model with the target id.
2. The renderer sees the confirm field is non-empty and replaces
   the footer text with a prompt like `delete <thing> <id>? this cannot be
   undone (y/N)` in error-bold via `overlayOpts.ConfirmPrompt`.
3. `y` runs the action and clears the confirm field. `n` /
   `esc` clears it without action.


## Colour palette in use

All colours come from `internal/tui/theme.go`. Don't hardcode
hex anywhere in an overlay renderer.

### Frame and chrome

| Token | Where |
|---|---|
| `accent.Primary` | Title-band foreground, focused border, cursor `›` |
| `accent.Subtle` | Title-band background |
| `colorTextDim` | Hint on the title band |
| `colorTextFaint` | Brackets in `keycap`, footer text, separator `─`, suffix text on list rows |
| `colorText` | Plain row label without entity colour |

### Per-entity (rows + glyphs)

| Entity | EntityKind tag | Colour |
|---|---|---|
| root agent | `root` | orange `#F2922B` (sol) |
| static agent | `static` | golden `#C98A4B` (arcilla dorada) |
| dynamic worker | `dynamic` | red `#C2412B` (lava viva) |
| skill | `skill` | green `#7FA650` (verde tunera) |
| mcp | `mcp` | tan `#D9A066` (arcilla clara) |
| provider | `provider` | brick `#B5533A` (lava clara) |
| secret | `secret` | orange `#F2922B` (sol, same as root) |
| session | `session` | gray `#A89B86` (cal apagada) |
| fact | `fact` | gray `#A89B86` (same as session) |

### Severity (markers, confirm prompts)

| Severity | Use |
|---|---|
| `StatusOK` | active marker (●) on the session that's currently in use |
| `StatusWarning` | running workers (●) |
| `StatusError` | failed / killed workers, confirm-prompt text |
| `StatusInfo` | informational; uses `#C98A4B` (arcilla dorada, warm amber) |
| `DimText` | neutral / idle marker |


## The state machine of a list overlay

This is the pattern. Every "list overlay" (Sessions, Workers)
follows it. Copy it verbatim:

1. **Model fields**: an `<overlay>Open bool`, a `<overlay>Sel int`
   selection cursor, and one or more `<overlay>Confirm<Action> string` fields for
   destructive prompts (Sessions has `sessionsConfirmDelete`;
   Workers has `workersConfirmKill` and `workersConfirmCollect`).

2. **Slash command** in `commands.go`: set `<overlay>Open = true`
   in the `slashResult`. The Model's `submitComposer` flips the
   model flag and refreshes the list right before rendering.

3. **Update key handler** in `overlays.go`: one function per
   overlay, e.g.
   `handleSessionsOverlayKey(msg) (Model, Cmd)`. The handler is
   gated by `if m.<overlay>Open { return m.handle...Key(msg) }`
   at the top of `Model.handleKey`. Implements the key
   contract above.

4. **Renderer** in a file matching the overlay (Sessions and Workers use
   `tabs_views.go`): builds a `[]listItem` from the Model's data, calls
   `renderList`, hands the result to `renderModal` with title, footer,
   and confirm prompt.

5. **Mouse**: not wired today for overlays. Click only switches
   focus between chat and composer. Adding overlay mouse support
   means parsing the click row in `chat_focus.go::regionAt` and
   routing to the overlay's row table.


## How to add a new "list of X" overlay

If you want to introduce a new overlay (say, `/audit` for the
audit log), do these things in order:

1. **Decide on the action vocabulary first.** Plural slash command
   (`/audit`), sub-verbs go through the handler (`/audit clear`),
   keyboard contract on the overlay. Coherence with the existing
   commands is what makes this scale.

2. **Add the `slashResult` field**: `openAuditOverlay bool` in
   `commands.go`. Add the dispatch case `"/audit"` and the
   handler `handleAuditCommand` mirroring `handleSessionsCommand`.

3. **Add the Model fields**: `auditOpen bool`, `auditSel int`,
   and `auditConfirmClear string` (or whatever destructive
   shortcut you support). Initialise in `NewModel`.

4. **Add the key handler** in `overlays.go`:

   ```go
   func (m Model) handleAuditOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) { ... }
   ```

   Gated by `if m.auditOpen { return m.handleAuditOverlayKey(msg) }`
   at the top of `handleKey`. Implement Esc / ↑↓ / Enter / n / d.

5. **Add the renderer** in `internal/tui/audit_view.go` (or
   wherever fits): build `[]listItem`, call `renderList`, then
   `renderModal`. Use `keycap(theme, "n")` in placeholders.
   Use the per-entity colour palette via `EntityKind`.

6. **Wire the render in `Model.View`**: `renderModal` takes the current
   body and returns the fully composed view. Pattern:

   ```go
   if m.auditOpen {
       return renderAudit(m.theme, m.auditEntries, m.auditSel,
           m.auditConfirmClear, body, m.width, m.height)
   }
   ```

   where `renderAudit` ends with a `renderModal(...)` call that receives
   `body` as the layer underneath.

7. **Add the entry to `/help`** in the right `helpSection` of
   `components.go::helpSections` (Writing box · Chat transcript ·
   Global · Slash commands), so it lands under the scope it belongs to.

8. **Tests**: minimum two -- one for the slash command setting the
   overlay flag, one for the navigation contract (Esc closes,
   Enter triggers the action, the destructive shortcut sets the
   confirm field).


## What NOT to do

- **Don't draw your own border.** `renderModal` is the only
  entry point. If your overlay needs an unusual frame (a
  full-screen editor), still use `renderModal` for the title
  band and the footer; only the inner content area is custom.

- **Don't hardcode colours.** Every colour comes from `theme`.

- **Don't reinvent the cursor / marker columns.** `renderList`
  is the contract.

- **Don't open a nested confirm overlay.** Use the
  `ConfirmPrompt` field in `overlayOpts`.

- **Don't switch focus inside an overlay.** Mouse clicks while
  an overlay is open are dropped on purpose (`regionAt` returns
  `regionOther`). The overlay owns the keyboard; clicking the
  chat behind it does not switch focus.

- **Don't pre-trim or pre-wrap your content beyond
  `listOverlayContentWidth`** (or the equivalent for your overlay).
  Lipgloss will handle the right edge as long
  as you measure with `lipgloss.Width()`. Plain monospace +
  styled spans work fine.

- **Don't put information the user needs to read for a long time
  in an overlay.** Overlays are transient ("pick something /
  trigger an action / close"). For sustained reading move it to
  the chat as a `MessageSystem` row or open an editor.


## Quick reference: the building blocks

```go
// Primary entry point. Sizes the modal, calls renderOverlay, composes over body.
renderModal(theme Theme, opts overlayOpts, body string, screenW, screenH int) string

// Inner fixed-size frame painter. Use renderModal instead unless you need a fixed size.
renderOverlay(theme Theme, opts overlayOpts, w, h int) string

// Vertical, navigable list. Used inside overlayOpts.Content.
renderList(theme Theme, items []listItem, selected int, emptyHint string, maxRows, contentWidth int) string

// Inline "press X" keystroke reference. Used in body and footer.
keycap(theme Theme, key string) string

// Theme accessors every renderer should use.
theme.AccentText()         // accent.Primary foreground
theme.FaintText()          // colorTextFaint foreground
theme.DimText()            // colorTextDim foreground
theme.PrimaryText()        // colorText foreground
theme.EntityText(kind)     // per-entity colour
theme.StatusOK()           // green (#7FA650)
theme.StatusWarning()      // orange (#F2922B)
theme.StatusError()        // red (#C2412B)
theme.StatusInfo()         // warm amber (#C98A4B)
theme.PanelBorderFocused() // rounded accent border, padding 0/1
```

That's it. If a new overlay needs anything not covered by the
above, discuss it. Don't sneak in a new colour or a new row
shape -- the value of the design system is consistency.
