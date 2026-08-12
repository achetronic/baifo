// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package spawn

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	baifoagent "github.com/achetronic/baifo/internal/agent"
	"github.com/achetronic/baifo/internal/workers"
)

// Universe enumerates the building blocks baifo knows about, used both
// to validate dynamic spawn specs (per decision #7) and to inject the
// list into the spawn-tool descriptions so the LLM composes specs
// from the right vocabulary.
type Universe interface {
	ListSkills() []string
	ListMCPs() []string
	ListProviders() []string
	ListSecretNames() []string

	// MCPTools returns the names of the tools exposed by the named
	// MCP. Used by the spawn_dynamic_agent description so the LLM
	// understands what a worker actually inherits when it lists an
	// MCP in its spec — otherwise the model assumes from the MCP
	// name alone (e.g. it sees "filesystem" and concludes the MCP
	// is read/write only, missing that "exec" lives there too).
	// Returns nil for an unknown name; callers should not error.
	MCPTools(name string) []string

	// MCPExternal reports whether the named MCP uses an external
	// transport (http / stdio). Those connect lazily, so their tool
	// list is unknown at description-compose time and MCPTools
	// returns nil for them — not because the server is empty, but
	// because we haven't connected yet. writeMCPs uses this to phrase
	// the difference accurately ("tools resolve at call time") instead
	// of implying an external MCP advertises nothing.
	MCPExternal(name string) bool

	// SpawnSkillDetails and SecretDetails enrich the spawn
	// description with the metadata the LLM needs to choose well:
	// a skill's purpose (from frontmatter) and a secret's intent
	// (from the store's description field). Returning empty slices
	// is fine — composeDynamicDescription falls back to "(none)".
	// Skill variant is namespaced SpawnSkillDetails so it does not
	// collide with App.SkillDetails (which already exists for the
	// TUI's Settings overlay and returns a different shape).
	SpawnSkillDetails() []NamedDescription
	SecretDetails() []NamedDescription
}

// NamedDescription is the shape Universe.SkillDetails /
// SecretDetails return. Kept in this package (not borrowed from
// internal/app) so the spawn tools stay free of import cycles and
// tests can build trivial fakes.
type NamedDescription struct {
	Name        string
	Description string
}

