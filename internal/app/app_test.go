// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/achetronic/baifo/internal/config"
	_ "github.com/achetronic/baifo/internal/providers/allproviders"
)

// minimalYAML is the smallest baifo.yaml that boots an App: just the
// theme. The root agent lives in agents.yaml under the unified
// model, written separately by writeAgents below. With no providers
// configured the App still boots in a degraded state — that's fine
// for testing ReloadFromDisk semantics.
const minimalYAML = `
theme:
  nerd_fonts: false
`

// minimalAgentsYAML defines a single root agent so RootName() has
// something to return. provider/model are placeholders: buildRoot
// will fail to resolve them, but rootTemplate() still surfaces the
// declared name, which is what these tests measure.
const minimalAgentsYAML = `
version: 1
agents:
  - name: test-root
    root: true
    prompt: |
      placeholder prompt
    llm:
      provider: fake
      model: fake-model
`

// reloadedAgentsYAML mirrors minimalAgentsYAML with a different
// root name so the reload assertion has something concrete to
// observe.
const reloadedAgentsYAML = `
version: 1
agents:
  - name: reloaded-root
    root: true
    prompt: |
      placeholder prompt
    llm:
      provider: fake
      model: fake-model
`

// writeConfig drops a fresh baifo.yaml into dir.
func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	p := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// writeAgents drops a fresh agents.yaml into dir.
func writeAgents(t *testing.T, dir, body string) {
	t.Helper()
	p := filepath.Join(dir, config.AgentsFileName)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write agents: %v", err)
	}
}

func TestApp_ReloadFromDisk_SwapsConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, minimalAgentsYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if got := a.RootName(); got != "test-root" {
		t.Fatalf("initial RootName: got %q, want test-root", got)
	}

	// Rewrite agents.yaml with a new root name. We do NOT rely on
	// the fsnotify watcher here — directly call ReloadFromDisk
	// because the watcher's debounce + filesystem timing make the
	// test flaky on slow CI disks. The fsnotify path is exercised
	// separately in TestApp_Watcher_TriggersReload.
	writeAgents(t, dir, reloadedAgentsYAML)
	if err := a.ReloadFromDisk(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := a.RootName(); got != "reloaded-root" {
		t.Fatalf("post-reload RootName: got %q, want reloaded-root", got)
	}
}

func TestApp_ReloadFromDisk_EmitsSubscribeReload(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ch := a.SubscribeReload()
	if ch == nil {
		t.Fatalf("SubscribeReload returned nil channel")
	}

	if err := a.ReloadFromDisk(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	select {
	case <-ch:
		// good
	case <-time.After(2 * time.Second):
		t.Fatalf("facade.ReloadEvent not delivered within 2s")
	}
}

func TestApp_UpsertMCPFromDisk_AddsNewEntry(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	yamlText := "name: filesystem\ntype: builtin\nbuiltin: filesystem\n"
	if err := a.UpsertMCPFromDisk(context.Background(), yamlText); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	names := a.ListMCPs()
	if len(names) != 1 || names[0] != "filesystem" {
		t.Errorf("after upsert: got %v, want [filesystem]", names)
	}
}

func TestApp_UpsertMCPFromDisk_RejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// http MCP without endpoint must be rejected.
	yamlText := "name: bad\ntype: http\n"
	if err := a.UpsertMCPFromDisk(context.Background(), yamlText); err == nil {
		t.Errorf("expected validation error for http without endpoint")
	}
	if names := a.ListMCPs(); len(names) != 0 {
		t.Errorf("failed upsert should not have changed state, got %v", names)
	}
}

func TestApp_UpsertMCP_PreservesComments(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Upsert an MCP whose YAML contains comments. The exact comments
	// must survive both the write to baifo.yaml AND a subsequent read
	// via MCPYAML, which is the path /mcp edit takes.
	yamlText := "# my custom github MCP\nname: github\ntype: http\nendpoint: https://api.example.com   # bear bones\nauth:\n  kind: none   # no oauth yet\n"
	if err := a.UpsertMCPFromDisk(context.Background(), yamlText); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := a.MCPYAML("github")
	if err != nil {
		t.Fatalf("MCPYAML: %v", err)
	}
	for _, want := range []string{
		"# my custom github MCP",
		"# bear bones",
		"# no oauth yet",
		"name: github",
	} {
		if !contains(got, want) {
			t.Errorf("missing %q in re-read yaml:\n%s", want, got)
		}
	}
}

