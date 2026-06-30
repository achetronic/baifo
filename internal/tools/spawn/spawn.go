// SPDX-License-Identifier: Apache-2.0

// Package spawn registers the spawn / supervise tools the root agent
// uses to manage workers. See .agents/WORKER_RUNTIME.md for the
// canonical schemas; the implementations here delegate to the
// workers.Manager so the LLM never reaches into manager internals.
//
// Phase 5 scope: static workers only. Dynamic-worker tools land in
// Phase 6 alongside the validator that ensures spec.MCPs/Skills are a
// subset of the universe baifo knows.
package spawn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/workers"
)

// TemplateResolver looks up a static-agent template by name in the
// loaded agents.yaml. The Manager wiring builds the closure; we keep
// the dependency narrow so this package does not import internal/app.
type TemplateResolver interface {
	Resolve(name string) (config.AgentTemplate, bool)

	// ListTemplates returns every static-agent template currently
	// declared, in a stable order. Used by list_agents and by the
	// spawn_static_agent description so the LLM can see what is
	// actually available without trial-and-error.
	ListTemplates() []config.AgentTemplate
}

// Tools bundles the per-spawn dependencies needed to build every
// tool. Construct one in the App's wiring, then call ADKTools().
type Tools struct {
	Manager   *workers.Manager
	Templates TemplateResolver

	// The next three fields are needed only when dynamic spawning is
	// enabled. Static-only deployments can leave them zero.
	Universe      Universe
	RootDefaults  RootDefaults
	EnableDynamic bool

	// ParentAllowedSecrets is the universe of secrets the spawning
	// agent itself can dereference. It bounds what its spawn calls
	// (static and dynamic) may grant to children — the secrets
	// pipeline enforces least-privilege from there.
	//
	// Two encodings:
	//
	//   - nil           → parent is "sovereign": no per-agent
	//                     restriction. Used for the root, which is
	//                     the user-facing coordinator and the
	//                     frontier of trust. Sub-agent allowlists
	//                     are validated against the global universe
	//                     of declared secrets in this mode.
	//   - non-nil slice → parent's explicit allowlist (possibly
	//                     empty). Sub-agent allowlists must be a
	//                     subset of this list, and an empty list
	//                     means the parent cannot delegate any
	//                     secret.
	//
	// Today only the root holds spawn tools, so this field is
	// effectively always nil in production. The shape is preserved
	// for the day a static template gets its own spawn tools.
	ParentAllowedSecrets []string
}

// OpaqueToolNames returns the names of every tool this package
// registers whose arguments must NOT be walked by the secrets
// expander. They are the spawn tools, and the reason is structural:
// their args carry the child agent's full spec — prompt, initial
// message, allowed_secrets list, sandbox path, etc. If the expander
// rewrote a ${secret:NAME} placeholder appearing inside that spec,
// the raw value would land in the child agent's prompt at
// construction time, bypassing the child's own allowlist (which is
// usually narrower than the parent's, often empty).
//
// Keeping the placeholders intact lets them propagate verbatim into
// the child's prompt. The child's own BeforeToolCallback then runs
// the expander at the child's own tool boundary, with the child's
// own allower in effect. That is the right gate for the
// allowed-secrets subset rule documented in DECISIONS.md #10 to be
// load-bearing rather than merely advisory.
//
// The list is kept here (not in internal/secrets or internal/agent)
// because this package is the source of truth for which spawn tools
// exist. Adding a new spawn-style tool means updating this list in
// the same commit; the agent.Builder reads it through
// Builder.OpaqueTools without knowing what "spawn" means.
//
// Returns a fresh slice on every call so callers cannot mutate the
// internal table by accident. Order matches the registration order
// in ADKTools().
func OpaqueToolNames() []string {
	return []string{
		"spawn_static_agent",
		"spawn_dynamic_agent",
		"spawn_dynamic_agents",
	}
}

