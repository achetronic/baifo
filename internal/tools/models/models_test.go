// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package models

import (
	"strings"
	"testing"
)

// fakeCatalog is a trivial Catalog for table tests.
type fakeCatalog struct {
	refs []ProviderRef
}

func (f fakeCatalog) ConfiguredProviders() []ProviderRef { return f.refs }

// TestListModelsKnownProvider checks that a configured provider whose
// type matches the catwalk catalogue (gemini) reports a non-empty model
// list plus the small/large default hints.
func TestListModelsKnownProvider(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{refs: []ProviderRef{
		{Name: "gemini-main", Type: "gemini"},
	}}}

	res := tools.listModels(listModelsArgs{})
	if len(res.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(res.Providers))
	}
	p := res.Providers[0]
	if p.Provider != "gemini-main" || p.Type != "gemini" {
		t.Fatalf("provider identity wrong: %+v", p)
	}
	if len(p.Models) == 0 {
		t.Fatalf("expected gemini to have models from catwalk catalogue")
	}
	if p.DefaultSmall == "" && p.DefaultLarge == "" {
		t.Errorf("expected at least one of small/large default model ids")
	}
	if p.Note != "" {
		t.Errorf("known provider should not carry a note, got %q", p.Note)
	}
	// Models must be sorted by descending context window.
	for i := 1; i < len(p.Models); i++ {
		if p.Models[i-1].ContextWindow < p.Models[i].ContextWindow {
			t.Fatalf("models not sorted by descending context window at %d", i)
		}
	}
}

// TestListModelsUnknownType checks that a provider whose type catwalk
// does not catalogue reports a note instead of an empty silent list.
func TestListModelsUnknownType(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{refs: []ProviderRef{
		{Name: "weird", Type: "not-a-real-type"},
	}}}

	res := tools.listModels(listModelsArgs{})
	if len(res.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(res.Providers))
	}
	p := res.Providers[0]
	if p.Note == "" {
		t.Errorf("unknown provider type should carry an explanatory note")
	}
	if len(p.Models) != 0 {
		t.Errorf("unknown provider type should not list models, got %d", len(p.Models))
	}
}

// TestListModelsCustomEndpoint checks that a provider of a catalogued
// type (openai) but pointed at a custom url is reported with a note and
// no catalogue, since the catwalk OpenAI catalogue does not describe
// what an arbitrary OpenAI-compatible endpoint actually serves.
func TestListModelsCustomEndpoint(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{refs: []ProviderRef{
		{Name: "local-ollama", Type: "openai", URL: "http://localhost:11434/v1"},
	}}}

	res := tools.listModels(listModelsArgs{})
	if len(res.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(res.Providers))
	}
	p := res.Providers[0]
	if p.Note == "" {
		t.Errorf("custom-endpoint provider should carry an explanatory note")
	}
	if len(p.Models) != 0 {
		t.Errorf("custom-endpoint provider should not list catalogue models, got %d", len(p.Models))
	}
}

// TestListModelsCustomEndpointMatched checks that an OpenAI-compatible custom URL
// matching a known catalogue endpoint yields a matched list of models and a note.
func TestListModelsCustomEndpointMatched(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{refs: []ProviderRef{
		{Name: "router-provider", Type: "openai", URL: "https://openrouter.ai/api/v1"},
	}}}

	res := tools.listModels(listModelsArgs{})
	if len(res.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(res.Providers))
	}
	p := res.Providers[0]
	if len(p.Models) == 0 {
		t.Fatalf("expected openrouter to yield non-empty models")
	}
	if p.Note == "" {
		t.Errorf("matched custom provider should carry a note mentioning the match")
	}
	if !strings.Contains(p.Note, "openrouter") {
		t.Errorf("expected note to mention 'openrouter' provider, got %q", p.Note)
	}
}

// TestListModelsFilter narrows the result to a single configured
// provider by name (case-insensitive).
func TestListModelsFilter(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{refs: []ProviderRef{
		{Name: "gemini-main", Type: "gemini"},
		{Name: "openai-main", Type: "openai"},
	}}}

	res := tools.listModels(listModelsArgs{Provider: "OpenAI-Main"})
	if len(res.Providers) != 1 {
		t.Fatalf("expected filtered result of 1, got %d", len(res.Providers))
	}
	if res.Providers[0].Provider != "openai-main" {
		t.Fatalf("filter returned wrong provider: %s", res.Providers[0].Provider)
	}
}

// TestListModelsSortedByName checks providers come back in a stable,
// name-sorted order regardless of config order.
func TestListModelsSortedByName(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{refs: []ProviderRef{
		{Name: "zeta", Type: "openai"},
		{Name: "alpha", Type: "anthropic"},
	}}}

	res := tools.listModels(listModelsArgs{})
	if len(res.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(res.Providers))
	}
	if res.Providers[0].Provider != "alpha" || res.Providers[1].Provider != "zeta" {
		t.Fatalf("providers not name-sorted: %s, %s",
			res.Providers[0].Provider, res.Providers[1].Provider)
	}
}

// TestADKToolsRegistered confirms the tool builds and is named
// list_models, and that a nil catalog yields no tool (no panic).
func TestADKToolsRegistered(t *testing.T) {
	tools := &Tools{Catalog: fakeCatalog{}}
	out, err := tools.ADKTools()
	if err != nil {
		t.Fatalf("ADKTools error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out))
	}
	if out[0].Name() != "list_models" {
		t.Errorf("tool name = %q, want list_models", out[0].Name())
	}

	nilTools := &Tools{}
	out, err = nilTools.ADKTools()
	if err != nil {
		t.Fatalf("nil-catalog ADKTools error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("nil catalog should yield no tools, got %d", len(out))
	}
}
