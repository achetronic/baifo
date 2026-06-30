// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// header.go is the persistent top-of-screen chrome of the chat: the
// baifo logo on the left, a tasteful colour-faded ribbon to its
// right, and breathing-room padding above and below.
//
// There are no tabs any more — every alternate view (sessions,
// workers, settings, ...) is invoked through a slash command
// that opens a modal overlay, so the header only has one job:
// remind the user where they are.

// headerHeight is the number of terminal rows the header occupies.
// Exposed so layout math (ZonesFor) can subtract it without
// duplicating the constant.
//
// Layout (top to bottom):
//
//	row 0  blank      (top padding)
//	row 1  logo row 0 + fade ribbon
//	row 2  logo row 1 + fade ribbon
//	row 3  logo row 2 + fade ribbon
//	row 4  blank      (bottom padding)
const headerHeight = 5

// headerLogoMinWidth is the terminal width below which the header
// drops the logo (and the fade) entirely. Anything narrower would
// either wrap the logo or fail to fit even a few cells of fade,
// neither of which reads well.
const headerLogoMinWidth = 36

// headerLogoPaddingLeft is the column padding inserted before the
// logo so it breathes from the left edge instead of hugging it.
// Two columns is enough to read as "intentional whitespace"
// without pushing the brand off-centre on narrow terminals.
const headerLogoPaddingLeft = 2

// headerLogoPaddingRight mirrors the left padding so the fade
// ribbon doesn't run all the way to the terminal edge — leaving a
// couple of empty columns on the right keeps the silhouette
// symmetric with the left margin.
const headerLogoPaddingRight = 2

// headerLogoFadeGap is the number of blank columns between the
// last character of the logo and the first character of the fade
// ribbon. Wide enough that the brand reads as its own block
// before the ribbon starts — the ribbon is a flourish, not a
// continuation of the logo.
const headerLogoFadeGap = 4

// headerLogo is the three-row half-block "BAIFO" mark. Same letter
// shapes as the splash, just packed at half resolution so it fits
// the header band. Generated from a 5-pixel-tall font packed into
// half-block glyphs.
//
// Height: 3 rows.
var headerLogo = []string{
	"█▀▀█  ▄▀▀▄  █  █▀▀▀  █▀▀█",
	"█▀▀▄  █▀▀█  █  █▀▀   █  █",
	"▀▀▀▀  ▀  ▀  ▀  ▀     ▀▀▀▀",
}

// headerFadeRamp is the density ladder used to dissolve the
// ribbon into the terminal background. Each glyph covers
// progressively less of its cell, so the foreground colour is
// painted on a shrinking fraction of pixels while the rest of
// each cell shows whatever the terminal renders behind it.
//
// Crucially the *foreground colour never changes* along the
// ribbon — we keep painting the accent and only the glyph mass
// shrinks. That way we never have to know what the terminal's
// real background is: the dissolve happens through the holes in
// the glyph itself, not through interpolating towards a colour
// we'd be guessing at.
//
// The ramp is intentionally long and includes repeated `█` /
// space steps at the ends so the transitions in and out are
// gentle instead of jumping straight from full block to half
// shade.
var headerFadeRamp = []string{
	"█", "█", "█",
	"▓", "▓",
	"▒", "▒",
	"░", "░",
	" ", " ", " ",
}

