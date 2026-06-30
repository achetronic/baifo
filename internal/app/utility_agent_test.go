// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// mkIndex builds an agentTemplateIndex from a literal AgentsFile so
// the utilityLLMRef tests don't need a full App boot.
func mkIndex(agents ...config.AgentTemplate) *agentTemplateIndex {
	return newAgentTemplateIndex(&config.AgentsFile{Agents: agents})
}

func rootTmpl(provider, model string) config.AgentTemplate {
	return config.AgentTemplate{
		Root: true, Name: "root", Prompt: "p",
		LLM: config.AgentLLM{Provider: provider, Model: model},
	}
}

func utilityTmpl(provider, model string) config.AgentTemplate {
	return config.AgentTemplate{
		Utility: true, Name: "utility",
		LLM: config.AgentLLM{Provider: provider, Model: model},
	}
}

// TestUtilityLLMRef pins the resolution order for the chores model:
// a complete utility entry wins; an incomplete (or absent) one falls
// back to the root; no usable LLM anywhere reports ok=false.
func TestUtilityLLMRef(t *testing.T) {
	cases := []struct {
		name         string
		idx          *agentTemplateIndex
		wantProvider string
		wantModel    string
		wantOK       bool
	}{
		{
			name:         "utility wins over root",
			idx:          mkIndex(rootTmpl("anthropic", "big"), utilityTmpl("gemini", "flash")),
			wantProvider: "gemini", wantModel: "flash", wantOK: true,
		},
		{
			name:         "incomplete utility falls back to root",
			idx:          mkIndex(rootTmpl("anthropic", "big"), utilityTmpl("", "")),
			wantProvider: "anthropic", wantModel: "big", wantOK: true,
		},
		{
			name:         "no utility falls back to root",
			idx:          mkIndex(rootTmpl("anthropic", "big")),
			wantProvider: "anthropic", wantModel: "big", wantOK: true,
		},
		{
			name:   "nothing usable",
			idx:    mkIndex(rootTmpl("", ""), utilityTmpl("", "")),
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{agentTmpl: tc.idx}
			provider, model, ok := a.utilityLLMRef()
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if provider != tc.wantProvider || model != tc.wantModel {
				t.Fatalf("got (%s, %s), want (%s, %s)",
					provider, model, tc.wantProvider, tc.wantModel)
			}
		})
	}
}

// TestUtilityAgentSpawnable confirms the index includes the utility
// entry in the spawnable template map and the resolver can
// reach it by name, since utility agents are normal agents.
func TestUtilityAgentSpawnable(t *testing.T) {
	idx := mkIndex(rootTmpl("p", "m"), utilityTmpl("p", "m"))
	if _, ok := idx.Resolve("utility"); !ok {
		t.Fatal("utility agent must be resolvable as a spawnable template")
	}
	found := false
	for _, tmpl := range idx.ListTemplates() {
		if tmpl.Utility {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ListTemplates must include the utility agent")
	}
	if idx.UtilityAgent() == nil {
		t.Fatal("UtilityAgent accessor must still find it")
	}
}
