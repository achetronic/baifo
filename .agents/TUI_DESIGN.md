# TUI_DESIGN.md
Complete TUI specification: identity, palette, layout, components,
responsive rules, slash commands, and interaction patterns. Single
source of truth, `internal/tui/theme.go` mirrors this file.

This document is **descriptive**. All described behaviour is implemented.

## Identity

**Name**, `baifo`, always lowercase, no caps even at the start of a
sentence in CLI output.

**Pronunciation**, *ái-do*.

**Etymology**, *Agent I Do* / "yo hago". Implicit: baifo is the one
that *does* things, the user *decides*.

**Splash logo** (shown briefly on startup). Two tiers:

- Wide terminals (>= 36 cols): five-row large block logo.
- Narrower (>= 24 cols): compact two-row small logo.

Small logo (two rows, `splashLogoSmall`):

```
   █▄  ▄▀█ █ █▀▀ █▀█
   █▄█ █▀█ █ █▀  █▄█
   the agent harness
```

Large logo (five rows of full-block glyphs, `splashLogoLarge`), centred
and bold in the accent colour. Tagline is one of four strings picked at
random each launch (`splashTaglines`).

For terminals narrower than 24 cols the splash renders just `baifo` in
accent colour.

## Palette

Single dark theme with a fixed **Canarias** identity, black volcanic
picón sand backgrounds, lime-wash off-white text, canarian clay borders,
and sun orange / lava red as the warm accents. The palette is part
of baifo's identity: `NewTheme()` takes no arguments and the sole
accent is `canariasAccent`. Hex values are
exact; lipgloss receives them as `lipgloss.Color("#xxxxxx")`. A standalone
swatch board lives at `docs/canarias-palette.html`.

### Base (fixed)

| Token | Hex | Tone | Use |
|---|---|---|---|
| `bg` | `#211C18` | dark picón | Main background |
| `bg_alt` | `#2B2520` | picón panel | Panels, cards, popovers |
| `bg_hover` | `#3A322E` | picón | Hover / selected row |
| `bg_focus` | `#473C34` | lightened picón | Active panel highlight |
| `border` | `#5E3119` | canarian clay | Default border |
| `border_focus` | `#F2922B` | sun (accent) | Border of the focused panel |
| `text` | `#E8DDCB` | lime-wash off-white | Primary text |
| `text_dim` | `#A89B86` | faded lime-wash | Secondary text, labels |
| `text_faint` | `#6E6356` | faint lime-wash | Timestamps, hints, placeholders |
| `success` | `#7FA650` | prickly-pear / aloe green | Worker done, OK badges |
| `warning` | `#F2922B` | sun | Worker running, in-progress |
| `error` | `#C2412B` | bright lava | Worker failed, errors |
| `info` | `#C98A4B` | golden clay | Tool call info events |
| `lava` | `#7E2114` | pure cooled lava | Reserved: decorative borders/accents only (too dark for text) |

### Accent (fixed)

A single bundle of three values, no presets, no override:

| Accent | primary | focus | subtle |
|---|---|---|---|
| `canarias` | `#F2922B` (sun) | `#F6AC56` (light sun) | `#5E3119` (clay) |

### Entity colours (fixed)

Kept within the Canarias family so badges never clash with the warm
picón background the way generic blues/violets would.

| Entity | Colour | Hex |
|---|---|---|
| Root agent | sun | `#F2922B` |
| Static agent | golden clay | `#C98A4B` |
| Dynamic worker | lava | `#C2412B` |
| Skill | aloe green | `#7FA650` |
| MCP | light clay | `#D9A066` |
| Provider | light lava | `#B5533A` |
| Secret | sun | `#F2922B` |
| Session | faded lime-wash | `#A89B86` |
| Fact | faded lime-wash | `#A89B86` |

Entity colours are stable; the accent is just what we paint user
actions with. Root agent and Secret both share the sun tone, that is
fine, they never render adjacent in the same badge row. Fact shares
the Session tone: facts are long-term notes, not active entities,
so a quiet colour fits.

