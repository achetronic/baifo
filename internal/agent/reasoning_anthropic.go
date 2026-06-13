// Licensed under the Apache License, Version 2.0; see LICENSE.

package agent

import (
	"strings"
	"sync"

	"charm.land/catwalk/pkg/embedded"
)

// reasoning_anthropic.go decides which Anthropic reasoning API a given
// model speaks: the classic budget-based one ("enabled") or the newer
// effort-based one ("adaptive"). Anthropic rejects the wrong shape with a
// 400, so this choice must be right per model.
//
// There is no field in the catwalk catalogue that states the API
// directly. The usable signal is reasoning_levels: models on the
// effort-based API advertise discrete levels (low/medium/high/...),
// while classic-API models advertise none. That heuristic is correct
// for every model EXCEPT a couple the catalogue lists with no levels
// despite being effort-based (Opus 4.5, Haiku 4.5). For those, and for
// any future gap, the caller can override the result explicitly.

// Anthropic reasoning API identifiers. These mirror the adk-utils
// anthropic adapter's ThinkingMode values and are what the builder
// forwards through providers.ModelOptions.ThinkingMode.
const (
	ReasoningAPIEnabled  = "enabled"
	ReasoningAPIAdaptive = "adaptive"
)

// anthropicProviderType is the catwalk provider ID for Anthropic.
const anthropicProviderType = "anthropic"

// adaptiveModelOverrides lists model-ID substrings that the catwalk
// catalogue does NOT mark with reasoning_levels but which nonetheless
// speak the adaptive API (they reject the classic "enabled" form). They
// are matched case-insensitively as substrings of the model ID so the
// dated suffixes (e.g. "-20251101") still match. This is the one place
// that encodes catalogue gaps; keep it short and documented.
var adaptiveModelOverrides = []string{
	"opus-4-5",
	"haiku-4-5",
}

var (
	anthropicLevelsIndex     map[string][]string
	anthropicLevelsIndexOnce sync.Once
)

// anthropicLevels returns the catwalk reasoning_levels for every
// Anthropic model, keyed by model ID, built once and cached.
func anthropicLevels() map[string][]string {
	anthropicLevelsIndexOnce.Do(func() {
		anthropicLevelsIndex = make(map[string][]string)
		for _, p := range embedded.GetAll() {
			if !strings.EqualFold(string(p.ID), anthropicProviderType) {
				continue
			}
			for _, m := range p.Models {
				anthropicLevelsIndex[m.ID] = m.ReasoningLevels
			}
		}
	})
	return anthropicLevelsIndex
}

// NormalizeReasoningAPI lowercases and trims an explicit reasoning-API
// override and maps it to a canonical value. Empty (and unrecognised
// input) returns "" meaning "no override, use the heuristic".
func NormalizeReasoningAPI(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ReasoningAPIEnabled:
		return ReasoningAPIEnabled
	case ReasoningAPIAdaptive:
		return ReasoningAPIAdaptive
	default:
		return ""
	}
}

// ValidReasoningAPI reports whether s is a recognised override value.
// Empty is valid: it means "no override".
func ValidReasoningAPI(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ReasoningAPIEnabled, ReasoningAPIAdaptive:
		return true
	default:
		return false
	}
}

// resolveAnthropicReasoningAPI picks the reasoning API for an Anthropic
// model. An explicit, valid override always wins. Otherwise it applies
// the heuristic: a known adaptive-gap model => adaptive; a model the
// catalogue marks with reasoning_levels => adaptive; a model the
// catalogue knows but lists without levels => enabled (genuinely
// classic); a model the catalogue does NOT know => adaptive, because an
// unknown ID is newer than the embedded catalogue and every new
// Anthropic model speaks the adaptive API (the classic "enabled" form
// gets rejected with a 400, as claude-fable-5 demonstrated).
//
// modelID is the bare model identifier (e.g. "claude-opus-4-8").
func resolveAnthropicReasoningAPI(modelID, override string) string {
	if v := NormalizeReasoningAPI(override); v != "" {
		return v
	}

	id := strings.ToLower(strings.TrimSpace(modelID))
	for _, frag := range adaptiveModelOverrides {
		if strings.Contains(id, frag) {
			return ReasoningAPIAdaptive
		}
	}

	levels, known := anthropicLevels()[modelID]
	if len(levels) > 0 {
		return ReasoningAPIAdaptive
	}
	if !known {
		return ReasoningAPIAdaptive
	}

	return ReasoningAPIEnabled
}
