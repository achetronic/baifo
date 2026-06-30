// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package agent builds ADK agents from baifo's high-level Spec.
//
// The Builder is the SINGLE place where agent.Agent instances are
// constructed. By going through Build, every agent automatically gets
// the secrets pipeline, audit logging, and (in later phases) the
// context guard plugin attached in the right order. Code outside this
// package must not call llmagent.New directly.
package agent

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"

	"github.com/achetronic/baifo/internal/audit"
	"github.com/achetronic/baifo/internal/mcps"
	"github.com/achetronic/baifo/internal/providers"
	"github.com/achetronic/baifo/internal/secrets"
)

// Spec is baifo's description of an agent, decoupled from ADK types so
// callers (config loader, spawn tools, ...) can build agents without
// importing the ADK directly.
type Spec struct {
	Name        string
	Description string
	Prompt      string

	// Provider + Model resolve through the providers.Registry.
	Provider string
	Model    string

	// AllowedSecrets is the whitelist for the secret expander. The
	// length encodes the policy:
	//
	//   - nil or empty → the agent cannot dereference any secret
	//     (least-privilege default). Built sub-agents land here when
	//     the operator declared no allowed_secrets on a template, or
	//     when a dynamic spawn omitted the field.
	//   - non-empty    → exactly those names are allowed.
	//
	// The ROOT agent does not go through this field. It is built
	// with UnrestrictedSecrets=true and gets AllowAll instead, so it
	// can dereference every secret the operator stored. See
	// SECRETS.md for the threat-model rationale.
	AllowedSecrets []string

	// UnrestrictedSecrets, when true, replaces the per-name allowlist
	// with AllowAll. Reserved for the root agent. Sub-agents must
	// leave this false and use AllowedSecrets to enumerate exactly
	// what they can see.
	UnrestrictedSecrets bool

	// MCPs lists the MCP names (as declared in baifo.yaml) whose tools
	// should be exposed to this agent. The Builder resolves them via
	// mcps.Registry.Tools.
	MCPs []string

	// Skills lists the skill slugs that should be available to this
	// agent. Skills do not contribute tool.Tool entries yet (see the
	// SKILL TOOLSET note in memory); reserved here so callers can
	// already wire the field from baifo.yaml.
	Skills []string

	// Reasoning is the optional reasoning-effort knob: one of
	// "minimal" / "low" / "medium" / "high" (empty = leave the model's
	// own default). The builder turns it into a request-level
	// GenerateContentConfig.ThinkingConfig (honoured by openai and
	// gemini) AND, for anthropic, a construction-time thinking budget
	// passed through providers.ModelOptions. Validation of the value
	// lives at the config / spawn boundary; an unrecognised value here
	// is treated as "unset" so a bad string never breaks agent
	// construction.
	Reasoning string

	// ReasoningAPI optionally overrides which Anthropic reasoning API the
	// model speaks: "enabled" (classic budget-based) or "adaptive"
	// (effort-based, Opus 4.5+). Empty lets the builder auto-detect from
	// the catwalk catalogue. Ignored by non-anthropic providers. An
	// unrecognised value is treated as "unset".
	ReasoningAPI string

	// ExtraTools are additional tools to attach beyond what MCPs and
	// Skills provide. Mostly useful for tests and for spawn/supervise
	// machinery in future phases.
	ExtraTools []tool.Tool
}

// Instance is the result of building an agent: the ADK Agent plus
// enough context for callers to identify it in audit records and TUI
// listings without crossing the ADK boundary.
type Instance struct {
	ID    string
	Spec  Spec
	Agent agent.Agent

	// LLM is the model the agent was built with. Exposed so the
	// context-guard plugin can hand it to the contextguard library
	// for summarisation. nil only when the agent has no LLM, which
	// is currently impossible (Build returns ErrNoRoot in that case)
	// but kept nullable to match agent.Agent's contract.
	LLM model.LLM
}

