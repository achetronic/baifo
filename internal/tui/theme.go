// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package tui implements baifo's terminal UI on top of charmbracelet's
// BubbleTea v2 stack. The single source of truth for visual decisions
// (palette, glyphs, spacing) is this file; components must import it
// and never hardcode colours or characters.
//
// See .agents/TUI_DESIGN.md for the spec; the values here mirror it
// exactly.
package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Base palette (fixed across every accent)
//
// In lipgloss v2 Color is a function returning image/color.Color, so
// we cache the parsed values once at package init rather than
// re-parsing the hex string on every style construction.

// The palette is the "Canarias" identity: black volcanic picón sand for
// backgrounds, lime-washed off-white for text, canarian clay for
// borders, and sun orange / lava red as the warm accents. It is fixed —
// baifo has a single, hand-tuned look, not a theme system.
var (
	colorBG        = lipgloss.Color("#211C18") // picón muy oscuro (fondo)
	colorBGAlt     = lipgloss.Color("#2B2520") // picón panel
	colorBGHover   = lipgloss.Color("#3A322E") // picón puro (fila seleccionada)
	colorBGFocus   = lipgloss.Color("#473C34") // picón aclarado
	colorBorder    = lipgloss.Color("#5E3119") // arcilla canaria
	colorText      = lipgloss.Color("#E8DDCB") // cal seca (blanco roto cálido)
	colorTextDim   = lipgloss.Color("#A89B86") // cal apagada
	colorTextFaint = lipgloss.Color("#6E6356") // cal tenue
	colorSuccess   = lipgloss.Color("#7FA650") // verde tunera/aloe
	colorWarning   = lipgloss.Color("#F2922B") // sol
	colorError     = lipgloss.Color("#C2412B") // lava viva (legible sobre picón)
	colorInfo      = lipgloss.Color("#C98A4B") // arcilla dorada
)

// colorLava is the pure cooled-lava red. It is intentionally darker than
// colorError and reserved for decorative borders/accents where contrast
// against text is not required; errors use the brighter colorError so
// they always read.
var colorLava = lipgloss.Color("#7E2114")

var _ = colorLava

// Silence the unused-variable lint for palette entries that the first
// pass of components does not consume yet. Removing this block when
// every colour is referenced is fine — it does nothing else.
var _ = []color.Color{colorBG, colorBGAlt, colorBGHover, colorBGFocus}

// Entity colours (fixed regardless of accent)

// Entity colours stay within the Canarias family so badges never clash
// with the warm picón background the way generic blues/violets would.
var (
	colorRootAgent     = lipgloss.Color("#F2922B") // sol
	colorStaticAgent   = lipgloss.Color("#C98A4B") // arcilla dorada
	colorDynamicWorker = lipgloss.Color("#C2412B") // lava viva
	colorSkill         = lipgloss.Color("#7FA650") // verde tunera
	colorMCP           = lipgloss.Color("#D9A066") // arcilla clara
	colorProvider      = lipgloss.Color("#B5533A") // lava clara
	colorSecret        = lipgloss.Color("#F2922B") // sol
	colorSession       = lipgloss.Color("#A89B86") // cal apagada
)

// Accent

// Accent bundles the three palette values that drive baifo's warm
// highlights: a primary tone, a brighter focus tone, and a desaturated
// subtle tone used as background tint on hover/selected rows. There is
// exactly one accent (canariasAccent); it is not user-configurable.
type Accent struct {
	Name    string
	Primary color.Color
	Focus   color.Color
	Subtle  color.Color
}

// canariasAccent is the single, fixed accent: sun orange primary, a
// brighter sun for focus, and canarian clay as the subtle background
// tint on hover/selected rows. baifo is not a theme system — this is the
// look.
var canariasAccent = Accent{
	Name:    "canarias",
	Primary: lipgloss.Color("#F2922B"), // sol
	Focus:   lipgloss.Color("#F6AC56"), // sol claro
	Subtle:  lipgloss.Color("#5E3119"), // arcilla
}

// Glyphs (Nerd Font primary, ASCII fallback)

type glyphSet struct {
	nerd  string
	ascii string
}

var glyphs = map[string]glyphSet{
	"root":        {nerd: "", ascii: "R"},
	"static":      {nerd: "", ascii: "S"},
	"dynamic":     {nerd: "", ascii: "D"},
	"skill":       {nerd: "", ascii: "k"},
	"mcp":         {nerd: "", ascii: "m"},
	"provider":    {nerd: "", ascii: "p"},
	"secret":      {nerd: "", ascii: "*"},
	"fact":        {nerd: "", ascii: "f"},
	"running":     {nerd: "", ascii: "~"},
	"done":        {nerd: "", ascii: "OK"},
	"failed":      {nerd: "", ascii: "x"},
	"idle":        {nerd: "", ascii: "."},
	"chevron":     {nerd: "›", ascii: ">"},
	"expanded":    {nerd: "˅", ascii: "v"},
	"bullet":      {nerd: "•", ascii: "*"},
	"arrow_right": {nerd: "→", ascii: "->"},
	"arrow_left":  {nerd: "←", ascii: "<-"},
	"gear":        {nerd: "", ascii: "*"},
	"search":      {nerd: "", ascii: "?"},
	"clock":       {nerd: "", ascii: "t"},
	"lock":        {nerd: "", ascii: "#"},
	"compact":     {nerd: "", ascii: "><"},
	"warn":        {nerd: "", ascii: "!"},
}

