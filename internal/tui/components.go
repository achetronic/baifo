// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"math/rand/v2"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

// Composer

// composer is the bottom-of-screen multiline input. It wraps a
// textarea.Model from bubbles, adding baifo-flavoured styling and a
// "/" hint line that becomes the slash-command autocomplete in a
// later iteration.
type composer struct {
	ta    textarea.Model
	theme Theme
}

// newComposer builds a composer pre-configured with theme-aware styles
// and the right key behaviour: plain Enter submits, Ctrl+Enter (and
// Alt+Enter as a fallback for terminals that don't distinguish
// Ctrl+Enter from Enter) inserts a newline.
func newComposer(theme Theme) composer {
	ta := textarea.New()
	ta.Placeholder = "Type a message — / for commands · Ctrl+J newline · Ctrl+↑ history"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(2)
	// No box around the writing area. Instead of the bubbles default
	// "┃ " gutter (which, paired with baifo's old panel border, made the
	// composer feel boxed-in and visually noisy), we paint a single
	// accent chevron "› " on the first line and align continuation
	// lines under it with two spaces. The chevron is the ONLY chrome the
	// composer carries; focus is signalled by colouring it (see View).
	// Prompt is set per-render in View via SetPromptFunc so the colour
	// can track focus; here we just blank the static Prompt so bubbles
	// doesn't draw its own bar underneath.
	ta.Prompt = ""
	// No in-box glyph: continuation lines and the first line all align
	// flush at the left padding. We keep a dynamic prompt func that
	// returns nothing so bubbles doesn't fall back to its default
	// "┃ " gutter. promptWidth 0 means the text starts at the box's
	// inner edge.
	ta.SetPromptFunc(0, func(textarea.PromptInfo) string { return "" })
	// Rebind newline so plain Enter goes to the model (submit) and
	// a few modifier-Enter variants insert a newline. Reality check
	// across terminals:
	//
	//   - "ctrl+enter" and "shift+enter" require the terminal to
	//     support the Kitty keyboard protocol (or at least the
	//     "basic key disambiguation" subset that bubbletea v2
	//     requests). Modern terminals (kitty, foot, alacritty,
	//     wezterm, ghostty, recent iTerm2) report them properly;
	//     older xterm-style terminals collapse both to plain Enter.
	//   - "alt+enter" is reliably distinguished by almost every
	//     terminal (it prepends ESC), so it's the safe fallback.
	//   - "ctrl+j" maps to 0x0A (line feed) at the byte level —
	//     a different byte from Enter's 0x0D, so even the
	//     oldest terminal distinguishes them. We accept it as
	//     the universal escape hatch.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+enter", "shift+enter", "alt+enter", "ctrl+j"),
		key.WithHelp("ctrl+enter / ctrl+j", "newline"),
	)
	// Word-wise cursor motion. Bubbles binds it to Alt+Left/Right by
	// default, but Ctrl+Left/Right is what most users reach for (it's
	// the convention in browsers, editors and shells). We add the
	// Ctrl variants alongside the Alt ones so both work. Same for the
	// word-delete bindings, which feel natural paired with the moves.
	ta.KeyMap.WordBackward = key.NewBinding(
		key.WithKeys("ctrl+left", "alt+left", "alt+b"),
		key.WithHelp("ctrl+left", "word left"),
	)
	ta.KeyMap.WordForward = key.NewBinding(
		key.WithKeys("ctrl+right", "alt+right", "alt+f"),
		key.WithHelp("ctrl+right", "word right"),
	)
	ta.Focus()
	// The only accent inside the box is the cursor: sun-orange. The
	// box border stays a discreet clay at all times (see View), so the
	// composer reads as a quiet, differentiated zone — the warm note is
	// the cursor, not a glowing frame. Styles() is a getter; mutate a
	// copy and push it back via SetStyles so the virtual-cursor colour
	// is recomputed.
	styles := ta.Styles()
	styles.Cursor.Color = theme.Accent.Primary
	ta.SetStyles(styles)
	return composer{ta: ta, theme: theme}
}

// panelSideMargin is the shared left/right inset for the three
// stacked zones — chat viewport, composer box and status bar — so
// their left and right edges line up in a single clean column.
const panelSideMargin = 1

// composerSideMargin is kept as an alias for readability at the
// composer call sites; it must equal panelSideMargin so the box
// aligns with the chat and footer.
const composerSideMargin = panelSideMargin