// renderHeader paints the persistent header band: padded "baifo"
// logo on the left, a colour-fading ribbon of block characters
// to its right that decays from accent into the chat background,
// and blank rows above and below for breathing room.
func renderHeader(theme Theme, width int) string {
	showLogo := width >= headerLogoMinWidth+headerLogoPaddingLeft

	blank := strings.Repeat(" ", width)
	logoRows := make([]string, len(headerLogo))
	if showLogo {
		logoW := lipgloss.Width(headerLogo[0])
		left := strings.Repeat(" ", headerLogoPaddingLeft)

		// Compute the budget the fade ribbon has to fill: the
		// gap between the logo and the right padding. Negative
		// means the terminal is too narrow for a fade — degrade
		// gracefully to plain spaces.
		fadeStart := headerLogoPaddingLeft + logoW + headerLogoFadeGap
		fadeWidth := width - fadeStart - headerLogoPaddingRight
		gap := strings.Repeat(" ", headerLogoFadeGap)
		rightPad := strings.Repeat(" ", headerLogoPaddingRight)

		var fade string
		if fadeWidth > 0 {
			fade = buildHeaderFade(theme.Accent.Primary, fadeWidth)
		} else {
			fade = strings.Repeat(" ", width-fadeStart) // best-effort blank
			rightPad = ""
		}

		// Only the row that crosses the logo's vertical centre
		// carries the fade ribbon; the other rows keep just the
		// logo so the ribbon reads as a single horizontal stroke.
		midRow := len(headerLogo) / 2
		for i, line := range headerLogo {
			styled := theme.AccentText().Bold(true).Render(line)
			if i == midRow {
				logoRows[i] = left + styled + gap + fade + rightPad
			} else {
				logoRows[i] = left + styled
			}
		}
	} else {
		for i := range logoRows {
			logoRows[i] = blank
		}
	}

	// Stack: blank pad · logo rows · blank pad. The blank rows give
	// the brand breathing room from the terminal edge above and the
	// chat content below — the chat panel border already plays the
	// separator role the old faint rule used to, so we don't draw
	// one here.
	return blank + "\n" + strings.Join(logoRows, "\n") + "\n" + blank
}

// buildHeaderFade returns a string of width `cols` whose glyphs
// step through a density ramp from full block to space while the
// foreground colour stays fixed at `accent`. The dissolve happens
// through the glyph's own shrinking mass — we never paint a
// "background-coloured" cell, so the ribbon always blends into
// whatever the user's terminal actually renders behind it
// (black, dark grey, image, transparency, anything). No more
// visible step at the tail because there is no second colour to
// step to.
func buildHeaderFade(accent color.Color, cols int) string {
	if cols <= 0 {
		return ""
	}

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(hexFromColor(accent)))
	steps := len(headerFadeRamp)

	var b strings.Builder
	for i := 0; i < cols; i++ {
		// Position along the ribbon, 0.0 next to the logo →
		// 1.0 at the right edge, mapped onto the density ramp.
		var t float64
		if cols == 1 {
			t = 0
		} else {
			t = float64(i) / float64(cols-1)
		}
		idx := int(t * float64(steps-1))
		if idx >= steps {
			idx = steps - 1
		}
		glyph := headerFadeRamp[idx]
		if glyph == " " {
			// Spaces don't need any styling; emitting them
			// bare also avoids unnecessary ANSI noise.
			b.WriteByte(' ')
			continue
		}
		b.WriteString(style.Render(glyph))
	}
	return b.String()
}

// hexFromColor renders any color.Color as a "#rrggbb" string
// suitable for lipgloss.Color. Wraps rgbComponents + hexFromRGB
// so callers can pass theme colours straight in.
func hexFromColor(c color.Color) string {
	r, g, b := rgbComponents(c)
	return hexFromRGB(r, g, b)
}

// rgbComponents extracts the 8-bit RGB triple from any color.Color.
// color.Color.RGBA returns alpha-premultiplied 16-bit values; we
// scale them back down to 0–255 and ignore alpha (the terminal
// background composites for us).
func rgbComponents(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// hexFromRGB renders an RGB triple as a "#rrggbb" string suitable
// for lipgloss.Color.
func hexFromRGB(r, g, b uint8) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 7)
	out[0] = '#'
	out[1] = hex[r>>4]
	out[2] = hex[r&0x0f]
	out[3] = hex[g>>4]
	out[4] = hex[g&0x0f]
	out[5] = hex[b>>4]
	out[6] = hex[b&0x0f]
	return string(out)
}