// ADKTools returns the static-spawn / supervise toolset for the root
// agent, in the order they appear in WORKER_RUNTIME.md. The dynamic
// spawn tools are appended when t.EnableDynamic is true.
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	if t.Manager == nil {
		return nil, fmt.Errorf("spawn.Tools: Manager is required")
	}

	specs := []toolBuilder{
		{name: "spawn_static_agent", build: t.buildSpawnStatic},
		{name: "query_agent", build: t.buildQuery},
		{name: "list_agents", build: t.buildListTemplates},
		{name: "list_running_agents", build: t.buildListRunning},
		{name: "inspect_agent", build: t.buildInspect},
		{name: "collect_agent", build: t.buildCollect},
		{name: "kill_agent", build: t.buildKill},
	}
	if t.EnableDynamic {
		if t.Universe == nil {
			return nil, fmt.Errorf("spawn.Tools: Universe is required when EnableDynamic is true")
		}
		specs = append(specs,
			toolBuilder{name: "spawn_dynamic_agent", build: t.buildSpawnDynamic},
			toolBuilder{name: "spawn_dynamic_agents", build: t.buildSpawnDynamicBatch},
		)
	}

	out := make([]tool.Tool, 0, len(specs))
	for _, s := range specs {
		tl, err := s.build()
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", s.name, err)
		}
		out = append(out, tl)
	}
	return out, nil
}

// toolBuilder pairs a name with its constructor so the table above
// reads cleanly.
type toolBuilder struct {
	name  string
	build func() (tool.Tool, error)
}

// spawn_static_agent

// SpawnStaticArgs is the input of spawn_static_agent.
type SpawnStaticArgs struct {
	Name           string   `json:"name"`
	InitialMessage string   `json:"initial_message"`
	AllowedSecrets []string `json:"allowed_secrets,omitempty"`
}

// SpawnResult is the result returned by both spawn tools.
type SpawnResult struct {
	WorkerID string `json:"worker_id"`
}

func (t *Tools) buildSpawnStatic() (tool.Tool, error) {
	desc := composeStaticDescription(t.Templates)
	return functiontool.New(
		functiontool.Config{Name: "spawn_static_agent", Description: desc},
		func(ctx tool.Context, a SpawnStaticArgs) (SpawnResult, error) {
			if t.Templates == nil {
				return SpawnResult{}, fmt.Errorf("no agents.yaml loaded")
			}
			tmpl, ok := t.Templates.Resolve(a.Name)
			if !ok {
				return SpawnResult{}, fmt.Errorf("unknown static agent %q", a.Name)
			}
			allowed, err := resolveStaticAllowedSecrets(tmpl.AllowedSecrets, a.AllowedSecrets, t.ParentAllowedSecrets)
			if err != nil {
				return SpawnResult{}, err
			}
			spec := workers.Spec{
				Kind:           workers.KindStatic,
				Name:           tmpl.Name,
				Description:    tmpl.Description,
				Prompt:         tmpl.Prompt,
				Provider:       tmpl.LLM.Effective(),
				Model:          tmpl.LLM.Model,
				Reasoning:      tmpl.LLM.Reasoning,
				ReasoningAPI:   tmpl.LLM.ReasoningAPI,
				Skills:         tmpl.Skills,
				MCPs:           tmpl.MCPs,
				AllowedSecrets: allowed,
				InitialMessage: a.InitialMessage,
			}
			w, err := t.Manager.Spawn(ctx, spec)
			if err != nil {
				return SpawnResult{}, err
			}
			return SpawnResult{WorkerID: w.ID()}, nil
		},
	)
}

// resolveStaticAllowedSecrets computes the effective allowlist for a
// static-spawn call. Rules:
//
//   - The template's declared allowlist (`tmpl.AllowedSecrets`) is
//     the hard ceiling for this worker. Operators put it there
//     deliberately.
//   - The LLM's override (`spawn_args.AllowedSecrets`) may only
//     narrow the ceiling: every entry must be a subset of the
//     template's. A nil override means "keep the template's";
//     an empty slice means "drop every secret for this run".
//   - Independently, the spawning agent (this Tools.Parent) cannot
//     grant secrets it does not itself hold. In production the
//     parent is the root, which is sovereign (parent slice == nil),
//     so this clause is a no-op; the validation matters once a
//     sub-agent receives its own spawn tools.
//
// Returns the slice to forward to the worker, or an error message
// the LLM can read and recover from.
func resolveStaticAllowedSecrets(template, override, parent []string) ([]string, error) {
	if err := validateSubsetOfParent(template, parent, "template's allowed_secrets"); err != nil {
		return nil, err
	}
	if override == nil {
		return template, nil
	}
	if err := validateSubset(template, override, "secret"); err != nil {
		return nil, fmt.Errorf("override exceeds template allowlist: %w", err)
	}
	if err := validateSubsetOfParent(override, parent, "override allowed_secrets"); err != nil {
		return nil, err
	}
	return override, nil
}

