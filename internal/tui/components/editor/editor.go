// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package editor is baifo's embedded text editor component.
//
// Why we wrote our own instead of using bubbles/textarea: the textarea
// in bubbles v2 is excellent for chat-style input but it does not
// expose a hook for per-line styling, which we need to render YAML
// syntax highlighting (PR B.2b) and to overlay autocomplete modals
// (PR B.2c). Fork or hack-with-ANSI-escapes were the alternatives;
// both cost more than writing a small editor of our own.
//
// Scope of this iteration (B.2a):
//   - rune-by-rune buffer with cursor + selection
//   - arrow / home / end / pgup / pgdn / ctrl+home / ctrl+end navigation
//   - selection with Shift+ navigation keys
//   - copy / cut / paste via the bubbletea v2 OSC52 clipboard helpers
//   - line numbers gutter, header title, footer with hotkeys + error bar
//   - Ctrl+S triggers a user-supplied validator; errors stay in the
//     footer and the buffer stays open. On success the editor emits
//     SaveMsg and closes.
//   - Esc with unsaved changes opens a confirmation modal
//     ("Discard changes? [y/N]"). Destructive actions always confirm.
//
// What does NOT live here yet (kept as hooks for future PRs):
//   - LineStyler is wired but always nil in B.2a (highlighting in B.2b)
//   - Triggers map is wired but always nil in B.2a (completions in B.2c)
//
// The editor is content-agnostic. The caller passes the initial text,
// a title, and an OnSave func; YAML knowledge lives elsewhere.
package editor

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// SaveMsg is emitted when the user pressed Ctrl+S and OnSave reported
// no errors. The parent Model uses it to persist the buffer and close
// the editor overlay.
type SaveMsg struct {
	// Value is the full buffer text, joined with '\n' line separators
	// and WITHOUT a trailing newline. Callers that need POSIX-style
	// "final newline" semantics should add one explicitly.
	Value string
}

// CancelMsg is emitted when the user pressed Esc on a clean buffer, or
// confirmed the discard prompt. The parent Model closes the editor on
// receipt; the buffer is not delivered.
type CancelMsg struct{}

// LineStyler is the per-line tokenisation hook. The styler receives
// the full raw line and returns the styled spans the editor should
// paint. By returning spans rather than a pre-styled string, we let
// the editor compose highlighting with selection and cursor styles
// cleanly: the editor knows which byte ranges to override and which
// to delegate to the styler.
//
// In B.2a this hook is nil (no styling). yamlhl plugs in here.
type LineStyler func(lineNum int, content string) []StyledSpan

// StyledSpan is one (from, to) byte range with a lipgloss style. The
// byte indexing matches the raw string the styler received; the
// editor converts to rune positions when overlaying selection/cursor.
type StyledSpan struct {
	From  int
	To    int
	Style lipgloss.Style
}

// CompletionProvider is the hook B.2c will use for autocomplete modals.
// Declared here so B.2a-callers can already wire empty maps.
type CompletionProvider func(prefix string, ctx CompletionContext) []Completion

// CompletionContext is what the editor passes to a CompletionProvider
// when its trigger fires. Kept lean on purpose; B.2c may extend it.
type CompletionContext struct {
	Line int
	Col  int

	// Lines is a snapshot of the whole buffer at trigger time, one
	// entry per row. Providers that need sibling context — e.g. a
	// `reasoning:` completer that wants to read the `model:` line
	// above it — scan this. Read-only; do not mutate.
	Lines []string
}

// Completion is a single autocomplete entry. The View will be shown in
// the picker; Insert is what lands in the buffer when the user picks.
type Completion struct {
	View   string
	Insert string
}

// Validator is the OnSave callback. An empty return slice means "save
// is allowed". Each returned error renders as one line in the footer
// error bar; the buffer is NOT closed when errors are reported, so
// the user can fix and retry.
type Validator func(buffer string) []error

// Styles holds every visual property the editor uses internally. Build
// one via DefaultStyles() and override fields; nil Options.Styles falls
// back to DefaultStyles().
type Styles struct {
	// Header and footer band (background + foreground + padding).
	Header lipgloss.Style
	// Gutter is the line-number column style.
	Gutter lipgloss.Style
	// ErrorLine is the validation-error footer style.
	ErrorLine lipgloss.Style
	// Selection is the highlighted text style.
	Selection lipgloss.Style
	// Cursor is the block cursor style.
	Cursor lipgloss.Style
	// SearchMatch styles a non-current search hit.
	SearchMatch lipgloss.Style
	// SearchCurrentMatch styles the active search hit.
	SearchCurrentMatch lipgloss.Style
	// Modal chrome colors for in-editor prompts (discard/save/help).
	ModalBorder  color.Color
	ModalTitleBg color.Color
	ModalTitleFg color.Color
	ModalText    color.Color
	ModalDim     color.Color
	// Completer popup colors.
	CompleterBg     color.Color
	CompleterSelBg  color.Color
	CompleterFg     color.Color
	CompleterDim    color.Color
	CompleterAccent color.Color
	CompleterBorder color.Color
}

