// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package spawn

import (
	"context"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/workers"
)

// fakeUniverse is a Universe stub backed by static slices. Tests
// inject one to drive the validator without touching the App.
type fakeUniverse struct {
	skills, mcps, providers, secrets []string

	// mcpTools maps MCP name → tool names. Tests that care about the
	// description-rendering path populate it; the rest can leave it
	// nil and MCPTools returns nil (rendered as "no tools advertised").
	mcpTools map[string][]string

	// externalMCPs is the set of MCP names treated as external
	// (http/stdio) by MCPExternal. Empty means every MCP is builtin.
	externalMCPs map[string]bool

	// skillDetails / secretDetails enrich the description with the
	// metadata the LLM consumes when choosing pieces for a spawn.
	// Tests that exercise the rendering path populate these; the
	// rest can leave them nil (renders as "(none)").
	skillDetails  []NamedDescription
	secretDetails []NamedDescription
}

func (f *fakeUniverse) ListSkills() []string      { return f.skills }
func (f *fakeUniverse) ListMCPs() []string        { return f.mcps }
func (f *fakeUniverse) ListProviders() []string   { return f.providers }
func (f *fakeUniverse) ListSecretNames() []string { return f.secrets }
func (f *fakeUniverse) MCPTools(name string) []string {
	if f.mcpTools == nil {
		return nil
	}
	return f.mcpTools[name]
}
func (f *fakeUniverse) MCPExternal(name string) bool {
	return f.externalMCPs[name]
}
func (f *fakeUniverse) SpawnSkillDetails() []NamedDescription { return f.skillDetails }
func (f *fakeUniverse) SecretDetails() []NamedDescription     { return f.secretDetails }

func newDynamicTools(t *testing.T) *Tools {
	t.Helper()
	mgr := workers.NewManager(workers.ManagerConfig{
		Sandbox: &workers.SandboxAllocator{DataDir: t.TempDir()},
		DriverFactory: func(_ string, _ workers.Spec, _ string) (workers.Driver, error) {
			return &noopDriver{}, nil
		},
	})
	return &Tools{
		Manager: mgr,
		Universe: &fakeUniverse{
			skills:    []string{"web-research", "coding"},
			mcps:      []string{"filesystem", "browse"},
			providers: []string{"openai-main", "anthropic-main"},
			secrets:   []string{"github_token", "openai_api_key"},
		},
		RootDefaults:  RootDefaults{Provider: "openai-main", Model: "gpt-5"},
		EnableDynamic: true,
	}
}

func TestDynamicToolsRegisteredWhenEnabled(t *testing.T) {
	tools, err := newDynamicTools(t).ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	if len(tools) != 9 {
		t.Errorf("got %d tools, want 9 (7 static + 2 dynamic)", len(tools))
	}
	hasDynamic := false
	hasBatch := false
	for _, tl := range tools {
		if tl.Name() == "spawn_dynamic_agent" {
			hasDynamic = true
		}
		if tl.Name() == "spawn_dynamic_agents" {
			hasBatch = true
		}
	}
	if !hasDynamic || !hasBatch {
		t.Error("dynamic tools missing from ADKTools()")
	}
}

func TestDynamicToolsAbsentWhenDisabled(t *testing.T) {
	tools := newDynamicTools(t)
	tools.EnableDynamic = false
	list, err := tools.ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	if len(list) != 7 {
		t.Errorf("got %d tools with EnableDynamic=false, want 7", len(list))
	}
}

func TestBuildDynamicSpecValidatesSkillSubset(t *testing.T) {
	tools := newDynamicTools(t)
	_, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:   "x",
		Prompt: "do stuff",
		Skills: []string{"web-research", "unknown-skill"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown skill") {
		t.Errorf("expected unknown-skill error, got %v", err)
	}
}

func TestBuildDynamicSpecValidatesMCPSubset(t *testing.T) {
	tools := newDynamicTools(t)
	_, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:   "x",
		Prompt: "do stuff",
		MCPs:   []string{"browse", "unknown-mcp"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown mcp") {
		t.Errorf("expected unknown-mcp error, got %v", err)
	}
}

func TestBuildDynamicSpecValidatesSecretSubset(t *testing.T) {
	tools := newDynamicTools(t)
	_, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:           "x",
		Prompt:         "do stuff",
		AllowedSecrets: []string{"unknown-secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown secret") {
		t.Errorf("expected unknown-secret error, got %v", err)
	}
}

