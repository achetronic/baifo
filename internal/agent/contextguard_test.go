// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

func TestContextGuard_DisabledByDefault(t *testing.T) {
	// shouldGuard returns false for the zero ContextGuardConfig.
	// This is the boot path: users who don't write a context_guard
	// block get no plugin overhead at all.
	if shouldGuard(config.ContextGuardConfig{}) {
		t.Errorf("zero config should not enable the guard")
	}
}

func TestContextGuard_EnabledByEnabledFlag(t *testing.T) {
	if !shouldGuard(config.ContextGuardConfig{Enabled: true}) {
		t.Errorf("Enabled: true should activate the guard")
	}
}

func TestContextGuard_OptionsThresholdDefault(t *testing.T) {
	// No strategy field => threshold strategy => no extra options.
	opts := agentOptionsFor(config.ContextGuardConfig{Enabled: true})
	if len(opts) != 0 {
		t.Errorf("default strategy should yield 0 options, got %d", len(opts))
	}
}

func TestContextGuard_OptionsSlidingWindow(t *testing.T) {
	opts := agentOptionsFor(config.ContextGuardConfig{
		Enabled:  true,
		Strategy: "sliding_window",
		MaxTurns: 15,
	})
	if len(opts) != 1 {
		t.Errorf("sliding_window should yield 1 option, got %d", len(opts))
	}
}

func TestContextGuard_OptionsWithMaxTokens(t *testing.T) {
	// max_tokens stacks with the strategy.
	opts := agentOptionsFor(config.ContextGuardConfig{
		Enabled:   true,
		Strategy:  "sliding_window",
		MaxTurns:  20,
		MaxTokens: 50000,
	})
	if len(opts) != 2 {
		t.Errorf("sliding_window + max_tokens should yield 2 options, got %d", len(opts))
	}
}

func TestGuardThreshold_LargeWindowFixedBuffer(t *testing.T) {
	// Windows >= 200k reserve a fixed 20k buffer.
	if got := GuardThreshold(1_000_000); got != 980_000 {
		t.Errorf("GuardThreshold(1M) = %d, want 980000", got)
	}
	if got := GuardThreshold(200_000); got != 180_000 {
		t.Errorf("GuardThreshold(200k) = %d, want 180000", got)
	}
}

func TestGuardThreshold_SmallWindowRatioBuffer(t *testing.T) {
	// Smaller windows reserve 20%.
	if got := GuardThreshold(100_000); got != 80_000 {
		t.Errorf("GuardThreshold(100k) = %d, want 80000", got)
	}
}

func TestGuardThreshold_DegenerateWindow(t *testing.T) {
	// A zero/garbage window must not produce a non-positive threshold
	// (which would make the gauge divide by zero downstream).
	if got := GuardThreshold(0); got <= 0 {
		t.Errorf("GuardThreshold(0) = %d, want > 0", got)
	}
}

func TestGuardContextWindow_KnownAndUnknown(t *testing.T) {
	// An unknown model falls back to the registry default (128k) — the
	// point is that it never returns 0, which would break the gauge.
	if got := GuardContextWindow("definitely-not-a-real-model-xyz"); got <= 0 {
		t.Errorf("GuardContextWindow(unknown) = %d, want > 0", got)
	}
}