## Glyphs

One glyph set, pure ASCII everywhere: it renders on every terminal
with every font. The one companion character is the selection rail
(▌), a CP437 half block chosen at full cell height so stacked
rows read as one solid bar instead of a broken pipe.
Components must use `theme.Glyph(name)` and never hardcode a glyph.

| Name | Glyph |
|---|---|
| `root` | `R` |
| `static` | `S` |
| `dynamic` | `D` |
| `skill` | `k` |
| `mcp` | `m` |
| `provider` | `p` |
| `secret` | `*` |
| `running` | `~` |
| `done` | `OK` |
| `failed` | `x` |
| `idle` | `.` |
| `chevron` | `>` |
| `bullet` | `*` |
| `arrow_right` | `->` |
| `arrow_left` | `<-` |
| `gear` | `*` |
| `search` | `?` |
| `clock` | `t` |
| `lock` | `#` |
| `fact` | `f` |
| `expanded` | `v` |
| `compact` | `><` |
| `warn` | `!` |

## Spacing & borders

- Padding inside any panel: `0` vertical, `1` horizontal (lipgloss `Padding(0, 1)`).
- Gap between sibling panels: `1` column.
- Borders: `lipgloss.RoundedBorder()` everywhere, no exceptions.
- Focus indicator: the focused panel's border switches to
  `accent.primary`.

## Layout

The screen is divided into four zones, top to bottom:

```
┌────────────────────────────────────────────────────────────────────┐
│ 1.  Tabs (logo / header row)                                       │
├────────────────────────────────────────────────────────────────────┤
│                                                                    │
│ 2.  Chat (the only main view; overlays float on top of it)         │
│                                                                    │
├────────────────────────────────────────────────────────────────────┤
│ 3.  Writing box (composer)                                         │
├────────────────────────────────────────────────────────────────────┤
│ 4.  Footer with contextual chips                                   │
└────────────────────────────────────────────────────────────────────┘
       overlays, Sessions / Workers / Help / Editor /
                  Secret prompt (modal, opened by slash commands)
```

Zone 1 is named `Tabs` in code. No tab strip is currently rendered;
every alternate view is a modal overlay invoked through a slash command.
See `.agents/OVERLAY_STYLE.md` for the overlay design system.

### Zone 1, Tabs (header)

Four rows tall (`headerHeight = 4`). `Zones.Tabs` in layout.go:

```
                                                              ← row 0: blank padding
  █▄  ▄▀█ █ █▀▀ █▀█   ███▓▓▒▒░░                             ← row 1: logo + fade
  █▄█ █▀█ █ █▀  █▄█   ███▓▓▒▒░░                             ← row 2: logo + fade
                                                              ← row 3: blank padding
```

Block-glyph "baifo" logo (`headerLogo`, same glyphs as `splashLogoSmall`),
two cols of left padding (`headerLogoPaddingLeft = 2`). To the right of
the logo, after a gap of `headerLogoFadeGap = 4` cols, `buildHeaderFade`
paints a colour-fading ribbon: it steps through `headerFadeRamp`
(█ ▓ ▒ ░ space, each in multiple copies) while keeping the foreground
fixed at `accent.Primary`. The dissolve happens through the glyph's
shrinking mass, not by interpolating toward a background colour, so the
ribbon blends into any terminal background. Two cols of right padding
(`headerLogoPaddingRight = 2`) terminate the ribbon. No bottom rule:
the chat viewport's own border already provides the chrome separator.

When the terminal is narrower than
`headerLogoMinWidth + headerLogoPaddingLeft` cols, the logo is
hidden and the four rows render blank, the layout stays stable,
just without the brand mark.

The zone is named `Tabs` in `layout.go` (`Zones.Tabs = headerHeight`) and
has `TabsCollapsed()` / `SidebarVisible()` hooks that report responsive
mode. In the current implementation the zone renders the logo and fade
ribbon only; no tab strip or breadcrumb is shown. The chip footer at the
bottom of the screen carries the contextual state.

