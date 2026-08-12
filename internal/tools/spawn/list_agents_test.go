// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package spawn

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"

	"github.com/achetronic/baifo/internal/config"
)

// findTool returns the first registered tool with the given name, or
// nil when absent. Used by the tests below to assert that renames
// don't accidentally drop a tool from the toolset.
func findTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name() == name {
			return tl
		}
	}
	return nil
}

// TestSpawnStaticDescriptionListsTemplates is the regression that made
// the LLM stop guessing template names. The Description string is the
// only place the model reliably reads before deciding whether to call
// spawn_static_agent, so the available names MUST appear there.
func TestSpawnStaticDescriptionListsTemplates(t *testing.T) {
	r := &fakeResolver{templates: map[string]config.AgentTemplate{
		"researcher":    {Name: "researcher"},
		"code-reviewer": {Name: "code-reviewer"},
	}}
	desc := composeStaticDescription(r)
	for _, want := range []string{"researcher", "code-reviewer"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing template name %q\nfull description: %s", want, desc)
		}
	}
}

// TestSpawnStaticDescriptionEmptyResolver documents the graceful path
// when no templates are declared. The description must still hint at
// the underlying mechanism so the LLM does not invent names.
func TestSpawnStaticDescriptionEmptyResolver(t *testing.T) {
	r := &fakeResolver{templates: map[string]config.AgentTemplate{}}
	desc := composeStaticDescription(r)
	if !strings.Contains(desc, "No templates") {
		t.Errorf("description should mention the empty state, got: %s", desc)
	}
}

// TestListAgentsToolReturnsTemplates exercises the new list_agents
// tool end-to-end through the public ADKTools constructor so naming
// regressions surface immediately.
func TestListAgentsToolReturnsTemplates(t *testing.T) {
	tools, err := newSpawnTools(t).ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	var listAgents = findTool(t, tools, "list_agents")
	if listAgents == nil {
		t.Fatal("list_agents tool not registered")
	}
	// Description should advertise that this lists templates, not
	// live workers (the rename made that distinction load-bearing).
	desc := listAgents.Description()
	if !strings.Contains(desc, "template") {
		t.Errorf("list_agents description should mention 'template', got: %s", desc)
	}
}

// TestListRunningAgentsToolReturnsWorkers checks the renamed tool
// is reachable under its new name and still talks about workers.
func TestListRunningAgentsToolReturnsWorkers(t *testing.T) {
	tools, err := newSpawnTools(t).ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	listRunning := findTool(t, tools, "list_running_agents")
	if listRunning == nil {
		t.Fatal("list_running_agents tool not registered")
	}
	desc := listRunning.Description()
	if !strings.Contains(desc, "LIVE worker") {
		t.Errorf("list_running_agents description should mention live workers, got: %s", desc)
	}
}