// TestBuildDynamicSpecRejectsSecretsBeyondParent confirms the
// least-privilege rule for nested spawn: a sub-agent built by a
// parent with an explicit allowlist cannot ask for secrets the
// parent itself cannot dereference. Today ParentAllowedSecrets is
// only nil (root is sovereign) in production, but the rule kicks
// in the moment a sub-agent gets its own spawn tools — the test
// pins the behaviour so a future regression is caught.
func TestBuildDynamicSpecRejectsSecretsBeyondParent(t *testing.T) {
	tools := newDynamicTools(t)
	tools.ParentAllowedSecrets = []string{"github_token"} // strict parent
	_, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:           "x",
		Prompt:         "do stuff",
		AllowedSecrets: []string{"github_token", "openai_api_key"},
	})
	if err == nil {
		t.Fatal("expected error when child asks for secret outside parent allowlist")
	}
	if !strings.Contains(err.Error(), "spawning agent cannot grant") {
		t.Errorf("unexpected error text: %v", err)
	}
}

// TestBuildDynamicSpecAcceptsSubsetOfParent is the happy path of the
// previous test: when the child asks only for a subset of what the
// parent allows, the spec is built normally.
func TestBuildDynamicSpecAcceptsSubsetOfParent(t *testing.T) {
	tools := newDynamicTools(t)
	tools.ParentAllowedSecrets = []string{"github_token", "openai_api_key"}
	spec, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:           "x",
		Prompt:         "do stuff",
		AllowedSecrets: []string{"github_token"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.AllowedSecrets) != 1 || spec.AllowedSecrets[0] != "github_token" {
		t.Errorf("got %v, want [github_token]", spec.AllowedSecrets)
	}
}

// TestBuildDynamicSpecNilParentAcceptsAnyKnownSecret confirms that
// a sovereign parent (ParentAllowedSecrets == nil, today: the root)
// can hand any secret of the global universe to a child.
func TestBuildDynamicSpecNilParentAcceptsAnyKnownSecret(t *testing.T) {
	tools := newDynamicTools(t)
	// tools.ParentAllowedSecrets stays nil by default — sovereign.
	spec, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:           "x",
		Prompt:         "do stuff",
		AllowedSecrets: []string{"openai_api_key"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.AllowedSecrets) != 1 || spec.AllowedSecrets[0] != "openai_api_key" {
		t.Errorf("got %v, want [openai_api_key]", spec.AllowedSecrets)
	}
}

func TestBuildDynamicSpecRejectsUnknownProvider(t *testing.T) {
	tools := newDynamicTools(t)
	_, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:   "x",
		Prompt: "do stuff",
		LLM:    DynamicLLM{Provider: "ghost", Model: "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("expected unknown-provider error, got %v", err)
	}
}

func TestBuildDynamicSpecInheritsRootProviderWhenUnset(t *testing.T) {
	tools := newDynamicTools(t)
	spec, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:   "x",
		Prompt: "do stuff",
	})
	if err != nil {
		t.Fatalf("buildDynamicSpec: %v", err)
	}
	if spec.Provider != "openai-main" || spec.Model != "gpt-5" {
		t.Errorf("expected RootDefaults to fill provider/model, got %+v", spec)
	}
}

// TestBuildDynamicSpecPropagatesDescription is the regression for
// the "root can compose anything" requirement: the LLM-supplied
// Description must reach workers.Spec (and downstream the agent
// builder), not get silently dropped on the spawn-tool boundary.
func TestBuildDynamicSpecPropagatesDescription(t *testing.T) {
	tools := newDynamicTools(t)
	spec, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:        "x",
		Prompt:      "do stuff",
		Description: "Produces a typed answer object.",
	})
	if err != nil {
		t.Fatalf("buildDynamicSpec: %v", err)
	}
	if spec.Description != "Produces a typed answer object." {
		t.Errorf("description not propagated: %q", spec.Description)
	}
}

// TestBuildDynamicSpecPropagatesReasoning checks a valid reasoning
// value is normalised and reaches workers.Spec so the builder can
// apply it per worker.
func TestBuildDynamicSpecPropagatesReasoning(t *testing.T) {
	tools := newDynamicTools(t)
	spec, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:   "x",
		Prompt: "do stuff",
		LLM:    DynamicLLM{Reasoning: "HIGH"},
	})
	if err != nil {
		t.Fatalf("buildDynamicSpec: %v", err)
	}
	if spec.Reasoning != "high" {
		t.Errorf("reasoning not normalised/propagated: %q", spec.Reasoning)
	}
}