// DynamicSpawnArgs is the input of spawn_dynamic_agent. Mirrors the
// schema in WORKER_RUNTIME.md. ContextGuard is omitted in v1; the
// builder ignores the field until contextguard is wired in.
type DynamicSpawnArgs struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`

	LLM            DynamicLLM `json:"llm,omitempty"`
	Skills         []string   `json:"skills,omitempty"`
	MCPs           []string   `json:"mcps,omitempty"`
	AllowedSecrets []string   `json:"allowed_secrets,omitempty"`
	InitialMessage string     `json:"initial_message,omitempty"`
}

// DynamicLLM is the per-spawn LLM override. When unset the worker
// inherits the root agent's provider+model (resolved at build time).
type DynamicLLM struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	// Reasoning optionally sets how hard the worker thinks for models
	// that support it: one of "minimal" / "low" / "medium" / "high"
	// (empty = the model's default). Call list_models to see which
	// models accept reasoning and at which levels.
	Reasoning string `json:"reasoning,omitempty"`
}

// BatchSpawnArgs is the input of spawn_dynamic_agents — pure
// convenience over calling spawn_dynamic_agent N times in one turn.
type BatchSpawnArgs struct {
	Agents []DynamicSpawnArgs `json:"agents"`
}

// BatchSpawnResult mirrors SpawnResult but for the batch tool.
type BatchSpawnResult struct {
	WorkerIDs []string `json:"worker_ids"`
}

// RootDefaults is the fallback LLM the dynamic-spawn tool applies
// when args.LLM is unset. The App fills this from cfg.Root.LLM at
// startup so workers spawned without an explicit provider inherit
// the root's.
type RootDefaults struct {
	Provider string
	Model    string
}

// buildSpawnDynamic returns the spawn_dynamic_agent ADK tool. It
// closes over the Universe so the description string always reflects
// what baifo actually knows (skills, MCPs, providers, secrets).
func (t *Tools) buildSpawnDynamic() (tool.Tool, error) {
	desc := composeDynamicDescription(t.Universe)
	return functiontool.New(
		functiontool.Config{Name: "spawn_dynamic_agent", Description: desc},
		func(ctx agent.Context, a DynamicSpawnArgs) (SpawnResult, error) {
			spec, err := t.buildDynamicSpec(a)
			if err != nil {
				return SpawnResult{}, err
			}
			w, err := t.Manager.Spawn(ctx, spec)
			if err != nil {
				return SpawnResult{}, err
			}
			return SpawnResult{WorkerID: w.ID()}, nil
		},
	)
}

// buildSpawnDynamicBatch returns the spawn_dynamic_agents ADK tool.
// Each spec in args.Agents is validated and spawned in sequence; the
// first failure aborts the whole batch and surfaces the error to the
// LLM so it can retry with a corrected spec.
func (t *Tools) buildSpawnDynamicBatch() (tool.Tool, error) {
	desc := "Spawn multiple dynamic workers in one LLM turn. " +
		"Equivalent to calling spawn_dynamic_agent N times sequentially; " +
		"if any spec fails validation the whole batch is rejected."
	return functiontool.New(
		functiontool.Config{Name: "spawn_dynamic_agents", Description: desc},
		func(ctx agent.Context, a BatchSpawnArgs) (BatchSpawnResult, error) {
			ids := make([]string, 0, len(a.Agents))
			for i, spawn := range a.Agents {
				spec, err := t.buildDynamicSpec(spawn)
				if err != nil {
					return BatchSpawnResult{}, fmt.Errorf("agents[%d]: %w", i, err)
				}
				w, err := t.Manager.Spawn(ctx, spec)
				if err != nil {
					return BatchSpawnResult{}, fmt.Errorf("agents[%d]: %w", i, err)
				}
				ids = append(ids, w.ID())
			}
			return BatchSpawnResult{WorkerIDs: ids}, nil
		},
	)
}

// buildDynamicSpec validates args against the universe (decision #7)
// and turns them into a workers.Spec. Returns a descriptive error
// when any referenced piece is unknown.
func (t *Tools) buildDynamicSpec(a DynamicSpawnArgs) (workers.Spec, error) {
	if a.Name == "" {
		return workers.Spec{}, fmt.Errorf("name is required")
	}
	if a.Prompt == "" {
		return workers.Spec{}, fmt.Errorf("prompt is required")
	}

	if err := validateSubset(t.Universe.ListSkills(), a.Skills, "skill"); err != nil {
		return workers.Spec{}, err
	}
	if err := validateSubset(t.Universe.ListMCPs(), a.MCPs, "mcp"); err != nil {
		return workers.Spec{}, err
	}
	// Secrets: every requested name must (a) exist in the global
	// universe and (b) be in the spawning agent's own allowlist —
	// so a worker cannot dereference a secret the parent itself
	// could not. When ParentAllowedSecrets is nil the parent is
	// sovereign (today: the root); the global-universe check from
	// (a) is enough.
	if err := validateSubset(t.Universe.ListSecretNames(), a.AllowedSecrets, "secret"); err != nil {
		return workers.Spec{}, err
	}
	if err := validateSubsetOfParent(a.AllowedSecrets, t.ParentAllowedSecrets, "allowed_secrets"); err != nil {
		return workers.Spec{}, err
	}

	provider := a.LLM.Provider
	model := a.LLM.Model
	if provider == "" {
		provider = t.RootDefaults.Provider
	}
	if model == "" {
		model = t.RootDefaults.Model
	}
	if provider == "" || model == "" {
		return workers.Spec{}, fmt.Errorf("provider and model are required (no root default available)")
	}
	if !contains(t.Universe.ListProviders(), provider) {
		return workers.Spec{}, fmt.Errorf("unknown provider %q (known: %s)", provider,
			strings.Join(t.Universe.ListProviders(), ", "))
	}
	if !baifoagent.ValidReasoning(a.LLM.Reasoning) {
		return workers.Spec{}, fmt.Errorf("invalid reasoning %q (use one of: off, minimal, low, medium, high)", a.LLM.Reasoning)
	}

	return workers.Spec{
		Kind:           workers.KindDynamic,
		Name:           a.Name,
		Description:    a.Description,
		Prompt:         a.Prompt,
		Provider:       provider,
		Model:          model,
		Reasoning:      baifoagent.NormalizeReasoning(a.LLM.Reasoning),
		Skills:         a.Skills,
		MCPs:           a.MCPs,
		AllowedSecrets: a.AllowedSecrets,
		InitialMessage: a.InitialMessage,
	}, nil
}

// validateSubset returns an error if any element of want is not in
// universe. kind is used to make the error message specific.
func validateSubset(universe, want []string, kind string) error {
	if len(want) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(universe))
	for _, u := range universe {
		set[u] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return fmt.Errorf("unknown %s %q (known: %s)", kind, w, strings.Join(universe, ", "))
		}
	}
	return nil
}

// contains is a trivial slice-contains; kept private to avoid pulling
// slices.Contains from the stdlib for one call site (Go 1.21+ would
// be fine but we keep this package import-light).
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// composeDynamicDescription builds the spawn_dynamic_agent tool docs.
// It embeds the current universe so the LLM sees exactly what is
// available and does not invent names.
func composeDynamicDescription(u Universe) string {
	var b strings.Builder
	b.WriteString("Spawn a worker built at runtime from the pieces baifo knows. ")
	b.WriteString("Returns the worker_id immediately; use query_agent / collect_agent to interact with it.\n\n")
	b.WriteString("Validation rules: every name in `skills`, `mcps` and `allowed_secrets` ")
	b.WriteString("must already exist in baifo. The root cannot fabricate a new skill body, MCP endpoint ")
	b.WriteString("or secret inside a spawn call. Everything else (prompt, description, ")
	b.WriteString("provider/model) is yours to compose.\n\n")
	b.WriteString("Optional `llm.reasoning` (minimal | low | medium | high) sets how hard the worker ")
	b.WriteString("thinks; only valid for models whose list_models entry reports reasoning_levels. ")
	b.WriteString("Omit it to use the model's default.\n\n")
	writeNamedDescriptions(&b, "Available skills", u.SpawnSkillDetails())
	writeMCPs(&b, u)
	writeBullet(&b, "Available providers", u.ListProviders())
	b.WriteString("  (call list_models to see each provider's models with their size, " +
		"context window, cost and reasoning support, so you can pick a smaller or larger " +
		"model per worker and dial its reasoning effort)\n")
	writeNamedDescriptions(&b, "Available secrets (referenced by name in allowed_secrets)", u.SecretDetails())
	return b.String()
}

// writeNamedDescriptions renders one item per line as "name: description"
// so the LLM can see what each skill is for / what each secret holds
// without a second tool call. Items with no description render just
// the name; an empty slice renders "(none)" to make the absence
// explicit (otherwise the model may assume omission).
func writeNamedDescriptions(b *strings.Builder, label string, items []NamedDescription) {
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(":")
	if len(items) == 0 {
		b.WriteString(" (none)\n")
		return
	}
	b.WriteByte('\n')
	cp := append([]NamedDescription{}, items...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Name < cp[j].Name })
	for _, it := range cp {
		b.WriteString("  - ")
		b.WriteString(it.Name)
		if d := strings.TrimSpace(it.Description); d != "" {
			b.WriteString(": ")
			b.WriteString(d)
		}
		b.WriteByte('\n')
	}
}

// writeMCPs lists every MCP with the tools it actually exposes. Done
// inline instead of via writeBullet because the LLM otherwise
// hallucinates a worker's capabilities from the MCP name alone (the
// canonical case: "filesystem" looks read/write-only but ships with
// `exec`, `process_status`, `process_kill`, etc.).
func writeMCPs(b *strings.Builder, u Universe) {
	mcps := append([]string{}, u.ListMCPs()...)
	sort.Strings(mcps)
	b.WriteString("- Available mcps: ")
	if len(mcps) == 0 {
		b.WriteString("(none)\n")
		return
	}
	b.WriteByte('\n')
	for _, name := range mcps {
		tools := append([]string{}, u.MCPTools(name)...)
		sort.Strings(tools)
		b.WriteString("  - ")
		b.WriteString(name)
		if len(tools) == 0 {
			// An external MCP (http/stdio) connects lazily, so its
			// tools are unknown here — say so rather than implying it
			// exposes nothing. A builtin with a genuinely empty list
			// is the only real "advertises nothing" case.
			if u.MCPExternal(name) {
				b.WriteString(": (external MCP — tools load when a worker uses it; run /mcps test " + name + " to list them)\n")
			} else {
				b.WriteString(": (no tools advertised)\n")
			}
			continue
		}
		b.WriteString(": ")
		b.WriteString(strings.Join(tools, ", "))
		b.WriteByte('\n')
	}
}

// writeBullet appends a labelled, sorted, comma-separated list to b.
// An empty values slice still emits the label with "(none)" so the
// LLM cannot assume the absence is an omission.
func writeBullet(b *strings.Builder, label string, values []string) {
	cp := append([]string{}, values...)
	sort.Strings(cp)
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(": ")
	if len(cp) == 0 {
		b.WriteString("(none)")
	} else {
		b.WriteString(strings.Join(cp, ", "))
	}
	b.WriteByte('\n')
}

// Silence the unused-import detector when the file is in flight
// during transitional edits.
var _ = context.Background
