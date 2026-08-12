// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// fakeMCPTool is a declRunTool stub that records whether Run was
// invoked, so tests can prove delegation happens with the original
// (unprefixed) identity.
type fakeMCPTool struct {
	name string
	ran  bool
}

func (f *fakeMCPTool) Name() string        { return f.name }
func (f *fakeMCPTool) Description() string { return "desc of " + f.name }
func (f *fakeMCPTool) IsLongRunning() bool { return false }
func (f *fakeMCPTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: f.name, Description: "desc of " + f.name}
}
func (f *fakeMCPTool) Run(_ agent.Context, _ any) (map[string]any, error) {
	f.ran = true
	return map[string]any{"ok": true}, nil
}

// fakeToolset returns a fixed set of declRunTools.
type fakeToolset struct {
	name  string
	tools []tool.Tool
}

func (f *fakeToolset) Name() string { return f.name }
func (f *fakeToolset) Tools(_ agent.ReadonlyContext) ([]tool.Tool, error) {
	return f.tools, nil
}

// TestPrefixedToolset_RenamesTools confirms every tool surfaces under
// "<prefix>__<name>" while Description/Declaration stay consistent.
func TestPrefixedToolset_RenamesTools(t *testing.T) {
	inner := &fakeToolset{
		name:  "mcp:magnific",
		tools: []tool.Tool{&fakeMCPTool{name: "images_upscale"}, &fakeMCPTool{name: "audio_tts"}},
	}
	ps := newPrefixedToolset("magnific", inner)

	tools, err := ps.Tools(rc())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	got := map[string]bool{}
	for _, tl := range tools {
		got[tl.Name()] = true
		dr := tl.(declRunTool)
		if dr.Declaration().Name != tl.Name() {
			t.Errorf("declaration name %q != tool name %q (LLM routing key must match)",
				dr.Declaration().Name, tl.Name())
		}
	}
	for _, want := range []string{"magnific__images_upscale", "magnific__audio_tts"} {
		if !got[want] {
			t.Errorf("missing prefixed tool %q; got %v", want, got)
		}
	}
}

// TestPrefixedTool_RunDelegatesToInner proves the prefix is cosmetic:
// invoking the renamed tool runs the original inner tool (which is
// what calls the MCP server with its real name).
func TestPrefixedTool_RunDelegatesToInner(t *testing.T) {
	inner := &fakeMCPTool{name: "images_upscale"}
	pt := &prefixedTool{inner: inner, prefix: "magnific"}

	if pt.Name() != "magnific__images_upscale" {
		t.Fatalf("Name() = %q", pt.Name())
	}
	if _, err := pt.Run(nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !inner.ran {
		t.Error("Run did not delegate to the inner tool")
	}
}

// TestPrefixedTool_ProcessRequestPacksUnderPrefixedName confirms the
// tool registers itself in req.Tools under the prefixed key and adds a
// matching declaration — the two must agree for function-call routing.
func TestPrefixedTool_ProcessRequestPacksUnderPrefixedName(t *testing.T) {
	pt := &prefixedTool{inner: &fakeMCPTool{name: "images_upscale"}, prefix: "magnific"}

	req := &model.LLMRequest{}
	if err := pt.ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	if _, ok := req.Tools["magnific__images_upscale"]; !ok {
		t.Errorf("req.Tools missing prefixed key; have %v", keysOf(req.Tools))
	}
	// The declaration list must carry the prefixed name too.
	found := false
	if req.Config != nil {
		for _, gt := range req.Config.Tools {
			for _, d := range gt.FunctionDeclarations {
				if d.Name == "magnific__images_upscale" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("declaration list missing the prefixed function name")
	}
}

// TestPrefixedToolset_BlankPrefixPassesThrough confirms a blank prefix
// is a no-op wrapper (returns the inner toolset unchanged).
func TestPrefixedToolset_BlankPrefixPassesThrough(t *testing.T) {
	inner := &fakeToolset{name: "x"}
	if got := newPrefixedToolset("  ", inner); got != inner {
		t.Errorf("blank prefix should return inner unchanged")
	}
}

// TestPrefixedTool_DeclarationDoesNotMutateInner guards against double
// prefixing across turns: building the declaration twice must not
// compound the prefix on the shared inner declaration.
func TestPrefixedTool_DeclarationDoesNotMutateInner(t *testing.T) {
	inner := &fakeMCPTool{name: "images_upscale"}
	pt := &prefixedTool{inner: inner, prefix: "magnific"}

	_ = pt.Declaration()
	_ = pt.Declaration()

	if inner.Declaration().Name != "images_upscale" {
		t.Errorf("inner declaration was mutated to %q", inner.Declaration().Name)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