// TestBuildDynamicSpecRejectsInvalidReasoning confirms a typo'd
// effort is rejected with a helpful error rather than silently
// ignored.
func TestBuildDynamicSpecRejectsInvalidReasoning(t *testing.T) {
	tools := newDynamicTools(t)
	_, err := tools.buildDynamicSpec(DynamicSpawnArgs{
		Name:   "x",
		Prompt: "do stuff",
		LLM:    DynamicLLM{Reasoning: "ultra"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid reasoning") {
		t.Errorf("expected invalid-reasoning error, got %v", err)
	}
}

func TestBuildDynamicSpecRejectsMissingNameAndPrompt(t *testing.T) {
	tools := newDynamicTools(t)
	if _, err := tools.buildDynamicSpec(DynamicSpawnArgs{Prompt: "x"}); err == nil {
		t.Error("expected error for missing name")
	}
	if _, err := tools.buildDynamicSpec(DynamicSpawnArgs{Name: "x"}); err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestDescriptionEmbedsUniverse(t *testing.T) {
	u := &fakeUniverse{
		skillDetails: []NamedDescription{{Name: "foo", Description: "do foo"}},
		providers:    []string{"bar"},
	}
	desc := composeDynamicDescription(u)
	if !strings.Contains(desc, "foo") {
		t.Error("description should embed available skill names")
	}
	if !strings.Contains(desc, "do foo") {
		t.Error("description should embed skill descriptions")
	}
	if !strings.Contains(desc, "bar") {
		t.Error("description should embed available providers")
	}
	if !strings.Contains(desc, "(none)") {
		t.Error("empty universes should render as (none)")
	}
}

// TestDescriptionEmbedsSecretDescriptions: the dynamic-spawn
// description must include each secret's free-text description from
// the store so the LLM can ask for the right one without trial and
// error. Names alone don't tell it what GEMINI_API_KEY vs OPENAI_KEY
// actually authenticate against.
func TestDescriptionEmbedsSecretDescriptions(t *testing.T) {
	u := &fakeUniverse{
		secretDetails: []NamedDescription{
			{Name: "GEMINI_API_KEY", Description: "Google Gemini API"},
			{Name: "GITHUB_TOKEN", Description: "Read-only PAT for github.com"},
		},
	}
	desc := composeDynamicDescription(u)
	for _, want := range []string{"GEMINI_API_KEY", "Google Gemini API", "GITHUB_TOKEN", "Read-only PAT"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q\n---\n%s", want, desc)
		}
	}
}

// TestDescriptionEmbedsMCPTools is the regression for the
// "no, workers can't access the terminal" hallucination: the
// dynamic-spawn description must list every tool exposed by each
// MCP so the LLM can see, for example, that `filesystem` ships
// with `exec` and is not read/write-only.
func TestDescriptionEmbedsMCPTools(t *testing.T) {
	u := &fakeUniverse{
		mcps: []string{"filesystem", "browse"},
		mcpTools: map[string][]string{
			"filesystem": {"read_file", "write_file", "exec"},
			"browse":     {"web_fetch"},
		},
	}
	desc := composeDynamicDescription(u)
	for _, want := range []string{"filesystem", "browse", "read_file", "write_file", "exec", "web_fetch"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q\n---\n%s", want, desc)
		}
	}
}

// TestDescriptionExternalMCPMessage confirms the wording difference
// between a builtin MCP that genuinely has no tools and an external
// (http/stdio) MCP whose tools resolve lazily at call time. The
// external one must NOT read "(no tools advertised)" — that phrasing
// wrongly implies the server exposes nothing, when really baifo just
// hasn't connected yet.
func TestDescriptionExternalMCPMessage(t *testing.T) {
	u := &fakeUniverse{
		mcps:         []string{"empty-builtin", "magnific"},
		externalMCPs: map[string]bool{"magnific": true},
		// no mcpTools for either, so both render the empty branch
	}
	desc := composeDynamicDescription(u)

	if !strings.Contains(desc, "empty-builtin: (no tools advertised)") {
		t.Errorf("builtin with no tools should say 'no tools advertised'\n---\n%s", desc)
	}
	if strings.Contains(desc, "magnific: (no tools advertised)") {
		t.Errorf("external MCP must NOT say 'no tools advertised'\n---\n%s", desc)
	}
	if !strings.Contains(desc, "magnific: (external MCP") {
		t.Errorf("external MCP should explain tools load at call time\n---\n%s", desc)
	}
	if !strings.Contains(desc, "/mcps test magnific") {
		t.Errorf("external MCP message should point to /mcps test\n---\n%s", desc)
	}
}

// silence unused-import for context when build is in flight
var _ = context.Background
