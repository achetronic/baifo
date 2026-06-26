// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package main

import (
	"path/filepath"
	"testing"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/secrets"
)

// secretRef builds the "${secret:NAME}" placeholder at runtime. We avoid
// writing the literal token contiguously in source because this harness
// expands ${secret:...} inside tool arguments before they hit disk.
func secretRef(name string) string { return "$" + "{secret:" + name + "}" }

// writeAndLoad writes the injected templates for choice c into a temp dir
// and loads them through the real loaders, returning the parsed config,
// agents file and the temp dir (so callers can open the secrets store).
// Asserting on the loaded structs (not the raw YAML) keeps the tests
// immune to the documentation comments the templates carry.
func writeAndLoad(t *testing.T, c providerChoice) (*config.Config, *config.AgentsFile, string) {
	t.Helper()
	dir := t.TempDir()
	if err := scaffoldConfigDirWithProvider(dir, c); err != nil {
		t.Fatalf("scaffoldConfigDirWithProvider: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatalf("generated baifo.yaml does not load: %v", err)
	}
	agents, err := config.LoadAgents(filepath.Join(dir, config.AgentsFileName))
	if err != nil {
		t.Fatalf("generated agents.yaml does not load: %v", err)
	}
	return cfg, agents, dir
}

// getSecret opens the plaintext store in dir and returns the stored value
// for name, failing the test if it cannot be read.
func getSecret(t *testing.T, dir, name string) string {
	t.Helper()
	store, err := secrets.NewStore(dir, "")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	v, err := store.Get(name)
	if err != nil {
		t.Fatalf("get secret %q: %v", name, err)
	}
	return v
}

// assertSingleProvider checks the config declares exactly one provider and
// returns it for further assertions.
func assertSingleProvider(t *testing.T, cfg *config.Config) config.ProviderEntry {
	t.Helper()
	if len(cfg.Providers) != 1 {
		t.Fatalf("want exactly 1 provider, got %d: %+v", len(cfg.Providers), cfg.Providers)
	}
	return cfg.Providers[0]
}

// assertRoot checks the root agent points at the expected provider/model
// and that the utility agent was left empty (falls back to the root).
func assertRoot(t *testing.T, agents *config.AgentsFile, provider, model string) {
	t.Helper()
	root := agents.RootAgent()
	if root == nil {
		t.Fatal("no root agent")
	}
	if root.LLM.Provider != provider {
		t.Errorf("root provider: got %q, want %q", root.LLM.Provider, provider)
	}
	if root.LLM.Model != model {
		t.Errorf("root model: got %q, want %q", root.LLM.Model, model)
	}
	if u := agents.UtilityAgent(); u != nil {
		if u.LLM.Provider != "" || u.LLM.Model != "" {
			t.Errorf("utility agent must stay empty, got provider=%q model=%q", u.LLM.Provider, u.LLM.Model)
		}
	}
}

func TestWizardConfig_GeminiAPIKey(t *testing.T) {
	const key = "test-gemini-key-1234567890"
	c := providerChoice{
		name: "gemini", typ: "gemini",
		apiKey: key, secretName: "GEMINI_API_KEY",
		model: "gemini-2.5-flash",
	}
	cfg, agents, dir := writeAndLoad(t, c)

	p := assertSingleProvider(t, cfg)
	if p.Name != "gemini" || p.Type != "gemini" {
		t.Errorf("provider id/type: got %q/%q", p.Name, p.Type)
	}
	if p.APIKey != secretRef("GEMINI_API_KEY") {
		t.Errorf("api_key ref: got %q", p.APIKey)
	}
	if p.URL != "" {
		t.Errorf("gemini must not set a url, got %q", p.URL)
	}
	assertRoot(t, agents, "gemini", "gemini-2.5-flash")
	if got := getSecret(t, dir, "GEMINI_API_KEY"); got != key {
		t.Errorf("stored secret: got %q, want %q", got, key)
	}
}

func TestWizardConfig_OpenAIOfficial(t *testing.T) {
	const key = "sk-official-abc"
	c := providerChoice{
		name: "openai", typ: "openai",
		apiKey: key, secretName: "OPENAI_API_KEY",
		model: "gpt-4o",
	}
	cfg, agents, dir := writeAndLoad(t, c)

	p := assertSingleProvider(t, cfg)
	if p.Name != "openai" || p.Type != "openai" {
		t.Errorf("provider id/type: got %q/%q", p.Name, p.Type)
	}
	if p.URL != "" {
		t.Errorf("official OpenAI must not set a url, got %q", p.URL)
	}
	if p.APIKey != secretRef("OPENAI_API_KEY") {
		t.Errorf("api_key ref: got %q", p.APIKey)
	}
	assertRoot(t, agents, "openai", "gpt-4o")
	if got := getSecret(t, dir, "OPENAI_API_KEY"); got != key {
		t.Errorf("stored secret: got %q, want %q", got, key)
	}
}

func TestWizardConfig_OpenAICompatible(t *testing.T) {
	const key = "ollama-key"
	c := providerChoice{
		name: "openai-compatible", typ: "openai",
		url:    "http://localhost:11434/v1",
		apiKey: key, secretName: "OPENAI_COMPATIBLE_API_KEY",
		model: "llama3.1",
	}
	cfg, agents, dir := writeAndLoad(t, c)

	p := assertSingleProvider(t, cfg)
	if p.Name != "openai-compatible" || p.Type != "openai" {
		t.Errorf("provider id/type: got %q/%q", p.Name, p.Type)
	}
	if p.URL != "http://localhost:11434/v1" {
		t.Errorf("compatible url: got %q", p.URL)
	}
	if p.APIKey != secretRef("OPENAI_COMPATIBLE_API_KEY") {
		t.Errorf("api_key ref: got %q", p.APIKey)
	}
	assertRoot(t, agents, "openai-compatible", "llama3.1")
	if got := getSecret(t, dir, "OPENAI_COMPATIBLE_API_KEY"); got != key {
		t.Errorf("stored secret: got %q, want %q", got, key)
	}
}

func TestWizardConfig_AnthropicOAuth(t *testing.T) {
	c := providerChoice{
		name: "anthropic", typ: "anthropic",
		oauth: true,
		model: "claude-sonnet-4-5-20250929",
	}
	cfg, agents, dir := writeAndLoad(t, c)

	p := assertSingleProvider(t, cfg)
	if p.Name != "anthropic" || p.Type != "anthropic" {
		t.Errorf("provider id/type: got %q/%q", p.Name, p.Type)
	}
	if p.Auth != "oauth" {
		t.Errorf("auth: got %q, want oauth", p.Auth)
	}
	if p.APIKey != "" {
		t.Errorf("OAuth provider must not reference an API key, got %q", p.APIKey)
	}
	assertRoot(t, agents, "anthropic", "claude-sonnet-4-5-20250929")
	// No key asked: the secrets store must have no entries.
	store, err := secrets.NewStore(dir, "")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	names, err := store.List()
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("OAuth path must leave secrets empty, got %v", names)
	}
}