// View renders the composer at the given width as a rounded box with
// side margins and one row of bottom margin separating it from the
// footer pills. The border is always a discreet clay tone — never the
// loud accent — so the box reads as a calm, differentiated zone. The
// warm accent in the composer is the cursor (set in newComposer), not
// the frame. focused is unused for colour now; kept for API symmetry.
//
// Layout (rows): box top border (1) + textarea (2) + box bottom
// border (1) + bottom margin (1) = 5 rows, matching z.Composer in
// ZonesFor and reservedBottomRows in overlayPaletteAboveComposer.
func (c composer) View(width int, focused bool) string {
	_ = focused
	border := c.theme.PanelBorder()
	// In lipgloss v2, Style.Width(N) makes the WHOLE rendered block N
	// columns wide (border + padding + content all included). So we
	// hand it the outer footprint directly — screen width minus one
	// margin per side — and lipgloss fits the textarea inside. (The
	// textarea itself was sized to outer-4 in resizeChat to match the
	// 2 border + 2 padding columns.)
	outer := width - 2*composerSideMargin
	if outer < 4 {
		outer = width // too narrow for margins; fall back to flush
	}
	box := border.Width(outer).Render(c.ta.View())
	if outer != width {
		box = lipgloss.NewStyle().MarginLeft(composerSideMargin).Render(box)
	}
	// One blank row of bottom margin so the box breathes above the
	// footer pills.
	return box + "\n"
}

// Value returns the current text without trailing whitespace.
func (c composer) Value() string {
	return strings.TrimSpace(c.ta.Value())
}

// Reset clears the composer.
func (c *composer) Reset() { c.ta.Reset() }

// Status bar (chip footer)

// statusBarData bundles every field the footer renders. The model
// fills it in just before View(); there's no live updater here.
//
// Every field is optional. Empty / zero values cause their chip to
// either be hidden (interlocutor) or dimmed (counts at 0), so the
// footer stays uncluttered when the underlying state is empty.
type statusBarData struct {
	// Model is the provider/model identifier of the active runner.
	// Model is the provider/model identifier of the active runner.
	Model string

	// Tokens is the cumulative token count of the active session.
	// Today this is always 0; the field is wired for the planned
	// token-progress chip.
	Tokens int

	// A2AStatus is one of "off", "ok", "insecure". The A2A chip
	// is suppressed when the server is off; only when it's
	// actively serving does the chip render.
	A2AStatus string

	// WorkersRunning is the number of workers currently in the
	// "running" status. Drives a warning-coloured chip when > 0.
	WorkersRunning int

	// Talking is the friendly label of the chat's active interlocutor
	// ("root" or a worker name). Empty hides the chip.
	Talking string

	// TalkingKind is the entity kind of the active interlocutor:
	// "root" | "static" | "dynamic". Drives the colour and the R/S/D
	// short letter inside the interlocutor chip. Empty falls back to
	// "root" because the chat defaults to the root agent.
	TalkingKind string

	// CopiedNotify is true when a message has just been copied.
	CopiedNotify bool

	// GuardEnabled reports whether the active interlocutor's context
	// guard is on. When true the footer renders a gauge chip showing
	// the strategy and how close the conversation is to compaction.
	GuardEnabled bool

	// GuardStrategy is the active compaction strategy ("threshold" or
	// "sliding_window"); drives the chip's strategy label.
	GuardStrategy string

	// GuardPercent is the 0..100 progress toward the next compaction;
	// drives both the chip value and its severity colour.
	GuardPercent int
}

// statusBarSidePadding is the left/right inset applied to the chip
// row so its edges line up with the chat viewport and the composer
// box above it. All three zones share panelSideMargin, so the chip
// row, the box border and the panel border start in the same column.
const statusBarSidePadding = panelSideMargin

