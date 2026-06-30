// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// AgentsFileName is the canonical name of the static-worker templates
// file inside .baifo/.
const AgentsFileName = "agents.yaml"

// AgentsFile is the root of agents.yaml.
type AgentsFile struct {
	Version int             `yaml:"version"`
	Agents  []AgentTemplate `yaml:"agents"`
}

// AgentTemplate is the on-disk definition of one agent in
// agents.yaml. Both the root agent (the one the user talks to in
// the TUI / over A2A) and the spawnable sub-agents share this
// shape. The single distinguishing flag is Root.
//
// Field names follow CONFIG.md > agents.yaml. The LLM section uses
// `provider` (the baifo term, see Phase 2).
type AgentTemplate struct {
	// Root marks the always-on entry-point agent. Exactly one
	// entry in agents.yaml must set this to true; the loader
	// rejects zero and more-than-one root setups. The flagged
	// agent receives the spawn / memory / todos tools and a
	// secrets allower of AllowAll regardless of AllowedSecrets;
	// all other entries are treated as ordinary spawnable
	// sub-agents.
	Root bool `yaml:"root,omitempty"`

	// Utility marks the background utility agent: the cheap model
	// baifo uses for internal chores that don't deserve the root's
	// (usually expensive) model — today, titling sessions and
	// summarising conversations for context-window compaction. At
	// most one entry may set it; when none does, those chores fall
	// back to the root's LLM. The utility agent never chats, never
	// receives tools and is not spawnable — only its llm block is
	// consumed.
	Utility bool `yaml:"utility,omitempty"`

	Name           string             `yaml:"name"`
	Description    string             `yaml:"description"`
	Prompt         string             `yaml:"prompt"`
	LLM            AgentLLM           `yaml:"llm"`
	Skills         []string           `yaml:"skills"`
	MCPs           []string           `yaml:"mcps"`
	ContextGuard   ContextGuardConfig `yaml:"context_guard"`
	AllowedSecrets []string           `yaml:"allowed_secrets"`
}

