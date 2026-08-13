// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/facade"
)

// TestChipContextGuard_HiddenWhenDisabled verifies the gauge chip is
// suppressed entirely when the guard is off, so the footer carries no
// permanently-inactive state.
func TestChipContextGuard_HiddenWhenDisabled(t *testing.T) {
	theme := NewTheme()
	if got := chipContextGuard(theme, false, "threshold", 0); got != "" {
		t.Errorf("disabled guard should render no chip, got %q", got)
	}
}

// TestChipContextGuard_ShowsStrategyAndPercent verifies the chip body
// carries the strategy label and the percentage when the guard is on.
func TestChipContextGuard_ShowsStrategyAndPercent(t *testing.T) {
	theme := NewTheme()
	got := chipContextGuard(theme, true, "threshold", 42)
	if !strings.Contains(got, "42%") {
		t.Errorf("chip should show the percentage, got %q", got)
	}
	if !strings.Contains(got, "tokens") {
		t.Errorf("threshold strategy should read as 'tokens', got %q", got)
	}

	win := chipContextGuard(theme, true, "sliding_window", 10)
	if !strings.Contains(win, "turns") {
		t.Errorf("sliding_window strategy should read as 'turns', got %q", win)
	}
}

func TestGuardSeverity_Escalates(t *testing.T) {
	cases := []struct {
		percent int
		want    string
	}{
		{0, "info"},
		{69, "info"},
		{70, "warning"},
		{89, "warning"},
		{90, "error"},
		{100, "error"},
	}
	for _, c := range cases {
		if got := guardSeverity(c.percent); got != c.want {
			t.Errorf("guardSeverity(%d) = %q, want %q", c.percent, got, c.want)
		}
	}
}

// TestBuildFooterChips_IncludesGuardWhenEnabled verifies the gauge chip
// is wired into the footer chip set when the guard is enabled.
func TestBuildFooterChips_IncludesGuardWhenEnabled(t *testing.T) {
	theme := NewTheme()
	chips := buildFooterChips(theme, statusBarData{
		Model:         "p/m",
		GuardEnabled:  true,
		GuardStrategy: "threshold",
		GuardPercent:  55,
	})
	joined := joinChips(chips, " ")
	if !strings.Contains(joined, "guard") || !strings.Contains(joined, "55%") {
		t.Errorf("footer should include the guard gauge chip, got %q", joined)
	}
}

// TestRefreshGuard_SeedDoesNotFireNotice verifies that the first
// (seed) read of a session that already carries a compaction does not
// spuriously announce it.
func TestRefreshGuard_SeedDoesNotFireNotice(t *testing.T) {
	f := &fakeFacade{guard: facade.ContextGuardStatus{
		Enabled: true, Strategy: "threshold", Percent: 50, Fingerprint: "100:42",
	}}
	m := NewModel(f, "v0")
	m.splash = false

	m.refreshGuard(false)

	if len(m.messages) != 0 {
		t.Fatalf("seed read should not append a notice, got %d messages", len(m.messages))
	}
	if !m.guard.Enabled || m.guard.Percent != 50 {
		t.Errorf("guard snapshot not stored: %+v", m.guard)
	}
}

// TestRefreshGuard_FiresNoticeOnNewCompaction verifies that once
// seeded, a changed fingerprint produces a highlighted MessageNotice.
func TestRefreshGuard_FiresNoticeOnNewCompaction(t *testing.T) {
	f := &fakeFacade{guard: facade.ContextGuardStatus{
		Enabled: true, Strategy: "threshold", Percent: 95, Fingerprint: "100:42",
	}}
	m := NewModel(f, "v0")
	m.splash = false

	m.refreshGuard(false) // seed
	f.guard.Fingerprint = "20:88"
	f.guard.Percent = 5 // dropped after compaction

	m.refreshGuard(true)

	if len(m.messages) != 1 {
		t.Fatalf("a fresh compaction should append exactly one notice, got %d", len(m.messages))
	}
	if m.messages[0].Kind != MessageNotice {
		t.Errorf("compaction row should be MessageNotice, got %v", m.messages[0].Kind)
	}

	// A second refresh with the same fingerprint must not duplicate it.
	m.refreshGuard(true)
	if len(m.messages) != 1 {
		t.Errorf("unchanged fingerprint should not append another notice, got %d", len(m.messages))
	}
}

// TestRefreshGuard_ClearedForWorkerChat verifies the gauge is hidden
// while a worker chat is active (the guard only tracks the root).
func TestRefreshGuard_ClearedForWorkerChat(t *testing.T) {
	f := &fakeFacade{guard: facade.ContextGuardStatus{Enabled: true, Percent: 80}}
	m := NewModel(f, "v0")
	m.splash = false
	m.activeInterlocutor = "worker-123"

	m.refreshGuard(true)

	if m.guard.Enabled {
		t.Errorf("guard should be cleared while a worker chat is active, got %+v", m.guard)
	}
	if len(m.messages) != 0 {
		t.Errorf("no notice should fire for a worker chat, got %d messages", len(m.messages))
	}
}