// DefaultStyles returns the built-in neutral styles (xterm-256 palette).
// These read well on most dark terminals and are the stand-alone defaults
// when no host styles are injected via Options.Styles.
func DefaultStyles() Styles {
	return Styles{
		Header:             lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("252")).Padding(0, 1),
		Gutter:             lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 1, 0, 0),
		ErrorLine:          lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Padding(0, 1),
		Selection:          lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("231")),
		Cursor:             lipgloss.NewStyle().Reverse(true),
		SearchMatch:        lipgloss.NewStyle().Background(lipgloss.Color("3")).Foreground(lipgloss.Color("0")),
		SearchCurrentMatch: lipgloss.NewStyle().Background(lipgloss.Color("214")).Foreground(lipgloss.Color("0")).Bold(true),
		ModalBorder:        lipgloss.Color("240"),
		ModalTitleBg:       lipgloss.Color("238"),
		ModalTitleFg:       lipgloss.Color("147"),
		ModalText:          lipgloss.Color("252"),
		ModalDim:           lipgloss.Color("245"),
		CompleterBg:        lipgloss.Color("236"),
		CompleterSelBg:     lipgloss.Color("238"),
		CompleterFg:        lipgloss.Color("252"),
		CompleterDim:       lipgloss.Color("245"),
		CompleterAccent:    lipgloss.Color("147"),
		CompleterBorder:    lipgloss.Color("240"),
	}
}

// Options configures a Model at construction time. Fields are picked
// to match what the first real consumer (PR A's /config edit) needs.
type Options struct {
	// Title appears in the header bar. Defaults to "Editor".
	Title string

	// InitialValue is the buffer text on open. May be empty.
	InitialValue string

	// OnSave validates the buffer when the user hits Ctrl+S. Required;
	// passing nil here defaults to a no-op validator that always
	// allows save (useful for free-form edits like /config edit).
	OnSave Validator

	// LineStyler is the per-line render hook. nil in B.2a.
	LineStyler LineStyler

	// Triggers maps a literal substring (e.g. "${secret:") to its
	// completion provider. nil/empty in B.2a.
	Triggers map[string]CompletionProvider

	// RequireSaveConfirm, when true, makes Ctrl+S open a "Apply
	// changes? [y/N]" prompt before running OnSave. Use this for
	// editors that mutate critical state (config upserts).
	RequireSaveConfirm bool

	// Styles overrides the editor's visual palette. nil uses DefaultStyles().
	Styles *Styles
}

// Model is the BubbleTea component. Construct with New, drive with
// Update, render with View. The host Model owns the lifecycle and
// reads SaveMsg / CancelMsg to decide when to close the editor.
type Model struct {
	styles   Styles
	title    string
	buf      *buffer
	cursor   position
	sel      *selection // nil when no active selection
	onSave   Validator
	styler   LineStyler
	triggers map[string]CompletionProvider
	keymap   keymap

	// viewport scrolls the rendered lines vertically. We feed it
	// pre-rendered strings via SetContentLines so it only handles
	// clipping; the editor owns syntax-relevant logic.
	vp viewport.Model

	// width/height of the editor area, including header and footer.
	// The viewport gets (height - chrome). Set with SetSize.
	width, height int

	// dirty becomes true on the first mutating operation and stays
	// true until SaveMsg is emitted. Used to decide whether Esc
	// goes straight to CancelMsg or pops the confirmation modal.
	dirty bool

	// confirmDiscard is true while the "Discard changes?" modal is
	// up. Keypresses are routed to the modal instead of the buffer.
	confirmDiscard bool

	// validationErrors is the latest output of OnSave. Rendered in
	// the footer; cleared on the next edit so the user sees fresh
	// state immediately.
	validationErrors []error

	// focused governs whether keypresses are consumed. The host
	// Model toggles this on overlay open/close.
	focused bool

	// undoStack / redoStack hold up to historyLimit snapshots. We
	// push to undo before mutating; ctrl+z pops from undo (saving
	// the current state to redo); ctrl+y is the mirror.
	undoStack []snapshot
	redoStack []snapshot

	// completer is the auto-complete overlay. nil when closed.
	// Keypresses route through it before the editor when non-nil.
	completer *completer

	// helpOpen is true while the '?' help overlay is up. Reading and
	// dismissing only; does not block other modals.
	helpOpen bool

	// confirmSave is true while the "Apply changes?" prompt is up.
	// Set by Ctrl+S when the buffer is in a Kind that requires
	// confirmation; the editor's RequireSaveConfirm field controls
	// whether Ctrl+S triggers this path.
	confirmSave bool

	// requireSave is the construction-time decision: when true,
	// Ctrl+S opens the confirmation prompt; when false, save is
	// immediate (validator still runs).
	requireSave bool

	// searchSt holds the find-in-buffer overlay state. nil when the
	// search bar is closed. While non-nil, key dispatch routes most
	// non-editing keys through the search handler.
	searchSt *search

	// xOffset is the leftmost rune column rendered for each line.
	// Increased when the cursor moves past the right edge of the
	// viewport so long lines remain navigable without soft-wrap.
	xOffset int

	// dragging is true between a left-button press inside the text
	// area and its release. While set, mouse motion extends the
	// selection from the press anchor (click-and-drag selection).
	dragging bool
}