// Footer keycaps are ALWAYS ASCII, on purpose — they never consult the
// nerd_fonts flag. Symbols like ⏎ (U+23CE) and ↹ (U+21B9) are generic
// Unicode, not Nerd Font glyphs, and the default console fonts on
// Windows (Consolas, Lucida Console) lack them, so they render as tofu
// boxes (this was issue #1: the "resume" shortcut in /session). Plain
// words read on every terminal regardless of the user's font.
const (
	keyNav   = "[up/dn]" // ↑/↓ selection / scroll
	keyEnter = "[enter]" // ⏎ primary action
	keyTab   = "[tab]"   // ↹ completion
)

// Theme: the single object every component receives

// Theme is the runtime view of the palette + glyph configuration. The
// palette is fixed (Canarias); only the Nerd-Font flag varies, set
// once at construction via NewTheme.
type Theme struct {
	Accent      Accent
	UseNerdFont bool
}

// NewTheme builds a Theme from the user's preferences. The accent is
// NewTheme builds a Theme from the user's preferences. The palette is
// fixed (the Canarias accent); the only preference is whether to use
// Nerd Font glyphs.
func NewTheme(useNerdFont bool) Theme {
	return Theme{
		Accent:      canariasAccent,
		UseNerdFont: useNerdFont,
	}
}

// Glyph returns the glyph for name, picking the right variant for the
// active font mode. Unknown names return "?" so a missing entry is
// visible during development without breaking the layout.
func (t Theme) Glyph(name string) string {
	g, ok := glyphs[name]
	if !ok {
		return "?"
	}
	if t.UseNerdFont {
		return g.nerd
	}
	return g.ascii
}

// Styles

// PanelBorder returns the border style for an unfocused panel.
func (t Theme) PanelBorder() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1)
}

// PanelBorderFocused returns the border style for the focused panel.
// It only differs from PanelBorder in the border colour, which uses
// the active accent's primary tone.
func (t Theme) PanelBorderFocused() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent.Primary).
		Padding(0, 1)
}

// TabActive returns the style for the active tab label.
func (t Theme) TabActive() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(t.Accent.Primary).
		Bold(true)
}

// TabInactive returns the style for an inactive tab label.
func (t Theme) TabInactive() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorTextDim)
}

// PrimaryText returns the style for ordinary body text.
func (t Theme) PrimaryText() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorText)
}

// DimText is the muted variant — used for labels and timestamps.
func (t Theme) DimText() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorTextDim)
}

// FaintText is the most muted — placeholder hints and divider labels.
func (t Theme) FaintText() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorTextFaint)
}

// AccentText returns text painted in the active accent's primary tone.
func (t Theme) AccentText() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Accent.Primary)
}

// EntityText returns text painted in the colour assigned to the given
// entity kind. Unknown kinds fall back to the dim foreground.
func (t Theme) EntityText(kind string) lipgloss.Style {
	var c color.Color
	switch kind {
	case "root":
		c = colorRootAgent
	case "static":
		c = colorStaticAgent
	case "dynamic":
		c = colorDynamicWorker
	case "skill":
		c = colorSkill
	case "mcp":
		c = colorMCP
	case "provider":
		c = colorProvider
	case "secret":
		c = colorSecret
	case "session":
		c = colorSession
	case "fact":
		// Facts share the muted session tone — they are long-term
		// notes the user has accumulated, not active entities, so a
		// quiet colour fits better than a vivid one.
		c = colorSession
	default:
		c = colorTextDim
	}
	return lipgloss.NewStyle().Foreground(c)
}

// StatusOK / Warning / Error / Info are the four severity-coded text
// styles used for inline indicators (status bar, toasts, tool cards).

// StatusOK returns the green "success" style.
func (t Theme) StatusOK() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorSuccess)
}

// StatusWarning returns the amber "warning" style.
func (t Theme) StatusWarning() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorWarning)
}

// StatusError returns the red "error" style.
func (t Theme) StatusError() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorError)
}

// StatusInfo returns the amber "info" style.
func (t Theme) StatusInfo() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorInfo)
}

// SpecialHeaderBand returns the style for the colored header band that
// marks a "special" chat row (context-guard notice, agent error, and
// any future prominent in-flow event). The band paints the given
// colour as the background with the dark base as the foreground so the
// label reads clearly on top of it — a filled bar that catches the eye
// without wrapping the row in a border box. Bold for extra weight.
func (t Theme) SpecialHeaderBand(bg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colorBG).
		Background(bg).
		Bold(true)
}
