// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package agent

import (
	"strings"

	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/providers"
)

// reasoning.go owns the provider-agnostic mapping from baifo's
// `reasoning` effort knob (set per agent in agents.yaml or per dynamic
// spawn) onto the concrete settings each LLM backend understands.
//
// The effort levels are intentionally few and human-readable, mirroring
// the catwalk catalogue's `reasoning_levels` and OpenAI's reasoning
// effort vocabulary, so the root can copy a value straight out of
// list_models into a spawn spec:
//
//	minimal | low | medium | high
//
// An empty value means "leave the model's own default untouched" — we
// never send a reasoning setting we weren't asked for, because doing so
// errors on non-reasoning models (e.g. gpt-4o rejects reasoning_effort).
//
// Two delivery paths exist because the backends disagree on where the
// knob lives:
//
//   - openai (o-series) and gemini read it REQUEST-side from
//     GenerateContentConfig.ThinkingConfig. thinkingConfigForReasoning
//     builds that.
//   - anthropic (adk-utils adapter) reads it only at CONSTRUCTION time
//     via a token budget. reasoningBudgetTokens maps the effort to a
//     budget the provider builder applies.
//
// Both are derived from the same effort string so a single config field
// drives every provider consistently.

// ReasoningEffort constants — the accepted values of the `reasoning`
// field. Empty string is the implicit "unset / model default".
const (
	ReasoningOff     = "off"
	ReasoningMinimal = "minimal"
	ReasoningLow     = "low"
	ReasoningMedium  = "medium"
	ReasoningHigh    = "high"
)

// NormalizeReasoning lowercases and trims a reasoning value and maps the
// "off" alias (and "none") to the empty string, which the rest of the
// pipeline treats as "do not set". Returns the canonical effort or "".
func NormalizeReasoning(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ReasoningOff, "none", "disabled":
		return ""
	case ReasoningMinimal:
		return ReasoningMinimal
	case ReasoningLow:
		return ReasoningLow
	case ReasoningMedium:
		return ReasoningMedium
	case ReasoningHigh:
		return ReasoningHigh
	default:
		// Unknown values are passed through lowercased so the caller's
		// validator can reject them with a clear message rather than us
		// silently swallowing a typo.
		return strings.ToLower(strings.TrimSpace(s))
	}
}

// ReasoningConfig encapsulates all the backend-specific reasoning settings
// generated from a single baifo effort string. The builder consumes this
// without having to know the provider-specific plumbing.
type ReasoningConfig struct {
	// Request-level config (consumed by Gemini and OpenAI).
	GenerateContentConfig *genai.GenerateContentConfig

	// Construction-time config (consumed by Anthropic).
	ModelOptions *providers.ModelOptions
}

// BuildReasoningConfig turns the raw effort and api override strings
// into concrete provider configs. Returns an empty ReasoningConfig if
// no effort was requested.
func BuildReasoningConfig(modelID, effort, apiOverride string) ReasoningConfig {
	var rc ReasoningConfig

	norm := NormalizeReasoning(effort)
	if norm == "" {
		return rc
	}

	// 1. Request-level path (Gemini, OpenAI)
	tc := thinkingConfigForReasoning(norm)
	if tc != nil {
		rc.GenerateContentConfig = &genai.GenerateContentConfig{
			ThinkingConfig: tc,
		}
	}

	// 2. Construction-time path (Anthropic)
	budget := reasoningBudgetTokens(norm)
	mode := resolveAnthropicReasoningAPI(modelID, apiOverride)
	rc.ModelOptions = &providers.ModelOptions{
		ThinkingEffort:       norm,
		ThinkingBudgetTokens: budget,
		ThinkingMode:         mode,
		// Anthropic requires the response ceiling to exceed the
		// thinking budget; give the final answer room on top of it.
		MaxOutputTokens: budget + 4096,
	}

	return rc
}

// ValidReasoning reports whether s is a recognised effort (after
func ValidReasoning(s string) bool {
	switch NormalizeReasoning(s) {
	case "", ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh:
		return true
	default:
		return false
	}
}

// thinkingConfigForReasoning builds the request-level ThinkingConfig for
// the openai/gemini path. Returns nil for an unset effort so we never
// attach reasoning to a model that wasn't asked to reason. ThinkingLevel
// (openai effort, gemini level) is set. We do NOT set ThinkingBudget
// here because Gemini's API rejects requests that specify both a level
// and a token budget. Anthropic receives its budget via ModelOptions
// instead.
func thinkingConfigForReasoning(effort string) *genai.ThinkingConfig {
	level, _, ok := reasoningLevelAndBudget(effort)
	if !ok {
		return nil
	}
	return &genai.ThinkingConfig{
		IncludeThoughts: true,
		ThinkingLevel:   level,
	}
}

// reasoningBudgetTokens maps an effort to the anthropic thinking budget
// (output tokens reserved for internal reasoning). Returns 0 for an
// unset effort so the anthropic builder leaves extended thinking off.
func reasoningBudgetTokens(effort string) int {
	_, budget, ok := reasoningLevelAndBudget(effort)
	if !ok {
		return 0
	}
	return budget
}

// reasoningLevelAndBudget is the single source of truth mapping an
// effort string to a genai.ThinkingLevel and an anthropic-style token
// budget. ok is false for the unset/unknown case so callers emit
// "leave default" rather than a zero-effort config.
//
// The token budgets only matter for the anthropic adapter (openai and
// gemini take the discrete ThinkingLevel instead). Anthropic's only
// constraint is budget_tokens >= 1024 and < max_output_tokens; there is
// no official low/medium/high vocabulary, so we map our words onto a
// budget scale that spans Anthropic's practical range — from the 1024
// floor (quick checks) up to 32k (deep, multi-step reasoning). The
// steps are spread geometrically so each level is a meaningful jump.
func reasoningLevelAndBudget(effort string) (genai.ThinkingLevel, int, bool) {
	switch NormalizeReasoning(effort) {
	case ReasoningMinimal:
		return genai.ThinkingLevelMinimal, 1024, true
	case ReasoningLow:
		return genai.ThinkingLevelLow, 6144, true
	case ReasoningMedium:
		return genai.ThinkingLevelMedium, 16384, true
	case ReasoningHigh:
		return genai.ThinkingLevelHigh, 32768, true
	default:
		return genai.ThinkingLevelUnspecified, 0, false
	}
}