// New builds a Model from Options. The cursor starts at (0,0); the
// buffer comes from Options.InitialValue.
func New(opts Options) Model {
	title := opts.Title
	if title == "" {
		title = "Editor"
	}
	onSave := opts.OnSave
	if onSave == nil {
		onSave = func(string) []error { return nil }
	}

	vp := viewport.New()
	// We render line numbers ourselves, so the viewport draws no gutter.
	// Soft-wrap is OFF in B.2a (long lines scroll horizontally via the
	// viewport's built-in offset, not implemented yet). Long lines just
	// get clipped at the right margin for now; full horizontal scroll
	// is on the TODO list for B.2e.

	st := DefaultStyles()
	if opts.Styles != nil {
		st = *opts.Styles
	}

	m := Model{
		styles:      st,
		title:       title,
		buf:         newBuffer(opts.InitialValue),
		onSave:      onSave,
		styler:      opts.LineStyler,
		triggers:    opts.Triggers,
		keymap:      defaultKeymap(),
		vp:          vp,
		focused:     true,
		requireSave: opts.RequireSaveConfirm,
	}
	return m
}

// Focus marks the editor as receiving keypresses. The default Model
// returned by New is already focused; Focus is here for symmetry with
// the rest of bubbles.
func (m *Model) Focus() { m.focused = true }

// Blur stops the editor from consuming key events. View() still
// renders the current state.
func (m *Model) Blur() { m.focused = false }

// Focused reports whether the editor consumes keys.
func (m Model) Focused() bool { return m.focused }

// Value returns the full buffer text joined with '\n' and without a
// trailing newline.
func (m Model) Value() string {
	return strings.Join(m.buf.lines(), "\n")
}

// SetValue replaces the buffer and resets the cursor to (0, 0). Dirty
// state and validation errors are cleared too \u2014 SetValue is used to
// switch documents, not to mutate the current one.
func (m *Model) SetValue(s string) {
	m.buf = newBuffer(s)
	m.cursor = position{}
	m.sel = nil
	m.dirty = false
	m.validationErrors = nil
}

// SetTitle changes the header text.
func (m *Model) SetTitle(t string) { m.title = t }

// SetSize updates the editor's outer dimensions. Sub-component sizes
// (viewport, footer area) are derived from these.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	vpHeight := height - chromeHeight(m)
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.vp.SetWidth(width)
	m.vp.SetHeight(vpHeight)
}

// Init implements tea.Model.
func (Model) Init() tea.Cmd { return nil }

// Dirty reports whether the buffer was modified since the last save
// or load. Useful for the host Model to render a "(modified)" marker
// in its own header if it likes.
func (m Model) Dirty() bool { return m.dirty }

// chromeHeight is the number of lines the editor reserves outside the
// viewport: 1 line header + 1 line footer + 1 line error bar when
// errors exist + 1 line search bar when active. Kept as a function
// (not a constant) so future configuration can change it without
// touching SetSize.
func chromeHeight(m *Model) int {
	h := 2 // header + footer
	if len(m.validationErrors) > 0 {
		h++
	}
	if m.searchSt != nil {
		h++
	}
	return h
}
