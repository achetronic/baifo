// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
)

// TestSplashLargeLogoIsTallerThanSmall is a soft guard: the large
// logo must be strictly taller than the legacy small one. Catches
// accidental swaps in the size tier selection.
func TestSplashLargeLogoIsTallerThanSmall(t *testing.T) {
	if len(splashLogoLarge) <= len(splashLogoSmall) {
		t.Errorf("large logo (%d rows) should be taller than small (%d rows)",
			len(splashLogoLarge), len(splashLogoSmall))
	}
}

// TestRenderSplashPicksLargeAtWideWidth confirms the large logo is
// used when the terminal is wide enough.
func TestRenderSplashPicksLargeAtWideWidth(t *testing.T) {
	theme := NewTheme(false)
	out := renderSplash(theme, 80)
	if !strings.Contains(out, strings.TrimSpace(splashLogoLarge[0])) {
		t.Errorf("renderSplash(80) did not include large logo first row\n%s", out)
	}
}

// TestRenderSplashFallsBackToSmallAtMediumWidth checks that the
// small logo is still used for terminals between 24 and 35 cols.
func TestRenderSplashFallsBackToSmallAtMediumWidth(t *testing.T) {
	theme := NewTheme(false)
	out := renderSplash(theme, 30)
	if !strings.Contains(out, strings.TrimSpace(splashLogoSmall[0])) {
		t.Errorf("renderSplash(30) did not include small logo first row\n%s", out)
	}
}

// TestRenderSplashFallsBackToPlainAtTinyWidth checks that very narrow
// terminals get the plain \"baifo\" fallback (no logo glyphs).
func TestRenderSplashFallsBackToPlainAtTinyWidth(t *testing.T) {
	theme := NewTheme(false)
	out := renderSplash(theme, 10)
	if strings.Contains(out, "█") {
		t.Errorf("renderSplash(10) should NOT include block glyphs\n%s", out)
	}
}