### Zone 2, Chat

The main and only view: a scrollable transcript of the
conversation with the active interlocutor (root by default, or any
worker the user switched to via `/worker talk <id>` or by clicking the
worker in the `/worker list` overlay).

Message types and their visual style:

| Type | Author label | Content |
|---|---|---|
| User message | `you · HH:MM` in `text_dim` | text in `text` |
| Root reply | `root · HH:MM` in sun orange `#F2922B` (`colorRootAgent`) | text in `text` |
| Tool call/result | one dimmed line per tool, paired by `CallID` | see below |
| System notice | italic, `text_dim`, centred | text |

**Tool rows** are a single dimmed line by default, with an
optional expanded block when the user toggles `Expanded` via Enter
on the focused row or by clicking the row:

```
 ▸ filesystem.read_file                                          running
 ✓ filesystem.read_file                                            12:34
 ✗ exec                                                    exit 1 · 12:34
```

When expanded, the args + result are printed as labelled key/value pairs enclosed in a rounded-border box. Both the border foreground and border background are `colorBGFocus` so the border disappears into the fill, creating a continuous solid surface. Text inside uses `PrimaryText` foreground on `colorBGFocus` background. The box has a left margin of 1 column, horizontal padding of 2, and vertical padding/margin of 1. Key-value pairs inside are separated by a blank line for readability. See `internal/tui/chat.go::specialExpandedBox` for the layout.

**Persistence of expanded tools**: If `runtime.chat_keep_tools_expanded` is enabled (set in baifo.yaml), expanded tool rows remain open when navigating the selection away or focusing the composer. If disabled, they collapse automatically upon losing focus.

**Focus and selection**: the chat has its own focus, distinct
from the writing box. Click on the chat or use `↑/↓` while no
text is in the writing box to give the chat focus; `↑/↓` then
moves the selection between messages, `Enter` toggles the
focused tool row, `Esc` returns to the writing box. The selected
message gets an accent block (`▍`) painted down its left side
for the full height of the row (header + body, expanded or not).

**Scrolling**: `runtime.chat_auto_scroll` (default `true`) pins
the viewport to the bottom; setting it to `false` lets the user
keep their scroll position while events arrive. Mouse wheel
always scrolls the chat regardless of which side has focus,
this requires `MouseModeCellMotion` to be active (see Zone 4
caveats).

### Zone 3, Writing box (composer)

Multiline text input rendered as a **rounded box, 5 rows tall**: box
top border (1) + 2-row textarea + box bottom border (1) + one blank
**bottom-margin** row that separates it from the footer pills. The box
carries a **side margin** of `panelSideMargin = 1` column each side,
the same inset shared by the chat viewport and the status bar, so all
three zones' left/right edges line up in one clean column. The
**border is always a discreet clay** tone, never the loud accent, so
the box stays a calm, differentiated zone. The single warm accent in
the composer is the **cursor** (sun-orange, set via `ta.SetStyles` in
`newComposer`).

There is **no in-box glyph**: the prompt func returns an empty string
(`SetPromptFunc(0, …)`) so the text starts flush at the box's inner
edge and bubbles never falls back to its default `┃` gutter.

The textarea's content width is `screen − 2*composerSideMargin − 4`
(border 2 + padding 2); `resizeChat` keeps it in sync.

The 5-row height is load-bearing: `ZonesFor` reserves `z.Composer = 5`
and `overlayPaletteAboveComposer` counts `reservedBottomRows = 1 + 5`
to float the slash-command popup directly above the box. Changing the
composer's row count means updating both.

Keystrokes:

- **`Enter`** submits.
- **`Ctrl+J`** inserts a newline. Universal: maps to `0x0A` at the
  byte level, distinct from `Enter`'s `0x0D` so every terminal
  delivers it as a separate event.