// renderStatusBar emits the bottom-of-screen footer as a row of
// contextual chips, separated by single spaces. Chips encode their
// own colour palette via chipStyleFor*; the footer's job is just to
// order them and clip on overflow.
//
// Order, left-to-right:
//
//  1. Interlocutor (root/static/dynamic) — who is the user talking to
//  2. Model        — provider/model
//  3. Workers      — running count (only when > 0)
//  4. A2A          — server status dot (only when serving)
//
// Clipping is from the right: when the rendered width exceeds the
// terminal, we drop the rightmost chip and re-render until it fits.
// The interlocutor chip survives longest because it's the most
// critical context.
//
// Chips are 1-row tall pills (no border, dim text on a slightly
// lifted background). ZonesFor.Status reserves footerHeight (1)
// rows for them. A symmetric statusBarSidePadding inset on both
// edges aligns the row with the writing box above it.
func renderStatusBar(theme Theme, d statusBarData, width int) string {
	chips := buildFooterChips(theme, d)

	// Budget for the chips themselves is the terminal width minus
	// the left+right inset. When the terminal is genuinely too
	// narrow to honour the inset we fall back to zero padding so
	// the chips still get a chance to render.
	inset := statusBarSidePadding
	budget := width - 2*inset
	if budget < 1 {
		inset = 0
		budget = width
	}

	// Greedy right-trim until the line fits the inset budget. We
	// measure with lipgloss.Width on the joined block; the chip
	// borders contribute 2 cols each so the math accounts for the
	// real on-screen width, not the rune count of the content.
	line := joinChips(chips, " ")
	for lipgloss.Width(line) > budget && len(chips) > 1 {
		chips = chips[:len(chips)-1]
		line = joinChips(chips, " ")
	}

	pad := strings.Repeat(" ", inset)
	return pad + line + pad
}

// buildFooterChips returns the chip strings in render order. Empty
// strings are filtered out by joinChips so a caller can suppress a
// chip by returning "" for it.
//
// The chip set is intentionally minimal — every chip must earn its
// place by either being persistently useful (who am I talking to,
// what model is gasting tokens) or by surfacing a transient state
// the user wants to notice (workers running, A2A on). Static
// metadata like the secrets count, the config scope and the baifo
// version are not chips: they're either consulted on demand via
// slash commands (/secret, /config) or shown in /help.
func buildFooterChips(theme Theme, d statusBarData) []string {
	out := make([]string, 0, 5)

	if d.CopiedNotify {
		out = append(out, chipCopied(theme))
	}
	if c := chipInterlocutor(theme, d.Talking, d.TalkingKind); c != "" {
		out = append(out, c)
	}
	if c := chipModel(theme, d.Model); c != "" {
		out = append(out, c)
	}
	if c := chipContextGuard(theme, d.GuardEnabled, d.GuardStrategy, d.GuardPercent); c != "" {
		out = append(out, c)
	}
	if c := chipWorkers(theme, d.WorkersRunning); c != "" {
		out = append(out, c)
	}
	if c := chipA2A(theme, d.A2AStatus); c != "" {
		out = append(out, c)
	}
	return out
}