// Builder turns a Spec into a fully-wired Instance. The collaborators
// it depends on are fields rather than parameters of Build so the same
// builder can produce many agents during the life of the process.
type Builder struct {
	Providers *providers.Registry
	Secrets   *secrets.Store
	Audit     *audit.Recorder

	// MCPs is optional. When nil, Spec.MCPs must be empty or Build
	// returns an error. Provided as a field (not a parameter) so the
	// rest of the wiring stays compact.
	MCPs *mcps.Registry

	// ModelWrapper, when non-nil, is given the LLM the providers
	// registry produced and returns the one the agent will actually
	// use. Used to install diagnostic wrappers (request dumpers,
	// rate-limiters, ...) without sprinkling conditionals through the
	// build path. A nil wrapper is a no-op (identity).
	ModelWrapper func(model.LLM) model.LLM

	// OpaqueTools lists tool names whose arguments must NOT be walked
	// by the secrets expander. The canonical population is the spawn
	// tool names (see internal/tools/spawn.OpaqueToolNames), which
	// take agent specs as input. If the expander ran on them, a
	// ${secret:NAME} placeholder embedded in the child's prompt or
	// initial_message would be substituted eagerly with the raw value
	// and baked into the child agent's prompt, bypassing the child's
	// own allowlist. Leaving the placeholders intact lets the child's
	// own BeforeToolCallback decide, at its own tool boundary, whether
	// to expand (using its own, typically narrower, allower).
	//
	// nil or empty disables the guard. Callers should pass the list
	// verbatim; the Builder converts it into a set internally.
	OpaqueTools []string

	// ScrubAllResponses controls the AfterToolCallback's
	// comprehensive-redaction pass: when true (the recommended
	// default), every tool result is scanned for ANY raw value
	// currently stored in `Secrets`, not just the values the
	// BeforeToolCallback substituted in this call. This closes the
	// "tools that emit secrets they were not given" gap (think of an
	// MCP echoing a debug header that contains a token from a
	// different secret in the store).
	//
	// Set to false to disable the comprehensive pass, useful only
	// for debugging or when the operator has profiled and found the
	// scan dominates a hot path. The targeted pass (scrubbing values
	// expanded in this call) remains active regardless.
	ScrubAllResponses bool

	// MinScrubLength is the minimum length (in bytes) a stored
	// secret value must have to be eligible for the comprehensive
	// pass. Below this floor the value is skipped to avoid
	// catastrophic false positives: e.g. a stored value of "1234"
	// would otherwise redact every "1234" anywhere in any tool
	// result.
	//
	// The targeted pass is unaffected: short values stay protected
	// when the LLM explicitly references them via ${secret:NAME}.
	// Recommended floor is 8 bytes (real API keys are far longer);
	// 0 or negative disables the filter entirely (every length goes
	// through), which is documented as dangerous in CONFIG.md and
	// SECRETS.md.
	MinScrubLength int
}

// ErrInvalidSpec is returned when the Spec is missing required fields.
var ErrInvalidSpec = errors.New("invalid agent spec")

// RunConfigForStreaming maps a per-provider streaming preference onto
// the ADK RunConfig the executors and runners consume. Streaming (SSE)
// is the default and is required for long Anthropic turns; non-streaming
// is reserved for OpenAI-compatible endpoints that do not implement SSE.
// Centralised here so every call site agrees on the mapping.
func RunConfigForStreaming(streaming bool) agent.RunConfig {
	mode := agent.StreamingModeSSE
	if !streaming {
		mode = agent.StreamingModeNone
	}
	return agent.RunConfig{StreamingMode: mode}
}