- **`Ctrl+Enter` / `Shift+Enter` / `Alt+Enter`** also insert a
  newline **when the terminal supports key disambiguation**
  (Kitty keyboard protocol or xterm `modifyOtherKeys` mode 2).
  Terminals that do: kitty, foot, alacritty, wezterm, ghostty,
  recent iTerm2. Terminals that don't: anything based on VTE
  (GNOME Terminal, Tilix, Terminator), there the modifier is
  stripped before the byte leaves the terminal, so plain Enter
  is indistinguishable from `Ctrl+Enter`. `Ctrl+J` always works.
- **`↑/↓`** move the text cursor between lines (composer focus).
- **`Esc`** cancels in-flight streaming.

While the slash-command popup is visible:

- **`Tab`** accepts the highlighted suggestion (cascading into the
  next level for branch verbs).
- **`Enter`** SENDS whatever is typed, popup or not — it never
  accepts the suggestion. One keypress, one command.
- **`↑/↓`** move the popup selection (wrap-around).
- **`Esc`** dismisses the popup without cancelling the stream.

The popup itself renders inside a rounded clay border on the panel
background: suggestion rows (selected row gets the accent rail and
lifted background; the typed prefix is highlighted in light sun
inside each name) plus a faint footer hint row (`↹ complete · ⏎
send · ↑↓`, prefixed with an `n/N` counter when more matches exist
than fit in the window).

Focus is not signalled by colour any more: the border stays clay and
the chevron stays dim in both states. The cursor (sun) only shows
while the composer is focused, which is signal enough; the chat's own
focus is shown by its selection marker (see "focus" above).

### Zone 3.5, Streaming bar

A one-row strip (`streamingBarHeight = 1`) between the chat and
the writing box. Reserved at all times so the layout stays stable;
visible only when the root agent is producing tokens. Holds a
small `spinner.MiniDot` glyph in accent and a faint `thinking…`
label so the operator knows tokens are flowing.

Wired to the `streamCancel` field on the Model. When the first
chunk of a root reply arrives, `handleAgentChunk` batches the
spinner's `Tick` command alongside the next-chunk read so the
animation drives in parallel with the stream. On `done` the
spinner pointer is cleared and the strip blanks out, without
collapsing, so the layout doesn't jump.

**Known WIP**: the spinner currently does not animate in some
environments (the ticker advances on the Model but the next
TickMsg sometimes fails to re-arm, under investigation). The
fallback indicator is a small ticks counter we leave in the
label while debugging. The chat is still fully usable; this is
purely a cosmetic bug.

### Zone 4, Footer (chip bar)

A row of borderless **pill chips** at the very bottom of the
screen, **one row tall** (`footerHeight = 1`). Each pill is a
horizontal strip with the `colorBGFocus` background colour
(`#473C34`, lightened picón), dim text on top, no border, padding `0, 1`.

The chip set is deliberately minimal, every chip must earn its
place:

| Chip | Trigger to show | Content |
|---|---|---|
| Interlocutor | always | glyph + name + `R`/`S`/`D` for root/static/dynamic |
| Model | always | `model · provider/model-id` |
| Guard | when the root's `context_guard` is enabled (root chat only) | `guard · tokens · NN%` or `guard · turns · NN%`, glyph severity-coloured by fill (info → warning ≥70% → error ≥90%) |
| Workers | when ≥ 1 worker is running | `workers · N`, glyph in warning amber |
| A2A | only when serving (`ok` / `insecure`) | `● a2a · ok` or `● a2a · insecure`, glyph severity-coloured |

The **Guard** chip is the live context-window gauge. The percentage
is how close the active session is to the next compaction: for the
token-threshold strategy it is `real prompt tokens / (context window −
buffer)`; for the sliding-window strategy it is `turns since the last
compaction / max_turns`. Both numerator and denominator come from
`Facade.ContextGuardStatus`, which reads the session-state keys the
adk-utils-go `contextguard` plugin maintains. The chip is hidden while
a worker chat is active (the gauge only tracks the root). When the
guard actually fires, a highlighted `MessageNotice` card lands in the
transcript (accent border + `compact` glyph) so the user sees the
compaction happen.