// joinChips composes chip pills side by side with sep between
// them. Chips are single-line now (no border, just a horizontal
// pill of dim text on a slightly lifted background), so a plain
// strings.Join would work — we keep the lipgloss.JoinHorizontal
// call so the helper stays robust if a chip variant grows taller
// in the future, and so unequal widths still align cleanly.
//
// Empty inputs are dropped first so a chip suppressed by its caller
// (e.g. workers count == 0) leaves no gap.
//
// Empty inputs are dropped first so a chip suppressed by its caller
// (e.g. workers count == 0) leaves no gap.
func joinChips(chips []string, sep string) string {
	nonEmpty := chips[:0:0]
	for _, c := range chips {
		if c != "" {
			nonEmpty = append(nonEmpty, c)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	// Interleave separators between chip blocks so each gap is its
	// own block and JoinHorizontal does not collapse them.
	blocks := make([]string, 0, len(nonEmpty)*2-1)
	for i, c := range nonEmpty {
		if i > 0 {
			blocks = append(blocks, sep)
		}
		blocks = append(blocks, c)
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, blocks...)
}

// chipCopied renders the temporary clipboard copy confirmation chip.
func chipCopied(theme Theme) string {
	return chip("✓", "clipboard", "copied!", chipStyleSeverity("success"))
}

// chipInterlocutor renders the chip that shows who the chat is
// currently talking to. The chip's colour follows the entity kind
// (cyan for root, violet for static, amber for dynamic), and the
// value is "<name> · <R|S|D>" so the kind is readable even without
// colour.
//
// Returns "" when no interlocutor is set so the chip is hidden.
func chipInterlocutor(theme Theme, name, kind string) string {
	if name == "" {
		return ""
	}
	if kind == "" {
		kind = "root"
	}
	glyph := theme.Glyph(kindGlyphKey(kind))
	value := name
	if letter := kindShortLetter(kind); letter != "" {
		value = name + " · " + letter
	}
	return chip(glyph, "", value, chipStyleForEntity(kind))
}

// chipModel renders the provider/model chip. Neutral palette — the
// model isn't an entity in the same sense as an agent, it's
// metadata. Hidden when empty.
func chipModel(theme Theme, model string) string {
	if model == "" {
		return ""
	}
	return chip(theme.Glyph("gear"), "model", model, chipStyleNeutral())
}

// chipContextGuard renders the context-guard gauge chip. Hidden when
// the guard is disabled. When on, the value reads "<strategy> · NN%"
// where the percentage is how close the conversation is to its next
// compaction, and the severity colour escalates from info → warning →
// error as the gauge fills so a near-full context catches the eye.
func chipContextGuard(theme Theme, enabled bool, strategy string, percent int) string {
	if !enabled {
		return ""
	}
	value := guardStrategyLabel(strategy) + " · " + strconv.Itoa(percent) + "%"
	return chip(theme.Glyph("compact"), "guard", value, chipStyleSeverity(guardSeverity(percent)))
}

// guardStrategyLabel maps the config strategy string to the short word
// shown inside the guard chip. "tokens" for the default token-threshold
// strategy, "turns" for the sliding-window (turn-count) strategy.
func guardStrategyLabel(strategy string) string {
	switch strategy {
	case "sliding_window":
		return "turns"
	default:
		return "tokens"
	}
}

// guardSeverity escalates the guard chip's colour as the gauge fills:
// info below 70%, warning amber from 70%, error red from 90% — so the
// user gets a calm-to-urgent gradient as compaction approaches.
func guardSeverity(percent int) string {
	switch {
	case percent >= 90:
		return "error"
	case percent >= 70:
		return "warning"
	default:
		return "info"
	}
}

// chipWorkers renders the running-workers chip. When zero, the chip
// is hidden — empty state is the default and would just add noise.
// When > 0, the chip is warning-coloured so the user notices.
func chipWorkers(theme Theme, n int) string {
	if n <= 0 {
		return ""
	}
	return chip(theme.Glyph("running"), "workers", strconv.Itoa(n), chipStyleSeverity("warning"))
}

// chipA2A renders the A2A server status chip — but only when the
// server is actually serving. The 99% case is `off`, in which case
// the chip is suppressed so the footer doesn't carry permanently
// inactive state. When the server is on, the glyph + value carry
// the severity colour (ok = green, insecure = warning amber).
func chipA2A(theme Theme, status string) string {
	switch status {
	case "ok", "insecure":
		return chip("●", "a2a", status, chipStyleSeverity(a2aSeverity(status)))
	default:
		return ""
	}
}

// a2aSeverity maps an A2A status string to one of the severity keys
// chipStyleSeverity understands.
func a2aSeverity(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "insecure":
		return "warning"
	default:
		return "error"
	}
}

// kindGlyphKey maps an interlocutor kind to the glyph name in the
// Theme.Glyph table. Anything unknown maps to "root" so the chip
// stays visible.
func kindGlyphKey(kind string) string {
	switch kind {
	case "static":
		return "static"
	case "dynamic":
		return "dynamic"
	default:
		return "root"
	}
}

// Splash logo

var splashTaglines = []string{
	"A baby goat, fed with gofio",
}

// splashLogoSmall is the compact ASCII brand mark used when the
// terminal is too narrow for the full-size logo.
var splashLogoSmall = []string{
	"█▀▀█  ▄▀▀▄  █  █▀▀▀  █▀▀█",
	"█▀▀▄  █▀▀█  █  █▀▀   █  █",
	"▀▀▀▀  ▀  ▀  ▀  ▀     ▀▀▀▀",
}

// splashLogoLarge is the headline brand mark shown on startup when
// the terminal is wide enough: the baifo head plus the BAIFO
// letters. Uses only block elements (U+2580/2584/2588) because they
// render deterministically on any monospace font.
var splashLogoLarge = []string{
	"             █████▄                   ▄▄███████▄",
	"             ▀███▀██▄              ▄▄███▀▀████▀▀",
	"               ██▄ ██▄  ▄▄▄▄     ▄███▀  ███▀▀",
	"                ██  ██▄ ▄█████▄ ▄██▀  ▄███",
	"▄██▄▄▄          ██▄▄▄███████▄▀█████  ▄███          ▄▄▄█▄",
	"██████████▄▄▄   ██████▀▀       █████████   ▄▄▄███████████",
	"██████▄▄▀▀▀██████▀  ████▀          ▀▀███ ▄███▀▀▀▄▄██████",
	" ████████▄   ▄██    █▀██▄▄█▀          ████▀  ▄▄████████▀",
	"  ▀████████▄██▀       ███▀             ██   ▄█████████▀",
	"   ▀██████████         █▀              █   ▄█████████",
	"      ▀▀██████▄                 ▄▄▄      ▄████████▀",
	"           ███▀█▄             ▄██▀██▄▄   ▄███▀▀",
	"            █████            ▄██████    ▄██",
	"            ▀████            █▀████▀    ██",
	"             ██                        ▀██",
	"             ███                       ▀██",
	"             ███ ▄▄ ▄▄▄▄             ▄  ██",
	"             ██  ▀████▀▀            ▄█  ███",
	"             ▀██   ██     ▄▀       ▄█   ███",
	"              ▀███████▄███▀    ▄▄███     ███",
	"                ██▄▀▀▀    ▄▄██████▀      ██▀",
	"                ▀▀███▄▄█████████▀       ▄█▀",
	"                   ▀▀█████████▀         ██",
	"                     ███████▀           ▀",
	"                     ██████▀",
	" ",
	"     ████████     ████     ██   ████████   ████████",
	"     ██    ██   ██    ██   ██   ██         ██    ██",
	"     ██████     ████████   ██   ██████     ██    ██",
	"     ██    ██   ██    ██   ██   ██         ██    ██",
	"     ████████   ██    ██   ██   ██         ████████",
}

// splashLogo is kept as an alias of the small variant to preserve
// the existing symbol for callers that just want "the logo".
var splashLogo = splashLogoSmall

// renderSplash returns the centred splash block with one of the
// taglines picked at random per launch.
//
// Three rendering tiers depending on terminal width:
//   - >= 60 cols: large block logo (goat head + BAIFO letters).
//   - >= 24 cols: small two-row logo (legacy look).
//   - < 24 cols: just "baifo" in accent colour.
func renderSplash(theme Theme, width int) string {
	if width < 24 {
		return theme.AccentText().Bold(true).Render("baifo")
	}

	var logo []string
	var logoWidth int
	if width >= 60 {
		logo = splashLogoLarge
		logoWidth = 57 // ancho del bloque (cabra centrada + letras BAIFO)
	} else {
		logo = splashLogoSmall
		logoWidth = 18
	}

	tagline := splashTaglines[rand.IntN(len(splashTaglines))]
	styledLogo := make([]string, len(logo))
	for i, line := range logo {
		styledLogo[i] = theme.AccentText().Bold(true).Render(line)
	}
	body := strings.Join(styledLogo, "\n") + "\n\n" +
		theme.FaintText().Render(centerLine(tagline, logoWidth))

	pad := (width - logoWidth) / 2
	if pad < 0 {
		pad = 0
	}
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.Repeat(" ", pad) + lines[i]
	}
	return strings.Join(lines, "\n")
}