// validateSubsetOfParent ensures every name in want is also in
// parent. A nil parent slice represents a sovereign parent (no
// restriction); in that case the function is a no-op.
//
// We accept "context" as a free-text prefix so error messages name
// the offending slice (e.g. "override allowed_secrets") rather than
// the generic "subset" wording.
func validateSubsetOfParent(want, parent []string, context string) error {
	if parent == nil {
		return nil
	}
	set := make(map[string]struct{}, len(parent))
	for _, p := range parent {
		set[p] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return fmt.Errorf("%s contains %q which the spawning agent cannot grant", context, w)
		}
	}
	return nil
}

// composeStaticDescription builds the spawn_static_agent tool docs.
// The list of template names is embedded so the LLM sees what is
// actually available without having to call list_agents first. The
// description regenerates on every boot / reload (this constructor
// runs each time the root agent rebuilds), so renaming or adding a
// template surfaces here automatically.
func composeStaticDescription(r TemplateResolver) string {
	base := "Spawn a static worker from the templates declared in agents.yaml. " +
		"Returns the worker_id immediately; use query_agent / collect_agent to interact with it."
	if r == nil {
		return base
	}
	templates := r.ListTemplates()
	if len(templates) == 0 {
		return base + " No templates are currently declared."
	}
	names := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		names = append(names, tmpl.Name)
	}
	return base + " Available templates: " + strings.Join(names, ", ") +
		". Call list_agents for the full description of each."
}

// query_agent

// QueryArgs is the input of query_agent.
type QueryArgs struct {
	WorkerID string `json:"worker_id"`
	Message  string `json:"message"`
}

// QueryResult is what the root sees after enqueueing a follow-up.
type QueryResult struct {
	OK bool `json:"ok"`
}

func (t *Tools) buildQuery() (tool.Tool, error) {
	const desc = "Send a new user message to an existing worker. Returns immediately; " +
		"the worker transitions to running. Call collect_agent once status is idle."
	return functiontool.New(
		functiontool.Config{Name: "query_agent", Description: desc},
		func(ctx tool.Context, a QueryArgs) (QueryResult, error) {
			if err := t.Manager.Query(ctx, a.WorkerID, a.Message); err != nil {
				return QueryResult{}, err
			}
			return QueryResult{OK: true}, nil
		},
	)
}

// list_agents (templates)

// TemplateSummary is the per-template row returned by list_agents.
// Description is the one-line note the user wrote in agents.yaml; the
// LLM uses it to pick the right template for the task at hand.
type TemplateSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
}

// TemplatesResult is the response shape of list_agents.
type TemplatesResult struct {
	Templates []TemplateSummary `json:"templates"`
}

func (t *Tools) buildListTemplates() (tool.Tool, error) {
	const desc = "List every static agent template declared in agents.yaml " +
		"with its description, provider and model. These are the names " +
		"accepted by spawn_static_agent."
	return functiontool.New(
		functiontool.Config{Name: "list_agents", Description: desc},
		func(_ tool.Context, _ struct{}) (TemplatesResult, error) {
			if t.Templates == nil {
				return TemplatesResult{Templates: []TemplateSummary{}}, nil
			}
			tmpls := t.Templates.ListTemplates()
			out := TemplatesResult{Templates: make([]TemplateSummary, 0, len(tmpls))}
			for _, tmpl := range tmpls {
				out.Templates = append(out.Templates, TemplateSummary{
					Name:        tmpl.Name,
					Description: tmpl.Description,
					Provider:    tmpl.LLM.Effective(),
					Model:       tmpl.LLM.Model,
				})
			}
			return out, nil
		},
	)
}

// list_running_agents (live workers)

// WorkerSummary is the per-worker row returned by list_running_agents
// and inspect_agent. Time-formatted so the LLM can reason about
// elapsed without re-parsing.
type WorkerSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Elapsed   string `json:"elapsed"`
	LastEvent string `json:"last_event,omitempty"`
}

// ListResult is the response shape of list_running_agents.
type ListResult struct {
	Workers []WorkerSummary `json:"workers"`
}