func TestWizardConfig_AnthropicAPIKey(t *testing.T) {
	const key = "sk-ant-xyz"
	c := providerChoice{
		name: "anthropic", typ: "anthropic",
		apiKey: key, secretName: "ANTHROPIC_API_KEY",
		model: "claude-sonnet-4-5-20250929",
	}
	cfg, agents, dir := writeAndLoad(t, c)

	p := assertSingleProvider(t, cfg)
	if p.Auth == "oauth" {
		t.Errorf("api-key path must not set auth: oauth")
	}
	if p.APIKey != secretRef("ANTHROPIC_API_KEY") {
		t.Errorf("api_key ref: got %q", p.APIKey)
	}
	assertRoot(t, agents, "anthropic", "claude-sonnet-4-5-20250929")
	if got := getSecret(t, dir, "ANTHROPIC_API_KEY"); got != key {
		t.Errorf("stored secret: got %q, want %q", got, key)
	}
}

// TestWizardConfig_SecretValueRoundTrips ensures a key with awkward
// characters survives the seal/open round-trip through the store.
func TestWizardConfig_SecretValueRoundTrips(t *testing.T) {
	const key = `we"ird\key with spaces`
	c := providerChoice{
		name: "gemini", typ: "gemini",
		apiKey: key, secretName: "GEMINI_API_KEY",
		model: "gemini-2.5-flash",
	}
	_, _, dir := writeAndLoad(t, c)
	if got := getSecret(t, dir, "GEMINI_API_KEY"); got != key {
		t.Errorf("stored secret: got %q, want %q", got, key)
	}
}