func TestApp_MCPYAML_RoundTripsBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Seed an entry, then round-trip it through MCPYAML.
	if err := a.UpsertMCPFromDisk(context.Background(),
		"name: filesystem\ntype: builtin\nbuiltin: filesystem\n"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	y, err := a.MCPYAML("filesystem")
	if err != nil {
		t.Fatalf("MCPYAML: %v", err)
	}
	if !contains(y, "name: filesystem") || !contains(y, "type: builtin") || !contains(y, "builtin: filesystem") {
		t.Errorf("unexpected yaml: %q", y)
	}
	if _, err := a.MCPYAML("missing"); err == nil {
		t.Errorf("unknown name should error")
	}
}

// contains is a tiny strings.Contains shim so the test file does not
// need the import. Keeps the diff focused.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestApp_UpsertSkill_PreservesContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	content := "---\nname: my-skill\ndescription: A test skill used by the unit suite to verify the upsert + readback round-trip works end to end.\n---\n\n# my-skill\n\nBody goes here.\n"
	if err := a.UpsertSkill(context.Background(), content); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := a.SkillContent("my-skill")
	if err != nil {
		t.Fatalf("SkillContent: %v", err)
	}
	if got != content {
		t.Errorf("content mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, content)
	}
}

func TestApp_UpsertSkill_RejectsInvalidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Uppercase name violates the ADK rule "lowercase a-z and digits only".
	content := "---\nname: BadSkill\ndescription: short\n---\n\nbody\n"
	if err := a.UpsertSkill(context.Background(), content); err == nil {
		t.Errorf("expected validation error for invalid name")
	}
}

func TestApp_UpsertAgent_PreservesContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	agentYAML := "name: rasff-monitor\ndescription: watches RASFF\nprompt: |\n  You are an agent.\nllm:\n  provider: anthropic\n  model: claude-sonnet-4\n"
	if err := a.UpsertAgent(context.Background(), agentYAML); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	names := a.ListAgentTemplates()
	found := false
	for _, n := range names {
		if n == "rasff-monitor" {
			found = true
		}
	}
	if !found {
		t.Errorf("agent not in list after upsert: %v", names)
	}

	got, err := a.AgentYAML("rasff-monitor")
	if err != nil {
		t.Fatalf("AgentYAML: %v", err)
	}
	if !contains(got, "name: rasff-monitor") {
		t.Errorf("name missing in re-read yaml:\n%s", got)
	}
	if !contains(got, "You are an agent") {
		t.Errorf("prompt body missing in re-read yaml:\n%s", got)
	}
}

func TestApp_UpsertAgent_RejectsMissingLLM(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// LLM missing entirely -> validateAgents must reject.
	agentYAML := "name: broken\nprompt: |\n  body\n"
	if err := a.UpsertAgent(context.Background(), agentYAML); err == nil {
		t.Errorf("expected validation error for missing llm")
	}
}

func TestApp_UpsertProvider_PreservesContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	provider := "name: anthropic-main\ntype: anthropic\napi_key: sk-test-123\n"
	if err := a.UpsertProvider(context.Background(), provider); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	names := a.ListProviders()
	found := false
	for _, n := range names {
		if n == "anthropic-main" {
			found = true
		}
	}
	if !found {
		t.Errorf("provider not listed after upsert: %v", names)
	}
	got, err := a.ProviderYAML("anthropic-main")
	if err != nil {
		t.Fatalf("ProviderYAML: %v", err)
	}
	if !contains(got, "type: anthropic") {
		t.Errorf("type missing in re-read yaml:\n%s", got)
	}
}

func TestApp_UpsertProvider_RejectsUnknownType(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	provider := "name: bogus\ntype: definitely-not-a-real-provider-type\n"
	if err := a.UpsertProvider(context.Background(), provider); err == nil {
		t.Errorf("expected validation error for unknown type")
	}
}

