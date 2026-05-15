// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package models registers the list_models tool the root agent uses to
// discover which LLM models each configured provider offers. The root
// needs this when composing dynamic workers: it lets the model pick a
// smaller, cheaper model for a trivial sub-task or a larger one for a
// hard task, instead of blindly reusing the root's own model.
//
// The catalogue is catwalk's embedded model database (the same source
// the TUI uses for model autocompletion and that contextguard uses for
// per-model context windows), so listing models is a zero-I/O lookup,
// no network call to the provider's /models endpoint. A provider whose
// type catwalk does not catalogue, or one pointed at a custom endpoint
// via url (any OpenAI-compatible server: Ollama, OpenRouter, vLLM, ...),
// reports a note instead of a catalogue so the LLM does not assume the
// endpoint serves the canonical model list.
package models

import (
	"sort"
	"strings"

	"github.com/achetronic/baifo/internal/modelcatalog"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ProviderRef is one provider as declared in baifo.yaml: its unique
// configured Name (what the root passes as `llm.provider` in a spawn
// spec), its Type (gemini, openai, anthropic), which is what we map to
// the catwalk catalogue, and its URL. A non-empty URL marks a custom
// endpoint: the catwalk catalogue for the type cannot describe what that
// endpoint actually serves, so it is reported with a note instead.
type ProviderRef struct {
	Name string
	Type string
	URL  string
}

// Catalog is the dependency the tool needs from the App: the list of
// providers the operator actually configured. Kept narrow so this
// package does not import internal/app or internal/config.
type Catalog interface {
	// ConfiguredProviders returns the providers declared in baifo.yaml.
	ConfiguredProviders() []ProviderRef
}

// Tools bundles the dependencies needed to build the list_models tool.
type Tools struct {
	Catalog Catalog
}

// listModelsArgs is the (optional) input. When Provider is set the
// result is narrowed to that single configured provider name; empty
// lists every configured provider.
type listModelsArgs struct {
	Provider string `json:"provider,omitempty"`
}

// ModelInfo is one model's relevant metadata for a sizing decision.
// Cost is per 1M tokens in USD as catwalk records it.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int64  `json:"context_window,omitempty"`
	MaxOutput     int64  `json:"max_output_tokens,omitempty"`
	CanReason     bool   `json:"can_reason,omitempty"`
	// ReasoningLevels lists the effort levels this model accepts (e.g.
	// "low", "medium", "high"), when the catalogue records them. Pass
	// one of these as `reasoning` in a spawn spec to control how hard
	// the worker thinks. Empty when the model is not a reasoning model
	// or the catalogue has no level data.
	ReasoningLevels []string `json:"reasoning_levels,omitempty"`
	// DefaultReasoningEffort is the effort the model uses when none is
	// requested. Empty when unknown / not applicable.
	DefaultReasoningEffort string  `json:"default_reasoning_effort,omitempty"`
	CostPer1MIn            float64 `json:"cost_per_1m_in,omitempty"`
	CostPer1MOut           float64 `json:"cost_per_1m_out,omitempty"`
}

// ProviderModels is the per-provider block of the result. DefaultSmall
// and DefaultLarge are catwalk's own "small/cheap" and "large/capable"
// picks for the provider (the cheapest signal for "use a smaller or
// bigger model" the root asked for).
type ProviderModels struct {
	Provider     string      `json:"provider"`
	Type         string      `json:"type"`
	DefaultSmall string      `json:"default_small_model,omitempty"`
	DefaultLarge string      `json:"default_large_model,omitempty"`
	Models       []ModelInfo `json:"models,omitempty"`
	Note         string      `json:"note,omitempty"`
}

// ListModelsResult is the tool's return value.
type ListModelsResult struct {
	Providers []ProviderModels `json:"providers"`
}