func (t *Tools) buildListRunning() (tool.Tool, error) {
	const desc = "List every LIVE worker currently spawned, with its status, " +
		"elapsed time, and last event. For the catalogue of templates " +
		"available to spawn, use list_agents instead."
	return functiontool.New(
		functiontool.Config{Name: "list_running_agents", Description: desc},
		func(_ tool.Context, _ struct{}) (ListResult, error) {
			infos := t.Manager.List()
			out := ListResult{Workers: make([]WorkerSummary, 0, len(infos))}
			for _, info := range infos {
				out.Workers = append(out.Workers, summaryFromInfo(info))
			}
			return out, nil
		},
	)
}

// summaryFromInfo formats a WorkerInfo for the LLM consumer.
func summaryFromInfo(info workers.WorkerInfo) WorkerSummary {
	return WorkerSummary{
		ID:        info.ID,
		Name:      info.Name,
		Kind:      kindString(info.Kind),
		Status:    info.Status.String(),
		Elapsed:   time.Since(info.StartedAt).Round(time.Second).String(),
		LastEvent: info.LastEvent,
	}
}

func kindString(k workers.Kind) string {
	if k == workers.KindStatic {
		return "static"
	}
	return "dynamic"
}

// inspect_agent

// InspectArgs is the input of inspect_agent. SinceEvent is reserved
// for the day the manager keeps a per-worker ring buffer of events.
type InspectArgs struct {
	WorkerID   string `json:"worker_id"`
	SinceEvent int    `json:"since_event,omitempty"`
}

func (t *Tools) buildInspect() (tool.Tool, error) {
	const desc = "Peek at a worker's current state without collecting it."
	return functiontool.New(
		functiontool.Config{Name: "inspect_agent", Description: desc},
		func(_ tool.Context, a InspectArgs) (WorkerSummary, error) {
			info, err := t.Manager.Inspect(a.WorkerID, a.SinceEvent)
			if err != nil {
				return WorkerSummary{}, err
			}
			return summaryFromInfo(info), nil
		},
	)
}

// collect_agent

// CollectArgs is the input of collect_agent.
type CollectArgs struct {
	WorkerID  string `json:"worker_id"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

// CollectResult is the response shape: output text + final status.
type CollectResult struct {
	Output string `json:"output"`
	Status string `json:"status"`
	Err    string `json:"error,omitempty"`
}

func (t *Tools) buildCollect() (tool.Tool, error) {
	const desc = "Wait for a worker to reach idle/done, return its output text, and remove it from the registry."
	return functiontool.New(
		functiontool.Config{Name: "collect_agent", Description: desc},
		func(ctx tool.Context, a CollectArgs) (CollectResult, error) {
			timeout := time.Duration(a.TimeoutMs) * time.Millisecond
			info, err := t.Manager.Collect(ctx, a.WorkerID, timeout)
			if err != nil {
				return CollectResult{Status: info.Status.String(), Err: err.Error()}, nil
			}
			return CollectResult{
				Output: info.Output,
				Status: info.Status.String(),
				Err:    info.Err,
			}, nil
		},
	)
}

// kill_agent

// KillArgs is the input of kill_agent.
type KillArgs struct {
	WorkerID string `json:"worker_id"`

	// Reason is an optional explanation surfaced on the subsequent
	// collect_agent call as CollectResult.Err. The LLM can use it to
	// pass context ("superseded by newer plan", "worker stuck on
	// rate-limit", ...). Empty defaults to "killed by agent".
	Reason string `json:"reason,omitempty"`
}

// KillResult mirrors QueryResult: a simple acknowledgment.
type KillResult struct {
	OK bool `json:"ok"`
}

func (t *Tools) buildKill() (tool.Tool, error) {
	const desc = "Cancel a running worker. The worker transitions to killed and is auto-collected after a grace period. Optional reason explains why."
	return functiontool.New(
		functiontool.Config{Name: "kill_agent", Description: desc},
		func(_ tool.Context, a KillArgs) (KillResult, error) {
			reason := a.Reason
			if reason == "" {
				reason = "killed by agent"
			}
			if err := t.Manager.Kill(a.WorkerID, reason); err != nil {
				return KillResult{}, err
			}
			return KillResult{OK: true}, nil
		},
	)
}

// Silence the unused-import check on context when transitional edits
// leave the file in an in-between state.
var _ = context.Background