// AgentLLM mirrors LLMRef and defines the provider and model settings.
// The effective value is exposed via the Provider accessor.
type AgentLLM struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`

	// Reasoning is the optional reasoning-effort knob for models that
	// support it: one of "minimal" / "low" / "medium" / "high" (empty
	// or "off" leaves the model's own default). Use list_models to see
	// which models accept reasoning and at which levels. Applied to
	// openai (reasoning_effort), gemini (thinking) and anthropic
	// (extended-thinking budget).
	Reasoning string `yaml:"reasoning"`

	// ReasoningAPI optionally overrides which Anthropic reasoning API the
	// model uses: "enabled" (classic budget-based, for Claude 3.7 /
	// Sonnet 4 / Opus 4) or "adaptive" (effort-based, for Opus 4.5+).
	// Empty auto-detects from the catalogue. Only anthropic uses it;
	// other providers ignore it.
	// Ref: https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking
	ReasoningAPI string `yaml:"reasoning_api"`
}

// Effective returns the provider name.
func (l AgentLLM) Effective() string {
	return l.Provider
}

// AgentsFilePath returns the absolute path of agents.yaml inside dir.
func AgentsFilePath(dir string) string {
	return filepath.Join(dir, AgentsFileName)
}

// LoadAgents reads and parses agents.yaml from path. Environment
// variables are expanded before parsing. A missing file yields an
// empty list (not an error) so installations without static workers
// boot cleanly.
func LoadAgents(path string) (*AgentsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AgentsFile{Version: 1}, nil
		}
		return nil, fmt.Errorf("read agents file: %w", err)
	}

	expanded := []byte(expandEnvPreservingSecrets(string(data)))

	var f AgentsFile
	if err := yaml.Unmarshal(expanded, &f); err != nil {
		return nil, fmt.Errorf("parse agents file: %w", err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	if err := validateAgents(f.Agents); err != nil {
		return nil, err
	}
	return &f, nil
}

// ValidateAgents is the exported wrapper around validateAgents so
// other packages (notably internal/app) can run the same schema
// rules without going through LoadAgents and disk I/O.
func ValidateAgents(agents []AgentTemplate) error {
	return validateAgents(agents)
}

// validateAgents enforces uniqueness of names, presence of fields
// every agent needs, and the at-most-one-root invariant.
//
// We do NOT require at-least-one-root here: a fresh install (before
// the wizard adds one) has an empty agents.yaml. App.buildRoot is the
// single place that turns "no root anywhere" into ErrNoRoot, which is
// the correct behaviour for the cold-start wizard path.
func validateAgents(agents []AgentTemplate) error {
	seen := make(map[string]struct{}, len(agents))
	roots := 0
	utilities := 0
	for i, a := range agents {
		if a.Name == "" {
			return fmt.Errorf("agents[%d]: missing name", i)
		}
		if _, dup := seen[a.Name]; dup {
			return fmt.Errorf("agents[%d]: duplicate name %q", i, a.Name)
		}
		seen[a.Name] = struct{}{}

		if a.Prompt == "" {
			return fmt.Errorf("agents[%d] (%s): missing prompt", i, a.Name)
		}
		// The root agent is allowed to ship with an empty llm.provider /
		// llm.model: the first-run wizard seeds a complete root (name,
		// prompt, tools) but leaves the model unchosen so the user picks
		// one via /agent edit. Such a root is "incomplete" — App.buildRoot
		// treats an empty LLM as ErrNoRoot (degraded boot) rather than a
		// fatal error — but it is still a VALID on-disk entry, so we must
		// not reject it here. Sub-agents, by contrast, are only ever added
		// once the user has chosen a model, so they keep the hard
		// requirement.
		if !a.Root && !a.Utility && (a.LLM.Effective() == "" || a.LLM.Model == "") {
			return fmt.Errorf("agents[%d] (%s): llm.provider and llm.model are required", i, a.Name)
		}
		if !validReasoning(a.LLM.Reasoning) {
			return fmt.Errorf("agents[%d] (%s): invalid llm.reasoning %q (use one of: off, minimal, low, medium, high)", i, a.Name, a.LLM.Reasoning)
		}
		if !validReasoningAPI(a.LLM.ReasoningAPI) {
			return fmt.Errorf("agents[%d] (%s): invalid llm.reasoning_api %q (use one of: enabled, adaptive, or leave empty to auto-detect)", i, a.Name, a.LLM.ReasoningAPI)
		}
		if a.Root {
			roots++
		}
		if a.Utility {
			utilities++
		}
	}
	if roots > 1 {
		return fmt.Errorf("agents.yaml: %d entries have root: true (at most one is allowed)", roots)
	}
	if utilities > 1 {
		return fmt.Errorf("agents.yaml: %d entries have utility: true (at most one is allowed)", utilities)
	}
	return nil
}

// validReasoning reports whether the YAML llm.reasoning value is one of
// the accepted effort levels. Empty / "off" / "none" mean "model
// default" and are valid. Kept as a local check (rather than importing
// internal/agent) so the config package stays dependency-free; the
// canonical mapping lives in internal/agent.NormalizeReasoning and the
// two lists must stay in sync.
func validReasoning(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off", "none", "disabled", "minimal", "low", "medium", "high":
		return true
	default:
		return false
	}
}

// validReasoningAPI reports whether the YAML llm.reasoning_api value is
// accepted. Empty means "auto-detect" and is valid. Kept local (rather
// than importing internal/agent) so the config package stays
// dependency-free; the canonical values live in
// internal/agent.NormalizeReasoningAPI and the two must stay in sync.
func validReasoningAPI(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "enabled", "adaptive":
		return true
	default:
		return false
	}
}

// RootAgent returns a pointer to the entry flagged with root: true,
// or nil if the file has no agents at all (a fresh install before
// the wizard ran). Callers should treat a nil return as ErrNoRoot
// equivalent.
//
// The loader's exactly-one-root validation guarantees that when
// the slice is non-empty there is exactly one matching entry, so
// we can stop at the first hit.
func (f *AgentsFile) RootAgent() *AgentTemplate {
	if f == nil {
		return nil
	}
	for i := range f.Agents {
		if f.Agents[i].Root {
			return &f.Agents[i]
		}
	}
	return nil
}

// UtilityAgent returns a pointer to the entry flagged utility: true,
// or nil when no entry opts in. Callers must treat nil (and an entry
// with an unset llm) as "fall back to the root's LLM" — the utility
// agent is an optional cost optimisation, never a requirement.
func (f *AgentsFile) UtilityAgent() *AgentTemplate {
	if f == nil {
		return nil
	}
	for i := range f.Agents {
		if f.Agents[i].Utility {
			return &f.Agents[i]
		}
	}
	return nil
}

// SubAgents returns the entries that are NOT the root, in the same
// order they appear on disk. Useful for the spawn-tools machinery
// and for listing what the user can /agents talk to.
func (f *AgentsFile) SubAgents() []AgentTemplate {
	if f == nil {
		return nil
	}
	out := make([]AgentTemplate, 0, len(f.Agents))
	for _, a := range f.Agents {
		if !a.Root {
			out = append(out, a)
		}
	}
	return out
}
