// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// chips.go defines baifo's visual "chip" primitive — a small inline
// pill used to surface contextual information in the footer area.
//
// A chip is conceptually:
//
//	┌ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┐
//	│  glyph  label · K   │
//	└ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘
//
// rendered inline as a single-line rounded box. Multiple chips line up
// horizontally with a one-column gap between them. The footer composes
// chips for: active interlocutor (root/static/dynamic), provider+model,
// active session, running workers count, secrets count, A2A status,
// config scope. The baifo version sits at the far right as plain faint
// text (no chip) because it's metadata, not state.
//
// Chips are the language. New contextual information goes through chip()
// or a chip-like helper here; nothing else in components.go should hand-
// craft bordered boxes.

// footerHeight is the number of terminal rows the chip footer takes
// up. Borderless pill chips are 1 row tall — no top/bottom corner
// padding — so the footer is a single line. Layout math (ZonesFor)
// reads this constant.
const footerHeight = 1

// chipStyle bundles the colour palette of a single chip. Today the
// three slots all default to text_dim — the chip itself is meant
// to recede into the background. A faint coloured glyph is the
// only place we let a chip carry entity colour, and that's only
// when the entity colour is dimmed enough not to compete with the
// composer's focus border.
type chipStyle struct {
	// Glyph is the foreground colour of the leading glyph. Most
	// chips keep this at text_dim. Entity / severity chips use a
	// dimmed entity tone so the eye can still tell chips apart
	// at a glance.
	Glyph color.Color

	// Label is the colour of the chip's descriptor (e.g.
	// "workers", "model"). Always text_dim.
	Label color.Color

	// Value is the colour of the chip's highlighted segment (the
	// name, the count, the status). text_dim so the whole chip
	// reads as quiet context — never bold, never accent.
	Value color.Color
}

// chip renders a single inline chip as a borderless pill: a short
// horizontal strip with a slightly raised background (colorBGAlt)
// and dim text on top. Padding is one column of breathing room on
// each side. Multiple chips line up on the same row separated by
// a single space.
//
// glyph is the leading icon (Theme.Glyph(...) is the canonical
// source), label is the descriptor, value is the meaningful bit
// the user actually looks at. Any of the three may be empty;
// empty parts are skipped along with their separators.
func chip(glyph, label, value string, style chipStyle) string {
	var inner strings.Builder
	wrote := false

	// Every inner span explicitly carries the pill background.
	// Lipgloss v2 doesn't inherit the wrapping style's Background
	// into child spans — each Render() call emits its own SGR
	// sequence that resets the bg unless we set it explicitly.
	// Without this, the pill looks "split": the bg shows in the
	// padding and the gaps, but where there's text the terminal
	// falls back to its own background.
	withBG := func(fg color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(fg).Background(colorBGFocus)
	}

	if glyph != "" {
		inner.WriteString(withBG(style.Glyph).Render(glyph))
		wrote = true
	}
	if label != "" {
		if wrote {
			inner.WriteString(withBG(style.Label).Render(" "))
		}
		inner.WriteString(withBG(style.Label).Render(label))
		wrote = true
	}
	if value != "" {
		if wrote {
			if label != "" {
				inner.WriteString(withBG(style.Label).Render(" · "))
			} else {
				inner.WriteString(withBG(style.Label).Render(" "))
			}
		}
		inner.WriteString(withBG(style.Value).Render(value))
	}

	// Borderless pill: dim text + slightly lifted background. The
	// background colour is duplicated on every inner span above so
	// the pill renders as a single solid surface in the terminal.
	pill := lipgloss.NewStyle().
		Background(colorBGFocus).
		Padding(0, 1)
	return pill.Render(inner.String())
}

// chipStyleForEntity returns the chip palette for an entity-kind
// chip. The glyph keeps a dimmed entity tone so chips remain
// distinguishable at a glance; label and value sit at text_dim so
// the chip reads as quiet context.
func chipStyleForEntity(kind string) chipStyle {
	return chipStyle{
		Glyph: dimmedEntityColor(kind),
		Label: colorTextDim,
		Value: colorTextDim,
	}
}

// chipStyleNeutral returns the chip palette for chips with no
// entity identity (e.g. model). Everything dim.
func chipStyleNeutral() chipStyle {
	return chipStyle{
		Glyph: colorTextDim,
		Label: colorTextDim,
		Value: colorTextDim,
	}
}

// chipStyleSeverity returns the chip palette for a status chip.
// The glyph picks up a dimmed severity tone (so warnings still
// catch the eye) while label and value stay text_dim.
func chipStyleSeverity(severity string) chipStyle {
	return chipStyle{
		Glyph: dimmedSeverityColor(severity),
		Label: colorTextDim,
		Value: colorTextDim,
	}
}

// dimmedEntityColor returns a softened version of the entity
// palette: we reuse the entity colour itself (kept dark enough by
// the palette designers) but let the surrounding dim-text context
// do the toning. Today this is just a passthrough to entityColor;
// the indirection exists so a future palette tweak can dim the
// glyphs further without touching every caller.
func dimmedEntityColor(kind string) color.Color {
	// In practice the entity colours we ship (cyan #22d3ee, violet
	// #a78bfa, etc.) read fine over the BGAlt background even
	// without further dimming. If a particular accent ever
	// overpowers the rest, swap the return for an entity-specific
	// subtle tone here.
	return entityColor(kind)
}

// dimmedSeverityColor mirrors dimmedEntityColor for the severity
// palette. The defaults pass straight through; same escape hatch
// for future per-severity dimming.
func dimmedSeverityColor(severity string) color.Color {
	return severityColor(severity)
}

// entityColor maps an entity kind to its colour. Mirrors the switch in
// Theme.EntityText so chips and inline text agree on the palette
// without forcing chip callers to construct a lipgloss style.
func entityColor(kind string) color.Color {
	switch kind {
	case "root":
		return colorRootAgent
	case "static":
		return colorStaticAgent
	case "dynamic":
		return colorDynamicWorker
	case "skill":
		return colorSkill
	case "mcp":
		return colorMCP
	case "provider":
		return colorProvider
	case "secret":
		return colorSecret
	case "session":
		return colorSession
	case "fact":
		// Facts share the muted session tone, matching Theme.EntityText.
		return colorSession
	default:
		return colorTextDim
	}
}

// severityColor maps a status string to one of the four severity
// palette colours. Anything unknown falls back to the dim text colour
// so a typo in a caller doesn't crash the render — it just makes the
// chip read as "no signal".
func severityColor(severity string) color.Color {
	switch severity {
	case "ok", "success":
		return colorSuccess
	case "warning", "insecure":
		return colorWarning
	case "error", "failed":
		return colorError
	case "info":
		return colorInfo
	default:
		return colorTextDim
	}
}

// kindShortLetter is the single-letter badge that goes inside the
// interlocutor chip's value (R/S/D for root/static/dynamic). It lets
// the eye distinguish the three kinds without reading the agent's
// name. Unknown kinds return an empty string so the value reads as
// just the name.
func kindShortLetter(kind string) string {
	switch kind {
	case "root":
		return "R"
	case "static":
		return "S"
	case "dynamic":
		return "D"
	default:
		return ""
	}
}