// Build constructs the agent. The order in which callbacks are wired
// matters and is documented inline.
func (b *Builder) Build(ctx context.Context, id string, spec Spec) (*Instance, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidSpec)
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidSpec)
	}
	if spec.Provider == "" || spec.Model == "" {
		return nil, fmt.Errorf("%w: provider and model are required", ErrInvalidSpec)
	}
	if b.Providers == nil {
		return nil, fmt.Errorf("builder: providers registry is nil")
	}

	// Reasoning is resolved once into a unified config that holds both
	// the request-level form (for openai/gemini) and the construction-time
	// form (for anthropic). The provider builders ignore the fields they
	// do not consume.
	rCfg := BuildReasoningConfig(spec.Model, spec.Reasoning, spec.ReasoningAPI)
	var modelOpts []providers.ModelOptions
	if rCfg.ModelOptions != nil {
		modelOpts = append(modelOpts, *rCfg.ModelOptions)
	}

	llm, err := b.Providers.Model(ctx, spec.Provider, spec.Model, modelOpts...)
	if err != nil {
		return nil, fmt.Errorf("resolve model: %w", err)
	}
	if b.ModelWrapper != nil {
		llm = b.ModelWrapper(llm)
	}

	tools, toolsets, err := b.resolveTools(spec)
	if err != nil {
		return nil, err
	}

	// InstructionProvider serves the static prompt verbatim. We use the
	// provider variant (not Instruction) to opt out of ADK's {var}
	// template substitution because that machinery clashes with user-supplied
	// prompts that legitimately contain curly braces.
	instructionProvider := staticInstructionProvider(spec.Prompt)

	// Build the callback chain. Order matters:
	//   1. secrets.Expand expands ${secret:NAME} BEFORE the tool runs.
	//   2. The audit hook logs the (redacted) call AFTER the tool runs,
	//      receiving args/result already redacted.
	//   3. secrets.Redact scrubs raw values from the result so the LLM
	//      never sees them.
	//
	// Before-tool callbacks run top-to-bottom; after-tool callbacks
	// also run top-to-bottom but we want audit BEFORE redaction in the
	// list, so audit sees the post-expansion / pre-redaction args. We
	// invert that by running redaction first and audit second.
	// Pick the secrets allower. The root agent is the human-facing
	// coordinator (the frontier of trust); it must be able to
	// dereference every secret the operator stored. Sub-agents go
	// through AllowerFor with an explicit list, and an empty list
	// is least-privilege, NOT "all".
	var allower secrets.Allower
	if spec.UnrestrictedSecrets {
		allower = secrets.AllowAll{}
	} else {
		allower = secrets.AllowerFor(spec.AllowedSecrets)
	}

	// Build the set of tool names whose args bypass the expander.
	// Empty set is fine; the callback just always runs the walker.
	// Doing the conversion once here avoids re-allocating on every
	// tool call in the hot path.
	var opaqueSet map[string]struct{}
	if len(b.OpaqueTools) > 0 {
		opaqueSet = make(map[string]struct{}, len(b.OpaqueTools))
		for _, name := range b.OpaqueTools {
			opaqueSet[name] = struct{}{}
		}
	}

	before := []llmagent.BeforeToolCallback{
		makeBeforeExpand(b.Secrets, allower, opaqueSet),
	}
	after := []llmagent.AfterToolCallback{
		makeAfterRedact(b.Secrets, b.ScrubAllResponses, b.MinScrubLength),
		makeAfterAudit(id, b.Audit),
	}

	llmCfg := llmagent.Config{
		Name:                spec.Name,
		Description:         spec.Description,
		Model:               llm,
		InstructionProvider: llmagent.InstructionProvider(instructionProvider),
		Tools:               tools,
		Toolsets:            toolsets,
		BeforeToolCallbacks: before,
		AfterToolCallbacks:  after,
	}

	if rCfg.GenerateContentConfig != nil {
		llmCfg.GenerateContentConfig = rCfg.GenerateContentConfig
	}

	a, err := llmagent.New(llmCfg)
	if err != nil {
		return nil, fmt.Errorf("build llm agent: %w", err)
	}

	return &Instance{
		ID:    id,
		Spec:  spec,
		Agent: a,
		LLM:   llm,
	}, nil
}

// resolveTools and resolveToolsets split the spec.MCPs list into
// the two ways ADK accepts tool surfaces:
//
//   - Builtin MCPs are in-process; their tools are concrete and
//     resolved eagerly via Registry.Tools(name). They go into the
//     []tool.Tool list passed as llmagent.Config.Tools.
//   - External MCPs (http / stdio) talk to a remote server. We hand
//     a tool.Toolset to ADK so it can connect lazily on the first
//     ListTools call. They land in llmagent.Config.Toolsets.
//
// spec.ExtraTools is appended verbatim to the static tool list.
func (b *Builder) resolveTools(spec Spec) ([]tool.Tool, []tool.Toolset, error) {
	if len(spec.MCPs) > 0 && b.MCPs == nil {
		return nil, nil, fmt.Errorf("builder: spec references MCPs but registry is nil")
	}

	var tools []tool.Tool
	var toolsets []tool.Toolset
	for _, name := range spec.MCPs {
		external, err := b.MCPs.IsExternal(name)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve mcp %q: %w", name, err)
		}
		if external {
			ts, err := b.MCPs.Toolset(name)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve mcp %q: %w", name, err)
			}
			// Namespace the server's tools with the MCP's configured
			// name (magnific__images_upscale) so the model knows their
			// provenance and two MCPs can't collide on a shared tool
			// name. The rename is presentation-only; the wrapped tool
			// still calls the server with its original name.
			ts = newPrefixedToolset(name, ts)
			// Wrap so a slow / unreachable / unauthenticated MCP
			// degrades to "no tools this turn" instead of hanging or
			// erroring the whole agent run. The root sees every MCP by
			// default, so one bad entry must not take the coordinator
			// down. See resilient_toolset.go.
			toolsets = append(toolsets, newResilientToolset(name, ts))
			continue
		}
		list, err := b.MCPs.Tools(name)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve mcp %q: %w", name, err)
		}
		tools = append(tools, list...)
	}
	tools = append(tools, spec.ExtraTools...)
	return tools, toolsets, nil
}