// ADKTools returns the single list_models tool. Returns an empty slice
// (no error) when no catalog is wired, so a misconfiguration degrades
// to "tool absent" rather than a boot failure.
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	if t.Catalog == nil {
		return nil, nil
	}
	tl, err := functiontool.New(
		functiontool.Config{
			Name: "list_models",
			Description: "List the LLM models available from the providers configured in baifo. " +
				"Use this before spawning a dynamic agent or authoring a static one to choose a " +
				"model that fits the task: a smaller/cheaper model for simple work, a larger/more " +
				"capable one for hard work. Each provider reports default_small_model and " +
				"default_large_model as quick picks, plus every known model with its context window, " +
				"per-1M-token cost, and reasoning support (can_reason, reasoning_levels, " +
				"default_reasoning_effort). When a model lists reasoning_levels you may set one as " +
				"the `reasoning` field of a spawn spec to control how hard the worker thinks. " +
				"Optional `provider` argument narrows the result to one configured provider by name.",
		},
		func(_ tool.Context, a listModelsArgs) (ListModelsResult, error) {
			return t.listModels(a), nil
		},
	)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{tl}, nil
}

// listModels builds the result by mapping each configured provider's
// type onto the catwalk catalogue. The mapping is pure and offline.
func (t *Tools) listModels(a listModelsArgs) ListModelsResult {
	refs := t.Catalog.ConfiguredProviders()
	filter := strings.TrimSpace(a.Provider)

	out := make([]ProviderModels, 0, len(refs))
	for _, ref := range refs {
		if filter != "" && !strings.EqualFold(ref.Name, filter) {
			continue
		}
		out = append(out, buildProviderModels(ref))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return ListModelsResult{Providers: out}
}

// buildProviderModels resolves one configured provider against the
// model catalog package.
func buildProviderModels(ref ProviderRef) ProviderModels {
	pm := ProviderModels{Provider: ref.Name, Type: ref.Type}

	cw, matchKind, ok := modelcatalog.Resolve(ref.Type, ref.URL)
	if !ok || matchKind == modelcatalog.MatchNone {
		if strings.TrimSpace(ref.URL) != "" {
			pm.Note = "provider " + quote(ref.Name) + " points at a custom endpoint; " +
				"baifo has no catalogue for it. Use any model id the endpoint serves; " +
				"baifo does not restrict it."
		} else {
			pm.Note = "no built-in model catalogue for provider type " + quote(ref.Type) +
				". Use any model id the endpoint serves; baifo does not restrict it."
		}
		return pm
	}

	pm.DefaultSmall = cw.DefaultSmallModelID
	pm.DefaultLarge = cw.DefaultLargeModelID
	pm.Models = make([]ModelInfo, 0, len(cw.Models))
	for _, m := range cw.Models {
		pm.Models = append(pm.Models, ModelInfo{
			ID:                     m.ID,
			Name:                   m.Name,
			ContextWindow:          m.ContextWindow,
			MaxOutput:              m.DefaultMaxTokens,
			CanReason:              m.CanReason,
			ReasoningLevels:        m.ReasoningLevels,
			DefaultReasoningEffort: m.DefaultReasoningEffort,
			CostPer1MIn:            m.CostPer1MIn,
			CostPer1MOut:           m.CostPer1MOut,
		})
	}
	// Largest context window first: a useful default ordering when the
	// root scans for "the biggest model" without reading every entry.
	sort.SliceStable(pm.Models, func(i, j int) bool {
		if pm.Models[i].ContextWindow != pm.Models[j].ContextWindow {
			return pm.Models[i].ContextWindow > pm.Models[j].ContextWindow
		}
		return pm.Models[i].ID < pm.Models[j].ID
	})

	if matchKind == modelcatalog.MatchByURLExact || matchKind == modelcatalog.MatchByURLHost {
		pm.Note = "models shown are the " + string(cw.ID) + " catalogue matched by url; the endpoint may serve a different subset"
	}

	return pm
}

// quote wraps s in double quotes for readable notes without pulling
// fmt for a single call site.
func quote(s string) string {
	return "\"" + s + "\""
}