func TestApp_DeleteProvider_Removes(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if err := a.UpsertProvider(context.Background(),
		"name: temp\ntype: anthropic\napi_key: x\n"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := a.DeleteProvider(context.Background(), "temp"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, n := range a.ListProviders() {
		if n == "temp" {
			t.Errorf("provider still listed after delete")
		}
	}
}

func TestApp_AddFact_Persists(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	id, err := a.AddFact(context.Background(), "Alby prefers concise replies", "preference")
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if id == 0 {
		t.Errorf("expected non-zero id")
	}
	details := a.FactDetails()
	found := false
	for _, f := range details {
		if f.ID == id && contains(f.Content, "concise") {
			found = true
		}
	}
	if !found {
		t.Errorf("fact not visible in FactDetails: %v", details)
	}
}

func TestApp_UpdateFact_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	id, err := a.AddFact(context.Background(), "Alby prefers verbose replies", "preference")
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}

	// FactContent must return what we just stored.
	got, _, err := a.FactContent(id)
	if err != nil {
		t.Fatalf("FactContent: %v", err)
	}
	if !contains(got, "verbose") {
		t.Fatalf("FactContent returned %q, expected to contain 'verbose'", got)
	}

	// Update and re-read.
	if err := a.UpdateFact(context.Background(), id, "Alby prefers concise replies"); err != nil {
		t.Fatalf("UpdateFact: %v", err)
	}
	got, _, err = a.FactContent(id)
	if err != nil {
		t.Fatalf("FactContent after update: %v", err)
	}
	if !contains(got, "concise") || contains(got, "verbose") {
		t.Errorf("FactContent after update = %q, expected 'concise' replacing 'verbose'", got)
	}

	// Same ID, new content visible in FactDetails too.
	found := false
	for _, f := range a.FactDetails() {
		if f.ID == id && contains(f.Content, "concise") {
			found = true
		}
	}
	if !found {
		t.Errorf("updated fact not visible in FactDetails")
	}
}

func TestApp_UpdateFact_UnknownIDErrors(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if err := a.UpdateFact(context.Background(), 999999, "x"); err == nil {
		t.Errorf("updating unknown ID should error")
	}
}

func TestApp_DeleteFact_Removes(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	id, err := a.AddFact(context.Background(), "throwaway", "")
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if err := a.DeleteFact(context.Background(), id); err != nil {
		t.Fatalf("DeleteFact: %v", err)
	}
	for _, f := range a.FactDetails() {
		if f.ID == id {
			t.Errorf("fact %d still present after delete", id)
		}
	}
}

func TestApp_UpdateFact_ChangesContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	id, err := a.AddFact(context.Background(), "original content", "")
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}

	// Sanity: FactContent returns the original value.
	got, _, err := a.FactContent(id)
	if err != nil {
		t.Fatalf("FactContent before update: %v", err)
	}
	if got != "original content" {
		t.Errorf("FactContent before update = %q, want %q", got, "original content")
	}

	// Update and verify the new value is persisted.
	if err := a.UpdateFact(context.Background(), id, "edited content"); err != nil {
		t.Fatalf("UpdateFact: %v", err)
	}
	got, _, err = a.FactContent(id)
	if err != nil {
		t.Fatalf("FactContent after update: %v", err)
	}
	if got != "edited content" {
		t.Errorf("FactContent after update = %q, want %q", got, "edited content")
	}

	// The fact should still be in the list with the new content.
	var found bool
	for _, f := range a.FactDetails() {
		if f.ID == id {
			found = true
			if f.Content != "edited content" {
				t.Errorf("FactDetails content = %q, want %q", f.Content, "edited content")
			}
		}
	}
	if !found {
		t.Errorf("fact %d not present after update", id)
	}
}

func TestApp_AddFact_RejectsEmptyContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.AddFact(context.Background(), "", ""); err == nil {
		t.Errorf("empty content should be rejected")
	}
}

func TestApp_DeleteAgent_Removes(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	agentYAML := "name: temp\nprompt: |\n  body\nllm:\n  provider: anthropic\n  model: claude\n"
	if err := a.UpsertAgent(context.Background(), agentYAML); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := a.DeleteAgent(context.Background(), "temp"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, n := range a.ListAgentTemplates() {
		if n == "temp" {
			t.Errorf("agent still listed after delete")
		}
	}
}

func TestApp_DeleteSkill_Removes(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	content := "---\nname: temp-skill\ndescription: A short throwaway used by the delete test, never expected to outlive the function it lives in.\n---\n\nbody\n"
	if err := a.UpsertSkill(context.Background(), content); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := a.DeleteSkill(context.Background(), "temp-skill"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := a.SkillContent("temp-skill"); err == nil {
		t.Errorf("SkillContent should fail after delete")
	}
}

func TestApp_Watcher_TriggersReload(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, minimalAgentsYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ch := a.SubscribeReload()
	if ch == nil {
		t.Skip("watcher disabled in this environment")
	}

	// Touch agents.yaml: rewrite it with the new root name and
	// let fsnotify do its job. The watcher debounces by 250ms, so
	// we give the channel a generous 3s window.
	writeAgents(t, dir, reloadedAgentsYAML)

	select {
	case <-ch:
		if got := a.RootName(); got != "reloaded-root" {
			t.Fatalf("watcher reload: RootName got %q, want reloaded-root", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("watcher did not deliver facade.ReloadEvent within 3s")
	}
}
