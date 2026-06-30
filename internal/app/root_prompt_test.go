// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"
)

// TestComposeRootPrompt_AllFlagsIncludesEveryBlock confirms the
// composer wires every capability block when its flag is set.
// Acts as a guard: if a future PR adds a new block but forgets
// to plumb it, this test won't fail (we'd need a new flag) — but
// the existing ones will at least stay non-regressed.
func TestComposeRootPrompt_AllFlagsIncludesEveryBlock(t *testing.T) {
	out := composeRootPrompt("you are baifo", rootCapabilitiesFlags{
		Memory:         true,
		Todos:          true,
		StaticSpawn:    true,
		DynamicSpawn:   true,
		Models:         true,
		Skills:         true,
		Secrets:        true,
		ContextCompact: true,
	})
	for _, marker := range []string{
		"## Runtime capabilities",
		"### Long-term memory",
		"### Task tracking",
		"### Workers",
		"spawn_static_agent",
		"spawn_dynamic_agent",
		"### Models",
		"list_models",
		"### Skills",
		"### Secrets",
		"### After a context compaction",
		"todos_list",
		"search_memory",
	} {
		if !strings.Contains(out, marker) {
			t.Errorf("composed prompt is missing %q", marker)
		}
	}
}

// TestComposeRootPrompt_NoFlagsReturnsUserPromptVerbatim is the
// "nothing was injected" case. When every flag is false the
// composer must hand back exactly what was supplied — no
// trailing header, no empty section.
func TestComposeRootPrompt_NoFlagsReturnsUserPromptVerbatim(t *testing.T) {
	in := "you are baifo\n"
	out := composeRootPrompt(in, rootCapabilitiesFlags{})
	if out != in {
		t.Errorf("with no flags want verbatim user prompt, got %q", out)
	}
}

// TestComposeRootPrompt_SpawnBlockShape pins the wording for the
// three spawn modes so a refactor of rootSpawnBlock can't silently
// change which tools the LLM is told to expect.
func TestComposeRootPrompt_SpawnBlockShape(t *testing.T) {
	cases := []struct {
		name            string
		staticEnabled   bool
		dynamicEnabled  bool
		wantStaticTool  bool
		wantDynamicTool bool
		wantBlock       bool
	}{
		{"both", true, true, true, true, true},
		{"static only", true, false, true, false, true},
		{"dynamic only", false, true, false, true, true},
		{"none", false, false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := composeRootPrompt("u", rootCapabilitiesFlags{
				StaticSpawn:  tc.staticEnabled,
				DynamicSpawn: tc.dynamicEnabled,
			})
			has := func(s string) bool { return strings.Contains(out, s) }
			if has("### Workers") != tc.wantBlock {
				t.Errorf("Workers block presence: got %v want %v", has("### Workers"), tc.wantBlock)
			}
			if has("`spawn_static_agent`") != tc.wantStaticTool {
				t.Errorf("static tool mention: got %v want %v", has("`spawn_static_agent`"), tc.wantStaticTool)
			}
			if has("`spawn_dynamic_agent`") != tc.wantDynamicTool {
				t.Errorf("dynamic tool mention: got %v want %v", has("`spawn_dynamic_agent`"), tc.wantDynamicTool)
			}
		})
	}
}

// TestComposeRootPrompt_UserPromptComesFirst guarantees the order:
// the user-supplied prompt is the head of the message and the
// runtime block follows. The model treats the first lines as the
// most prescriptive, so persona must lead.
func TestComposeRootPrompt_UserPromptComesFirst(t *testing.T) {
	user := "PERSONA: be terse."
	out := composeRootPrompt(user, rootCapabilitiesFlags{Memory: true})
	idxUser := strings.Index(out, user)
	idxHeader := strings.Index(out, rootCapabilitiesHeader)
	if idxUser == -1 || idxHeader == -1 {
		t.Fatalf("expected both fragments present; got out=%q", out)
	}
	if idxUser > idxHeader {
		t.Errorf("user prompt must come before capabilities header (user=%d, header=%d)",
			idxUser, idxHeader)
	}
}