The footer is deliberately minimal: it surfaces only live, changing
state. Static information the user can consult on demand (secrets,
scope, version) is reached through the slash commands (`/secret`,
`/help`, `/settings`), not pinned to a chip.

Visual rules:

- **Pill background**: `colorBGFocus` everywhere. Identical across
  chip kinds; the chip is identified by its glyph, not its
  background colour. **Every styled span inside the pill must
  explicitly set its own `Background(colorBGFocus)`**, lipgloss
  v2 doesn't inherit the wrapping style's background into child
  spans, so the pill would look "split" between text and padding
  without the explicit propagation. See `chips.go::chip`.
- **Glyph colour**: dimmed entity / severity tone so chips can
  still be told apart at a glance without shouting.
- **Label and value**: `text_dim` for both. No bold. The chip
  recedes into the background; the eye reads it as ambient
  context.

Chips overflow from the right side: when the row is too narrow
to fit all, the rightmost ones are dropped silently. The
interlocutor chip survives longest because it carries the most
critical context.

Mouse: clicks on a chip do nothing today; chips are display-only.

## Markdown rendering

The root agent's replies come back as Markdown. baifo renders them
through [Glamour](https://github.com/charmbracelet/glamour) so
bold / italic / code / lists / tables look right in a terminal,
while the user's own messages and tool rows stay plain, they
either don't need formatting or have their own conventions
(tool cards). System and error messages are also plain.

Implementation lives in `internal/tui/markdown.go` and is wired
into `chatView.renderMessage` via a small per-message cache:

- **Canarias theme**: the renderer starts from `glamour.DarkStyleConfig`
  and repaints it in the palette (`canariasMarkdownStyle`): sun-orange
  headings (H1 prefixed with `▰ `, no background box), golden-clay
  underlined links with sun link-text, lime-wash body/strong/emph,
  sun bullets, a clay quote bar and clay horizontal rules, inline
  code as lava-red on lightened picón, and code blocks sitting on a
  picón panel while keeping Glamour's tuned Chroma syntax colours.

- **Library**: `github.com/charmbracelet/glamour v0.7.0`. We
  intentionally pin to a pre-`x/ansi` version of glamour so it
  doesn't fight baifo's `charm.land/bubbletea/v2` + `charm.land/lipgloss/v2` stack, see
  `.agents/DEPENDENCIES.md` for the rationale.
- **Per-message cache** (`markdownCache`): keyed by message index,
  invalidated when the chat content width changes. We re-render
  on demand but throttle at `markdownThrottle = 200ms` per
  message so streaming chunks don't trigger 50 Glamour passes
  per second on long replies. The cache holds the last rendered
  output; calls within the throttle window return it as-is.
  When the streaming ends (`done == true`), the model passes
  `force = true` and Glamour is re-run for the final, polished
  render.
- **Width-aware**: the renderer is bound to
  `contentWidth() - markdownWrapMargin()`, so the word wrap that
  Glamour computes matches the chat's gutter math and `rowSpans`
  stays accurate for the click-to-row hit testing.
- **Half-formed Markdown guard** (`prepareForGlamour`): while the
  LLM is mid-emission of a fenced code block, the input ends
  with an unclosed ` ``` `. Glamour would either extend the
  block to the end of the buffer or render it weirdly. We
  detect unclosed fences (by counting line-leading ``` ``` ```
  markers) and explicitly close the dangling segment before
  handing it to Glamour. The closed part renders normally; the
  dangling part renders as an in-progress code block. The screen
  stops dancing as a result.
- **Custom theme**: we copy `glamour.DarkStyleConfig` and set
  `Document.Margin = 0`. The default has a 2-col left margin
  that pushes the agent's body away from the rest of the chat;
  zeroing it keeps user and root rows in the same left column.
