// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package spawn

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/workers"
)

// fakeResolver is a TemplateResolver backed by a static map.
type fakeResolver struct {
	templates map[string]config.AgentTemplate
}

func (r *fakeResolver) Resolve(name string) (config.AgentTemplate, bool) {
	t, ok := r.templates[name]
	return t, ok
}

func (r *fakeResolver) ListTemplates() []config.AgentTemplate {
	names := make([]string, 0, len(r.templates))
	for n := range r.templates {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]config.AgentTemplate, 0, len(names))
	for _, n := range names {
		out = append(out, r.templates[n])
	}
	return out
}

func newSpawnTools(t *testing.T) *Tools {
	t.Helper()
	mgr := workers.NewManager(workers.ManagerConfig{
		Sandbox: &workers.SandboxAllocator{DataDir: t.TempDir()},
		DriverFactory: func(_ string, _ workers.Spec, _ string) (workers.Driver, error) {
			return &noopDriver{}, nil
		},
	})
	resolver := &fakeResolver{templates: map[string]config.AgentTemplate{
		"researcher": {
			Name:   "researcher",
			Prompt: "you are a researcher",
			LLM:    config.AgentLLM{Provider: "p", Model: "m"},
		},
	}}
	return &Tools{Manager: mgr, Templates: resolver}
}

// noopDriver lets the spawn tools register and Spawn without doing any
// real work. The Manager's status transitions still happen.
type noopDriver struct{}

func (*noopDriver) Send(_ context.Context, _ string, _ *workers.EventBus, _ string) error {
	return nil
}
func (*noopDriver) WaitIdle(context.Context) error { return nil }
func (*noopDriver) Output() string                 { return "" }
func (*noopDriver) Close() error                   { return nil }

func TestADKToolsReturnsAllStaticTools(t *testing.T) {
	tools, err := newSpawnTools(t).ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	if len(tools) != 7 {
		t.Errorf("got %d tools, want 7", len(tools))
	}
	want := map[string]bool{
		"spawn_static_agent":  true,
		"query_agent":         true,
		"list_agents":         true, // templates available
		"list_running_agents": true, // live workers
		"inspect_agent":       true,
		"collect_agent":       true,
		"kill_agent":          true,
	}
	for _, tl := range tools {
		if !want[tl.Name()] {
			t.Errorf("unexpected tool name %q", tl.Name())
		}
		delete(want, tl.Name())
	}
	if len(want) != 0 {
		t.Errorf("missing tools: %v", want)
	}
}

func TestADKToolsRejectsNilManager(t *testing.T) {
	_, err := (&Tools{}).ADKTools()
	if err == nil {
		t.Error("expected error when Manager is nil")
	}
}

func TestSummaryFromInfoFormatsKind(t *testing.T) {
	info := workers.WorkerInfo{
		ID:   "w_1",
		Name: "x",
		Kind: workers.KindStatic,
	}
	s := summaryFromInfo(info)
	if s.Kind != "static" {
		t.Errorf("Kind: got %q, want static", s.Kind)
	}
}

// ── resolveStaticAllowedSecrets ────────────────────────────────

// TestResolveStaticAllowedSecrets_NilOverrideInheritsTemplate is the
// happy path: the LLM does not touch allowed_secrets, the worker
// inherits the operator-declared template list verbatim.
func TestResolveStaticAllowedSecrets_NilOverrideInheritsTemplate(t *testing.T) {
	tmpl := []string{"GH_TOKEN"}
	got, err := resolveStaticAllowedSecrets(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slicesEqual(got, tmpl) {
		t.Errorf("got %v, want %v", got, tmpl)
	}
}

// TestResolveStaticAllowedSecrets_EmptyOverrideMeansNone confirms
// that an explicit empty list from the LLM means "this run gets no
// secrets", not "inherit template" — i.e. nil and [] are different.
func TestResolveStaticAllowedSecrets_EmptyOverrideMeansNone(t *testing.T) {
	tmpl := []string{"GH_TOKEN", "API_KEY"}
	got, err := resolveStaticAllowedSecrets(tmpl, []string{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want explicit empty slice", got)
	}
}

// TestResolveStaticAllowedSecrets_OverrideNarrowsTemplate confirms
// the LLM can drop secrets the template declared.
func TestResolveStaticAllowedSecrets_OverrideNarrowsTemplate(t *testing.T) {
	tmpl := []string{"GH_TOKEN", "API_KEY"}
	got, err := resolveStaticAllowedSecrets(tmpl, []string{"GH_TOKEN"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slicesEqual(got, []string{"GH_TOKEN"}) {
		t.Errorf("got %v, want [GH_TOKEN]", got)
	}
}

// TestResolveStaticAllowedSecrets_OverrideCannotExceedTemplate
// confirms the LLM cannot ask for secrets the template did not
// declare. This is the core least-privilege rule.
func TestResolveStaticAllowedSecrets_OverrideCannotExceedTemplate(t *testing.T) {
	tmpl := []string{"GH_TOKEN"}
	_, err := resolveStaticAllowedSecrets(tmpl, []string{"GH_TOKEN", "EXTRA"}, nil)
	if err == nil {
		t.Fatal("expected error when override exceeds template")
	}
	if !strings.Contains(err.Error(), "override exceeds template") {
		t.Errorf("unexpected error text: %v", err)
	}
}

// TestResolveStaticAllowedSecrets_TemplateMustBeSubsetOfParent
// confirms the parent-allowlist rule: a template cannot grant a
// secret the spawning agent itself cannot dereference. Today the
// parent is always the root (sovereign), but the check is in place
// for the day a sub-agent gets its own spawn tools.
func TestResolveStaticAllowedSecrets_TemplateMustBeSubsetOfParent(t *testing.T) {
	parent := []string{"GH_TOKEN"}
	tmpl := []string{"GH_TOKEN", "API_KEY"}
	_, err := resolveStaticAllowedSecrets(tmpl, nil, parent)
	if err == nil {
		t.Fatal("expected error when template exceeds parent")
	}
}

// TestResolveStaticAllowedSecrets_NilParentMeansSovereign confirms
// that nil ParentAllowedSecrets bypasses the parent check. This is
// the root's behaviour today and the test pins it.
func TestResolveStaticAllowedSecrets_NilParentMeansSovereign(t *testing.T) {
	tmpl := []string{"WHATEVER", "ANY_NAME"}
	got, err := resolveStaticAllowedSecrets(tmpl, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slicesEqual(got, tmpl) {
		t.Errorf("got %v, want %v", got, tmpl)
	}
}

// slicesEqual compares two []string for equal length + element
// equality at every index. Avoids pulling reflect for one assertion.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
