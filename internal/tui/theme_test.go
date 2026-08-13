// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"
)

// TestGlyphsArePureASCII pins the guarantee behind issue #10: every
// glyph the theme serves is pure ASCII, so it renders on every
// terminal with every font. A glyph that sneaks into the map outside
// ASCII turns into a tofu box on default console fonts; this test
// fails the moment one does.
func TestGlyphsArePureASCII(t *testing.T) {
	theme := NewTheme()
	for name := range glyphs {
		g := theme.Glyph(name)
		if g == "" {
			t.Errorf("glyph %q is empty: an empty indicator renders as nothing (the issue #10 bug)", name)
		}
		for _, r := range g {
			if r > 127 {
				t.Errorf("glyph %q = %q contains non-ASCII rune U+%04X", name, g, r)
			}
		}
	}
}

// TestGlyphUnknownNameKeepsTheDevMarker protects the "?" fallback for
// unknown names: it is a deliberate development aid and must stay
// single-width ASCII too.
func TestGlyphUnknownNameKeepsTheDevMarker(t *testing.T) {
	theme := NewTheme()
	if got := theme.Glyph("definitely-not-a-glyph"); got != "?" {
		t.Errorf("unknown glyph: got %q, want \"?\"", got)
	}
}
