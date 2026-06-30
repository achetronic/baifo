// SPDX-License-Identifier: Apache-2.0

package agent

import "testing"

// TestNormalizeReasoningAPI checks canonicalisation: known values pass
// through (case/space-insensitive), everything else (including typos)
// collapses to "" meaning "no override".
func TestNormalizeReasoningAPI(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"enabled":   ReasoningAPIEnabled,
		"ADAPTIVE":  ReasoningAPIAdaptive,
		"  Enabled": ReasoningAPIEnabled,
		"adaptive ": ReasoningAPIAdaptive,
		"classic":   "", // not a recognised value
		"bogus":     "",
	}
	for in, want := range cases {
		if got := NormalizeReasoningAPI(in); got != want {
			t.Errorf("NormalizeReasoningAPI(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValidReasoningAPI: empty and the two canonical values are valid;
// anything else is rejected so the config boundary can complain.
func TestValidReasoningAPI(t *testing.T) {
	for _, v := range []string{"", "enabled", "adaptive", "ENABLED", " Adaptive "} {
		if !ValidReasoningAPI(v) {
			t.Errorf("ValidReasoningAPI(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"classic", "budget", "effort", "x"} {
		if ValidReasoningAPI(v) {
			t.Errorf("ValidReasoningAPI(%q) = true, want false", v)
		}
	}
}

// TestResolveAnthropicReasoningAPI_Override: a valid explicit override
// always wins, even against what the heuristic would pick.
func TestResolveAnthropicReasoningAPI_Override(t *testing.T) {
	// Opus 4.8 would heuristically be adaptive; force it to enabled.
	if got := resolveAnthropicReasoningAPI("claude-opus-4-8", "enabled"); got != ReasoningAPIEnabled {
		t.Errorf("override enabled: got %q, want enabled", got)
	}
	// A classic model would heuristically be enabled; force adaptive.
	if got := resolveAnthropicReasoningAPI("claude-sonnet-4-20250514", "adaptive"); got != ReasoningAPIAdaptive {
		t.Errorf("override adaptive: got %q, want adaptive", got)
	}
	// An invalid override is ignored and the heuristic applies.
	if got := resolveAnthropicReasoningAPI("claude-sonnet-4-20250514", "garbage"); got != ReasoningAPIEnabled {
		t.Errorf("invalid override should fall back to heuristic enabled, got %q", got)
	}
}

// TestResolveAnthropicReasoningAPI_GapModels: models the catalogue lists
// without reasoning_levels but which are effort-based must still resolve
// to adaptive via the override list. This guards the exact 400 we chased
// (Opus 4.5 / Haiku 4.5 rejecting the classic "enabled" form).
func TestResolveAnthropicReasoningAPI_GapModels(t *testing.T) {
	for _, id := range []string{
		"claude-opus-4-5-20251101",
		"claude-haiku-4-5-20251001",
	} {
		if got := resolveAnthropicReasoningAPI(id, ""); got != ReasoningAPIAdaptive {
			t.Errorf("gap model %q: got %q, want adaptive", id, got)
		}
	}
}

// TestResolveAnthropicReasoningAPI_Classic: older models with no
// reasoning_levels and not in the gap list default to the classic
// enabled API.
func TestResolveAnthropicReasoningAPI_Classic(t *testing.T) {
	for _, id := range []string{
		"claude-sonnet-4-20250514",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
		"claude-sonnet-4-5-20250929",
	} {
		if got := resolveAnthropicReasoningAPI(id, ""); got != ReasoningAPIEnabled {
			t.Errorf("classic model %q: got %q, want enabled", id, got)
		}
	}
}

// TestResolveAnthropicReasoningAPI_LevelledModels: models the catalogue
// marks with reasoning_levels resolve to adaptive via the heuristic
// (no override, no gap-list entry needed).
func TestResolveAnthropicReasoningAPI_LevelledModels(t *testing.T) {
	// These advertise reasoning_levels in catwalk's embedded catalogue.
	for _, id := range []string{
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-4-6",
	} {
		if got := resolveAnthropicReasoningAPI(id, ""); got != ReasoningAPIAdaptive {
			t.Errorf("levelled model %q: got %q, want adaptive", id, got)
		}
	}
}

// An unknown model (absent from the embedded catalogue) defaults to
// adaptive: unknown means newer than the catalogue, and every new
// Anthropic model rejects the classic "enabled" form with a 400.
func TestResolveAnthropicReasoningAPI_Unknown(t *testing.T) {
	if got := resolveAnthropicReasoningAPI("claude-future-99", ""); got != ReasoningAPIAdaptive {
		t.Errorf("unknown model: got %q, want adaptive", got)
	}
}