- **Width clamp** (`clampToWidth`): glamour v0.7.0 wraps via
  muesli/reflow/wordwrap, which sometimes emits lines WIDER than the
  wrap budget: it never breaks long unspaced tokens, miscalculates by
  1–2 cells on some inputs, and indents block quotes with the `│ `
  token OUTSIDE the budget. Every overflowing line wraps onto an extra
  terminal row that `rowSpans` never counted, desyncing selection and
  scroll (the "interface goes crazy" report). After each render we
  re-wrap any over-budget line (ansi.Wrap, with ansi.Hardwrap as the
  safety net for unspaced tokens) so no line ever exceeds the budget.
  Re-wrap, not truncate — no character is lost.
- **Graceful failure**: if Glamour errors out (rare, parsing edge
  cases), the renderer returns the original text. The chat
  never goes blank.
- **Variation-selector stripping** (`terminal.StripVariationSelectors`):
  emoji that carry U+FE0F legitimately render WIDER than x/ansi's
  wcwidth reports on Windows Terminal with emoji fallback fonts, so a
  measured-as-fitting line could still wrap physically (issue #22).
  The render path strips the invisible selectors; the visible emoji
  stays. `glamour.WithEmoji()` is deliberately NOT enabled so glamour
  never introduces shortcode-converted emoji of its own.
- **Terminal capability degradation** (`internal/platform/terminal`):
  the selection rail (`▌`), rounded borders, quote bar (`│ `) and
  rules (`─`) are package vars gated by `terminal.SupportsBoxDrawing`.
  On legacy Windows consoles (no WT_SESSION/ConEmu/ANSICON/pty) they
  degrade to a pure-ASCII set (`|`, `+---+`, `-`) so the row
  bookkeeping stays in sync with what the terminal actually paints.
  `cmd/baifo` also switches the console code page to UTF-8 at startup
  (`terminal.PrepareUTF8`, restored on exit) because Bubble Tea v2
  enables VT processing but never touches the code page.

What we explicitly do NOT do:

- Render markdown for user / system / error / tool messages.
- Style code blocks via Chroma syntax highlighting (Glamour
  supports it but glamour v0.7's default scheme clashes with our
  palette; we accept plain code blocks for now).
- Render markdown anywhere outside `MessageRoot`.

### Floating toasts

Bottom-right corner, stacked. Each toast: title (1 line, bold),
body (1–2 lines, dim). Auto-dismiss after a few seconds; manual
dismiss with `Esc`. Types: `info`, `success`, `warning`, `error`.

### Overlays

baifo renders modal overlays over the chat for every alternate
view. **All overlays share the same chrome and the same five-key
contract**. See `.agents/OVERLAY_STYLE.md` for the design system
spec. Overlays:

| Slash command | Renderer | Notes |
|---|---|---|
| `/session list` | `renderSessions` | List of persisted root sessions. Active session marked with `●` ok-green. |
| `/worker list` | `renderWorkers` | Live workers with status-coloured marker. Enter switches the chat to that worker. |
| `/fact list` | `renderFacts` | Long-term memory entries (first content line + `#id · category · author · timestamp` meta row). Enter opens the embedded editor on the entry; `n` adds, `d` deletes (y/N gated). |
| `/help`, Ctrl+/, F1 | `renderHelp` | Keybindings grouped by scope (Writing box · Chat transcript · Global · Slash commands). Not navigable. |
| Embedded YAML editor | `editor.Model` | Opened by every CRUD verb that mutates an entity. Predictable keymap (Ctrl+S save, Esc/Ctrl+Q quit, Ctrl+F find, Ctrl+Z / Ctrl+Shift+Z undo/redo, Tab/Shift+Tab indent, Ctrl+D duplicate line, Ctrl+K delete line, Alt+↑/↓ move line; Ctrl+C/X act on the whole line when nothing is selected). Full mouse support: wheel scrolls, click places the cursor, drag selects, shift+click extends. `?` shows the full keymap. |
| Secret prompt | `overlays.SecretPrompt` | Masked-input modal for `/secret set`. |

`Esc` dismisses any overlay. While an overlay is open, key input
routes through it; mouse clicks on the chat behind it are dropped
(see `regionAt` in `chat_focus.go`).

## Slash commands

Triggered by typing `/` as the first character in the writing box.

### Plural-resource commands

Every resource collection lives behind its singular slash command.
For `/session` and `/worker`, a bare command returns a usage error;
the `list` sub-verb opens the overlay. For all other resource commands
(`/mcp`, `/skill`, `/agent`, `/provider`, `/fact`), a bare command
returns an inline list as a system message (same result as `list`);
other sub-verbs perform actions on a specific entry.

| Command | Behaviour |
|---|---|
| `/session` | Print usage: `usage: /session [list|new|switch|rename|delete]` |
| `/session list` | Open Sessions overlay |
| `/session new` | Create a fresh session and switch to it |
| `/session switch <id>` | Activate session id |
| `/session rename <id> <title>` | Rename session |
| `/session delete <id>` | Delete session (creates a new one if active) |
| `/worker` | Print usage: `usage: /worker [list|talk|kill|collect]` |
| `/worker list` | Open Workers overlay |
| `/worker talk <id\|name>` | Switch the chat to that worker |
| `/worker kill <id\|name>` | Cancel the worker |
| `/worker collect <id\|name>` | Harvest its output and remove it |
| `/agent`, `/agent add\|edit\|delete <name>` | Agent template CRUD (root + sub-agents) via the editor |
| `/mcp`, `/mcp add\|edit\|delete\|auth\|test\|logout <name>` | MCP CRUD + OAuth flow / reachability test |
| `/provider`, `/provider add\|edit\|delete <name>` | Provider CRUD |
| `/skill`, `/skill add\|edit\|delete <name>` / `/skill install <url>` | SKILL.md CRUD + install from archive |
| `/secret`, `/secret set <name>\|delete <name>\|encode\|decode` | Secret CRUD + mode flip |
| `/fact`, `/fact add\|edit <id>\|delete <id>` | Long-term memory CRUD |

### Top-level verbs

| Command | Behaviour |
|---|---|
| `/help`, `/?` | Help overlay |
| `/quit` | Exit baifo |
| `/settings` | Print config path and available verbs (inline system message) |
| `/settings edit` | Open `baifo.yaml` in the embedded editor |
| `/settings reload` | Re-read `.baifo/` from disk |
| `/root` | Switch the chat back to the root agent |

## Responsive

Breakpoints by terminal width (height matters only for the "too
small" message). Thresholds in `internal/tui/layout.go`.

- **Wide / Normal / Narrow**, all use the same single-column
  layout. The `/worker list` overlay covers the need a workers sidebar
  would have served.
- **Too small**, single-screen "terminal too small" message.

## Animations

Conservative. The only motion accepted:

- Splash screen fade-out after ~600 ms.
- Toast fade-out.

## Keyboard map (global)

| Key | Action |
|---|---|
| `Ctrl+C` | Quit baifo |
| `Ctrl+/`, `F1` | Toggle help overlay |
| `Esc` | Close the open overlay / cancel current stream |
| `Enter` (composer focus) | Submit message |
| `Enter` (chat focus) | Toggle expansion of the focused tool row |
| `Ctrl+J` (composer) | Insert newline, works in every terminal |
| `Ctrl+Enter` / `Shift+Enter` / `Alt+Enter` (composer) | Insert newline, only in terminals that support key disambiguation (not VTE) |
| `Ctrl+↑` / `Ctrl+↓` (composer) | Recall previous / next sent message (shell-style history). Ctrl+↓ past the newest restores the in-progress draft. Resets on submit or edit |
| `Ctrl+←` / `Ctrl+→` (composer) | Move the cursor one word left / right. `Alt+←` / `Alt+→` are equivalent for terminals that collapse Ctrl+arrows |
| `↑/↓/PgUp/PgDn/Home/End` (chat focus) | Move the message selection |
| Wheel up / down | Scroll the chat |
| Click on chat | Give the chat focus, toggle tool if on one |
| Click on writing box | Give the writing box focus |

There is **no `Tab` cycling** between views; baifo has only the chat.
Focus moves between the chat transcript and the writing box through
the explicit focus model above (mouse click), not a mode toggle.

## Component layout

`internal/tui/` is flat by design. Current files:

```
internal/tui/
├── model.go                # root tea.Model (Init / Update / View)
├── theme.go                # palette, accents, glyphs
├── layout.go               # responsive breakpoints + ZonesFor
├── header.go               # logo + accent fade ribbon (no rule, no tabs)
├── chat.go                 # chatView, tool-row renderer, message rows
├── chat_focus.go           # focus model + mouse routing
├── markdown.go             # Glamour-backed Markdown rendering for MessageRoot
├── chips.go                # pill chip primitive (bg, padding, styling helpers)
├── components.go           # composer, footer chips, splash, help, toasts
├── commands.go             # slash dispatcher and slashResult
├── palette.go              # slash-command autocomplete popup (state + render)
├── palette_tree.go         # slash-command tree mirroring the dispatcher
├── overlay_chrome.go       # renderOverlay + renderList + keycap (the shared chrome)
├── overlays.go             # per-overlay key handlers (sessions, workers)
├── tabs_views.go           # renderSessions / renderWorkers overlays
├── editor_overlay.go       # embedded editor overlay glue
├── secret_overlay.go       # secret-prompt overlay glue
├── interlocutor.go         # switch chat focus to a worker; per-worker chat history
├── overlays/               # self-contained overlay components
│   ├── secret_prompt.go    # masked-input modal
│   ├── editor_validators.go
│   └── skill_styler.go     # SKILL.md syntax highlighter
└── components/
    └── editor/             # reusable multiline editor + mdhl/yamlhl
```

`overlay_chrome.go` is the single source of truth for the modal
overlay design system. Read `.agents/OVERLAY_STYLE.md` before
adding a new overlay.

The `overlays/` sub-package owns components that don't touch
`Model` private state and can be tested in isolation
(`secret_prompt`, `editor_validators`, `skill_styler`). The
"overlay glue" files (`editor_overlay.go`, `secret_overlay.go`)
live in the root package because they mutate `Model` fields
directly during the `Update` dispatch.

The `components/editor/` sub-package owns the reusable
multi-line editor with markdown (mdhl) and YAML (yamlhl) syntax
highlighters. The component ships neutral xterm-256 defaults and
stays host-agnostic; baifo injects the Canarias palette through
`editor.Options.Styles` plus the yamlhl/mdhl themes, all built in
`internal/tui/editor_theme.go` from the `theme.go` colour vars.

### Multi-Agent Chat State (The `chatHistories` Cache)

**Context:** Users can spawn multiple background workers and switch their active chat view between the root agent and various workers using the `/chat` command. Because all agents can stream responses concurrently, the UI must route incoming stream chunks to the correct logical chat without corrupting the screen.

**Design:**
The TUI implements a strict "cache-based" model for chat state:
- The `m.messages` slice represents what is actively painted on the screen.
- A background map `m.chatHistories[workerID] []Message` stores the complete visual state of every interlocutor.
- When an agent (root or worker) streams an event:
  - If the agent is the active interlocutor, the event mutates `m.messages` and the UI repaints.
  - If the agent is in the background, the event mutates the slice inside `m.chatHistories[agentID]`. The UI does NOT repaint.
- When the user switches tabs, `m.messages` is swapped with the target's stashed slice from `m.chatHistories`. 

**Why:** Earlier iterations discarded background events ("if I'm not looking at it, I don't paint it"), forcing the TUI to request a full history replay from the backend database every time the user switched tabs. This caused flickers and broke active streaming continuity. The independent caching model ensures instant tab switching with zero data loss and allows background agents to finish complex, multi-tool tasks silently without bleeding their output into the user's active conversation.