// centerLine pads s with leading spaces so it appears centred inside
// a column of width w. Used to keep the tagline under the logo block.
func centerLine(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return strings.Repeat(" ", (w-n)/2) + s
}

// Toast

// ToastKind drives the border colour.
type ToastKind int

const (
	ToastInfo ToastKind = iota
	ToastSuccess
	ToastWarning
	ToastError
)

// Toast is one floating notification. Body is two-line max; longer
// strings are truncated.
type Toast struct {
	Kind  ToastKind
	Title string
	Body  string
}

// renderToast paints a single toast in a coloured box. Stacking of
// multiple toasts is the caller's job.
func renderToast(theme Theme, t Toast) string {
	var border lipgloss.Style
	switch t.Kind {
	case ToastSuccess:
		border = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorSuccess)
	case ToastWarning:
		border = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorWarning)
	case ToastError:
		border = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorError)
	default:
		border = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorTextDim)
	}
	title := theme.PrimaryText().Bold(true).Render(t.Title)
	body := theme.DimText().Render(t.Body)
	return border.Padding(0, 1).Render(title + "\n" + body)
}

// Help overlay

// helpLine is one entry in the help table. Decoupled from rendering so
// the same data can drive a future searchable palette.
type helpLine struct {
	Keys, Description string
}

