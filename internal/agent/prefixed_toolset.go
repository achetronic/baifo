// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// prefixed_toolset.go namespaces the tools of an external MCP with the
// MCP's configured name, so the coordinator can tell which server a
// tool came from and two MCPs that both expose, say, "list" don't
// collide.
//
// The MCP SDK hands us tools with the server's raw names
// (images_upscale, audio_tts, ...). Nothing in that name says "this
// belongs to magnific", so when the user asks "what can magnific do?"
// the model can only pattern-match the brand inside descriptions and
// guesses. Prefixing turns every tool into "<mcp>__<tool>"
// (magnific__images_upscale), which:
//
//   - gives the model unambiguous provenance, and
//   - prevents name collisions between MCPs.
//
// The rename is presentation-only. The wrapped tool still calls the
// server with its ORIGINAL name (mcpTool.Run closes over its own
// t.name), so the prefix never reaches the wire — only the model's
// view of the catalogue and the function-call routing key change.

// toolNamePrefixSep separates the MCP name from the tool name. Double
// underscore is the de-facto convention among MCP gateways and stays
// within the function-name charset every provider accepts.
const toolNamePrefixSep = "__"

// declRunTool is the concrete surface a tool must expose for the ADK
// flow to declare and invoke it: tool.Tool plus Declaration() and
// Run(). It mirrors the unexported toolinternal.FunctionTool. MCP
// tools satisfy it (their methods are exported); we assert against it
// so a future ADK tool type that doesn't would degrade visibly rather
// than silently dropping tools.
type declRunTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx tool.Context, args any) (map[string]any, error)
}

// prefixedToolset wraps an external MCP's toolset and renames every
// tool to "<prefix>__<name>".
type prefixedToolset struct {
	inner  tool.Toolset
	prefix string
}

// newPrefixedToolset wraps inner so its tools surface under prefix.
// A blank prefix returns inner untouched (defensive; callers pass the
// MCP name which is always non-empty in practice).
func newPrefixedToolset(prefix string, inner tool.Toolset) tool.Toolset {
	if strings.TrimSpace(prefix) == "" {
		return inner
	}
	return &prefixedToolset{inner: inner, prefix: prefix}
}

func (p *prefixedToolset) Name() string { return p.inner.Name() }

func (p *prefixedToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	inner, err := p.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(inner))
	for _, t := range inner {
		dr, ok := t.(declRunTool)
		if !ok {
			// Can't safely rename a tool we can't re-declare and
			// re-run. Pass it through unprefixed rather than drop it.
			out = append(out, t)
			continue
		}
		out = append(out, &prefixedTool{inner: dr, prefix: p.prefix})
	}
	return out, nil
}

// prefixedTool renames one tool. It implements the tool.Tool +
// Declaration + Run surface (so the ADK flow treats it as a function
// tool) and ProcessRequest (so the flow packs it into the request
// under the prefixed name).
type prefixedTool struct {
	inner  declRunTool
	prefix string
}

func (t *prefixedTool) prefixedName() string {
	return t.prefix + toolNamePrefixSep + t.inner.Name()
}

func (t *prefixedTool) Name() string        { return t.prefixedName() }
func (t *prefixedTool) Description() string { return t.inner.Description() }
func (t *prefixedTool) IsLongRunning() bool { return t.inner.IsLongRunning() }

// Declaration returns a shallow copy of the inner declaration with the
// name swapped for the prefixed one. We copy so we never mutate the
// toolset's shared declaration (a second build/turn would otherwise
// see a doubly-prefixed name).
func (t *prefixedTool) Declaration() *genai.FunctionDeclaration {
	inner := t.inner.Declaration()
	if inner == nil {
		return nil
	}
	clone := *inner
	clone.Name = t.prefixedName()
	return &clone
}

// Run delegates to the inner tool unchanged. The inner MCP tool calls
// the server with its own original name, so the prefix is purely a
// catalogue/routing concern and never crosses the wire.
func (t *prefixedTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	return t.inner.Run(ctx, args)
}

// ProcessRequest packs this tool into the LLM request under its
// prefixed name. It mirrors the SDK's internal toolutils.PackTool
// (which we can't import): register the tool in req.Tools keyed by the
// name the model will call, and append its prefixed declaration to the
// request's function-declaration list. Keeping the key and the
// declaration name identical is what lets the flow route a
// "magnific__images_upscale" function call back to this wrapper.
func (t *prefixedTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	name := t.prefixedName()
	if _, dup := req.Tools[name]; dup {
		return fmt.Errorf("duplicate tool: %q", name)
	}
	req.Tools[name] = t

	decl := t.Declaration()
	if decl == nil {
		return nil
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	// Consolidate into the single function-declaration genai.Tool the
	// SDK expects, matching PackTool's behaviour.
	var funcTool *genai.Tool
	for _, gt := range req.Config.Tools {
		if gt != nil && gt.FunctionDeclarations != nil {
			funcTool = gt
			break
		}
	}
	if funcTool == nil {
		req.Config.Tools = append(req.Config.Tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{decl},
		})
	} else {
		funcTool.FunctionDeclarations = append(funcTool.FunctionDeclarations, decl)
	}
	return nil
}

// Compile-time checks that the wrapper exposes the surfaces the ADK
// flow requires: tool.Toolset for the set, and the function-tool +
// declaration + run surface for each tool (mirrors the unexported
// toolinternal.FunctionTool).
var (
	_ tool.Toolset = (*prefixedToolset)(nil)
	_ declRunTool  = (*prefixedTool)(nil)
)
