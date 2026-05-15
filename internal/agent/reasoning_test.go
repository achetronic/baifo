// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package agent

import (
	"testing"

	"google.golang.org/genai"
)

func TestNormalizeReasoning(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"  ":       "",
		"off":      "",
		"OFF":      "",
		"none":     "",
		"disabled": "",
		"Low":      ReasoningLow,
		"  HIGH ":  ReasoningHigh,
		"medium":   ReasoningMedium,
		"minimal":  ReasoningMinimal,
		"bogus":    "bogus", // passed through (lowercased) for the validator to reject
	}
	for in, want := range cases {
		if got := NormalizeReasoning(in); got != want {
			t.Errorf("NormalizeReasoning(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidReasoning(t *testing.T) {
	valid := []string{"", "off", "none", "minimal", "low", "medium", "high", "HIGH", " Low "}
	for _, v := range valid {
		if !ValidReasoning(v) {
			t.Errorf("ValidReasoning(%q) = false, want true", v)
		}
	}
	invalid := []string{"ultra", "max", "lowish", "1"}
	for _, v := range invalid {
		if ValidReasoning(v) {
			t.Errorf("ValidReasoning(%q) = true, want false", v)
		}
	}
}

// TestThinkingConfigForReasoning checks the request-level config is nil
// for unset effort (so non-reasoning models are never sent a setting)
// and carries the mapped level + budget otherwise.
func TestThinkingConfigForReasoning(t *testing.T) {
	if tc := thinkingConfigForReasoning(""); tc != nil {
		t.Errorf("unset effort must yield nil ThinkingConfig, got %+v", tc)
	}
	if tc := thinkingConfigForReasoning("off"); tc != nil {
		t.Errorf("off must yield nil ThinkingConfig, got %+v", tc)
	}

	cases := map[string]genai.ThinkingLevel{
		ReasoningMinimal: genai.ThinkingLevelMinimal,
		ReasoningLow:     genai.ThinkingLevelLow,
		ReasoningMedium:  genai.ThinkingLevelMedium,
		ReasoningHigh:    genai.ThinkingLevelHigh,
	}
	for effort, wantLevel := range cases {
		tc := thinkingConfigForReasoning(effort)
		if tc == nil {
			t.Fatalf("effort %q yielded nil config", effort)
		}
		if tc.ThinkingLevel != wantLevel {
			t.Errorf("effort %q level = %q, want %q", effort, tc.ThinkingLevel, wantLevel)
		}
		if tc.ThinkingBudget != nil {
			t.Errorf("effort %q must not carry a ThinkingBudget, got %v", effort, *tc.ThinkingBudget)
		}
	}
}

// TestReasoningBudgetTokens checks the anthropic budget mapping: 0 for
// unset, monotonically increasing and >= the Anthropic 1024 minimum.
func TestReasoningBudgetTokens(t *testing.T) {
	if b := reasoningBudgetTokens(""); b != 0 {
		t.Errorf("unset effort budget = %d, want 0", b)
	}
	min := reasoningBudgetTokens(ReasoningMinimal)
	low := reasoningBudgetTokens(ReasoningLow)
	med := reasoningBudgetTokens(ReasoningMedium)
	high := reasoningBudgetTokens(ReasoningHigh)
	if min < 1024 {
		t.Errorf("minimal budget %d below Anthropic floor of 1024", min)
	}
	if !(min < low && low < med && med < high) {
		t.Errorf("budgets not strictly increasing: %d %d %d %d", min, low, med, high)
	}
}
