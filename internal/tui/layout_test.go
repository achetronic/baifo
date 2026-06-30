// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import "testing"

func TestClassifyByWidth(t *testing.T) {
	const okHeight = 40
	cases := []struct {
		width int
		want  LayoutMode
	}{
		{10, LayoutNarrow},
		{79, LayoutNarrow},
		{80, LayoutNormal},
		{119, LayoutNormal},
		{120, LayoutWide},
		{200, LayoutWide},
	}
	for _, c := range cases {
		if got := Classify(c.width, okHeight); got != c.want {
			t.Errorf("Classify(%d, %d): got %v, want %v", c.width, okHeight, got, c.want)
		}
	}
}

func TestClassifyHeightGateBeatsWidth(t *testing.T) {
	if got := Classify(200, 10); got != LayoutTooSmall {
		t.Errorf("low height should win: got %v", got)
	}
	if got := Classify(40, 23); got != LayoutTooSmall {
		t.Errorf("height=23 should be too small: got %v", got)
	}
}

func TestSidebarVisibleOnlyWide(t *testing.T) {
	cases := map[LayoutMode]bool{
		LayoutTooSmall: false,
		LayoutNarrow:   false,
		LayoutNormal:   false,
		LayoutWide:     true,
	}
	for mode, want := range cases {
		if got := mode.SidebarVisible(); got != want {
			t.Errorf("%v.SidebarVisible(): got %v, want %v", mode, got, want)
		}
	}
}

func TestTabsCollapsedOnlyNarrow(t *testing.T) {
	cases := map[LayoutMode]bool{
		LayoutTooSmall: false,
		LayoutNarrow:   true,
		LayoutNormal:   false,
		LayoutWide:     false,
	}
	for mode, want := range cases {
		if got := mode.TabsCollapsed(); got != want {
			t.Errorf("%v.TabsCollapsed(): got %v, want %v", mode, got, want)
		}
	}
}

func TestZonesForWithoutComposer(t *testing.T) {
	z := ZonesFor(40, false)
	if z.Tabs != headerHeight || z.Status != footerHeight || z.Composer != 0 {
		t.Errorf("fixed zones wrong: %+v", z)
	}
	// No composer ⇒ no streaming bar either.
	if z.StreamingBar != 0 {
		t.Errorf("StreamingBar should be 0 without composer, got %d", z.StreamingBar)
	}
	want := 40 - headerHeight - footerHeight
	if z.Main != want {
		t.Errorf("Main: got %d, want %d", z.Main, want)
	}
}

func TestZonesForWithComposer(t *testing.T) {
	z := ZonesFor(40, true)
	if z.Composer != 5 {
		t.Errorf("Composer: got %d, want 5", z.Composer)
	}
	if z.StreamingBar != streamingBarHeight {
		t.Errorf("StreamingBar: got %d, want %d", z.StreamingBar, streamingBarHeight)
	}
	want := 40 - headerHeight - footerHeight - 5 - streamingBarHeight
	if z.Main != want {
		t.Errorf("Main: got %d, want %d", z.Main, want)
	}
}

func TestZonesForTinyHeightStillReturnsPositiveMain(t *testing.T) {
	z := ZonesFor(2, true)
	if z.Main < 1 {
		t.Errorf("Main should never be < 1, got %d", z.Main)
	}
}