// helpSection groups related help entries under a heading so the
// overlay reads by scope (writing box vs chat vs global) instead of
// one undifferentiated list. Note, when set, is a short paragraph
// rendered under the heading before (or instead of) the key rows —
// used for the slash-commands section, which explains the concept
// rather than enumerating every command (those live behind /help's
// own autocomplete and the command palette).
type helpSection struct {
	Title string
	Note  string
	Lines []helpLine
}

var helpSections = []helpSection{
	{
		Title: "Writing box",
		Lines: []helpLine{
			{"Enter", "send the message"},
			{"Ctrl+J", "insert a newline (Ctrl+Enter also works on modern terminals)"},
			{"Ctrl+↑ / Ctrl+↓", "recall previous / next sent message"},
			{"Ctrl+← / Ctrl+→", "move the cursor one word left / right (Alt+←/→ also works)"},
			{"Click on box", "give the writing box focus"},
		},
	},
	{
		Title: "Chat transcript",
		Lines: []helpLine{
			{"↑ / ↓", "move the message selection (when the chat has focus)"},
			{"PgUp / PgDn", "page through the chat"},
			{"Home / End", "jump to first / last message"},
			{"Enter", "expand / collapse the focused tool row"},
			{"y / c", "copy clean message text to clipboard"},
			{"Click on chat", "give the chat focus / toggle the clicked tool"},
			{"Mouse wheel", "scroll the chat"},
		},
	},
	{
		Title: "Global",
		Lines: []helpLine{
			{"Esc", "cancel current stream / close the open overlay"},
			{"Ctrl+/, F1", "toggle this help"},
			{"Ctrl+C", "quit"},
		},
	},
	{
		Title: "Slash commands",
		Note:  "Type / at the start of the writing box to run a command.",
		Lines: []helpLine{
			{"Tab", "complete the highlighted suggestion"},
			{"↑ / ↓", "move through the suggestions"},
			{"Enter", "send the command as typed"},
			{"Esc", "dismiss the suggestion popup"},
		},
	},
}

// renderHelp emits the keybinding overlay shown when the user
// presses Ctrl+/. The body is grouped into scoped sections (writing
// box, chat, global, slash commands), each with a dim heading and a
// two-column key/description table. No list cursor — help isn't
// navigable.
//
// Rendered as a centred modal composed over `back`. See
// .agents/OVERLAY_STYLE.md for the invariants.
func renderHelp(theme Theme, back string, width, height int) string {
	heading := theme.AccentText().Bold(true)
	dim := theme.DimText()
	primary := theme.PrimaryText()

	var b strings.Builder
	for si, sec := range helpSections {
		if si > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(heading.Render(sec.Title))
		b.WriteByte('\n')
		if sec.Note != "" {
			b.WriteString(dim.Render(wrapText(sec.Note, 60)))
			if len(sec.Lines) > 0 {
				b.WriteByte('\n')
			}
		}
		for li, l := range sec.Lines {
			b.WriteString(theme.AccentText().Render("  " + padRight(l.Keys, 20)))
			b.WriteString(primary.Render(l.Description))
			if li < len(sec.Lines)-1 {
				b.WriteByte('\n')
			}
		}
	}
	return renderModal(theme, overlayOpts{
		Title:   "Help",
		Content: b.String(),
		Footer:  "[esc] close",
	}, back, width, height)
}

// wrapText soft-wraps s to width columns on word boundaries, used for
// the slash-commands explanatory note. Kept dependency-free (no
// lipgloss) since the note is plain ASCII prose.
func wrapText(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		if i > 0 {
			if lineLen+1+len(w) > width {
				b.WriteByte('\n')
				lineLen = 0
			} else {
				b.WriteByte(' ')
				lineLen++
			}
		}
		b.WriteString(w)
		lineLen += len(w)
	}
	return b.String()
}

// padRight pads s with spaces to w display columns. It measures with
// lipgloss.Width (not len) so rows whose keys contain multi-byte
// glyphs — the arrow keys ↑↓←→ — still align in the help table.
func padRight(s string, w int) string {
	pad := w - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}
