// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/config/yamledit"
	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/scaffolds"
)

// agentsSectionKey is the top-level YAML key under which agent
// templates live in agents.yaml. Hoisted here so the file is the
// single source of truth for the section name.
const agentsSectionKey = "agents"

// AgentYAML implements facade.Facade. Reads agents.yaml from disk and
// returns the exact YAML chunk of the named agent, comments and
// all. Falls back to a struct-reconstructed view if the entry exists
// in memory but not on disk (rare — only happens when the user just
// upserted via a different path and disk hasn't been re-read).
func (a *App) AgentYAML(name string) (string, error) {
	root, err := yamledit.LoadFile(config.AgentsFilePath(a.configDir))
	if err != nil {
		return "", fmt.Errorf("load yaml: %w", err)
	}
	if node := yamledit.FindEntry(root, agentsSectionKey, name); node != nil {
		data, err := yamlMarshal(node)
		if err != nil {
			return "", fmt.Errorf("marshal: %w", err)
		}
		return data, nil
	}

	// Fallback to the in-memory index.
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.agentTmpl == nil {
		return "", fmt.Errorf("agent templates not loaded")
	}
	if r := a.agentTmpl.root; r != nil && r.Name == name {
		node := yamledit.BuildAgentEntry(*r)
		data, err := yamlMarshal(node)
		if err != nil {
			return "", fmt.Errorf("marshal: %w", err)
		}
		return data, nil
	}
	tmpl, ok := a.agentTmpl.byName[name]
	if !ok {
		return "", fmt.Errorf("agent %q not found", name)
	}
	node := yamledit.BuildAgentEntry(tmpl)
	data, err := yamlMarshal(node)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return data, nil
}

// AgentScaffold implements facade.Facade by delegating to the package-level
// scaffold function. Kept as a method for facade.Facade conformance.
func (a *App) AgentScaffold(suggestedName string) string {
	return scaffolds.Agent(suggestedName)
}

// UpsertAgent implements facade.Facade. Parses the buffer as an
// AgentTemplate, validates it against the existing schema rules in
// config.validateAgents (via a thin wrapper here), and writes through
// yamledit so the rest of agents.yaml keeps its comments and order.
//
// On success we trigger a reload so the worker manager picks up the
// new template definition (workers spawned BEFORE the change are not
// re-templated; that's a deliberate keep-it-simple choice).
//
// Root-flag invariants enforced here:
//   - You cannot delete the root by dropping its flag: if the on-disk
//     entry was the root, the incoming buffer must keep root: true.
//   - You cannot add a second root by flagging a different name: at
//     most one entry may carry root: true at any time.
func (a *App) UpsertAgent(ctx context.Context, yamlText string) error {
	var tmpl config.AgentTemplate
	if err := yamlUnmarshal(yamlText, &tmpl); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if tmpl.Name == "" {
		return fmt.Errorf("missing name")
	}
	if err := validateAgentTemplate(tmpl); err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	// Root-flag invariants. We consult the in-memory index because
	// it already reflects the last validated load — the disk file
	// may be mid-edit when the user types something invalid.
	a.mu.RLock()
	existingRoot := a.rootTemplate()
	var existingUtility *config.AgentTemplate
	if a.agentTmpl != nil {
		existingUtility = a.agentTmpl.UtilityAgent()
	}
	a.mu.RUnlock()
	if existingRoot != nil {
		if existingRoot.Name == tmpl.Name && !tmpl.Root {
			return fmt.Errorf("the root agent %q cannot have root: true removed; "+
				"edit a different field or delete the entry instead", tmpl.Name)
		}
		if existingRoot.Name != tmpl.Name && tmpl.Root {
			return fmt.Errorf("a second entry cannot set root: true; "+
				"%q is already the root", existingRoot.Name)
		}
	}
	// Utility-flag invariant: at most one entry may carry it. Unlike
	// the root, removing the flag is allowed (baifo falls back to the
	// root LLM for chores), so we only guard against duplicates.
	if existingUtility != nil && existingUtility.Name != tmpl.Name && tmpl.Utility {
		return fmt.Errorf("a second entry cannot set utility: true; "+
			"%q is already the utility agent", existingUtility.Name)
	}

	// Re-parse to a *yaml.Node so the user's comments survive the
	// write. Same pattern as MCPs.
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return fmt.Errorf("parse node: %w", err)
	}
	node := mappingOfDoc(&doc)
	if node == nil {
		return fmt.Errorf("expected a mapping at the top of the buffer")
	}

	path := filepath.Join(a.configDir, config.AgentsFileName)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load agents.yaml: %w", err)
	}
	if err := yamledit.UpsertEntry(root, agentsSectionKey, tmpl.Name, node); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save agents.yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// DeleteAgent implements facade.Facade. Removes the named entry from
// agents.yaml and triggers a reload.
//
// Refuses to delete the root entry. The user is meant to either
// edit it in place (provider, prompt, ...) or, if they really want
// a different root, first flag a new agent and demote the old one
// — but until the root-swap UX is built, the simpler invariant
// ("you can't leave baifo without a root") is the safer one.
func (a *App) DeleteAgent(ctx context.Context, name string) error {
	a.mu.RLock()
	existingRoot := a.rootTemplate()
	a.mu.RUnlock()
	if existingRoot != nil && existingRoot.Name == name {
		return fmt.Errorf("the root agent %q cannot be deleted; "+
			"edit it instead, or swap roots once a /agent swap-root command lands", name)
	}

	path := filepath.Join(a.configDir, config.AgentsFileName)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load agents.yaml: %w", err)
	}
	if err := yamledit.DeleteEntry(root, agentsSectionKey, name); err != nil {
		return err
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save agents.yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// SetRootAgent implements facade.Facade. Promotes the named agent to
// be the root: it sets root: true on that entry and strips the flag
// from whatever entry held it before, writing through yamledit so the
// rest of agents.yaml keeps its comments and order. A reload follows,
// which rebuilds the live root agent from the new on-disk definition.
//
// This is the persistent counterpart to the TUI's `/root` (which only
// switches the chat view). Guards:
//   - The target must exist in agents.yaml.
//   - Promoting the current root is a friendly no-op (no write, no
//     reload) so repeated invocations don't churn the file.
func (a *App) SetRootAgent(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("missing name")
	}

	a.mu.RLock()
	existingRoot := a.rootTemplate()
	a.mu.RUnlock()
	if existingRoot != nil && existingRoot.Name == name {
		return fmt.Errorf("%q is already the root", name)
	}

	path := filepath.Join(a.configDir, config.AgentsFileName)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load agents.yaml: %w", err)
	}
	if err := yamledit.SetExclusiveBool(root, agentsSectionKey, name, "root"); err != nil {
		if err == yamledit.ErrNotFound {
			return fmt.Errorf("agent %q not found", name)
		}
		return fmt.Errorf("set root: %w", err)
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save agents.yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// AgentDetails implements facade.Facade. Returns a one-line summary
// per agent. We include both the root and the sub-agents; the root
// is annotated in the Description prefix so the Settings overlay
// can distinguish it without needing a separate field on the
// facade DTO.
func (a *App) AgentDetails() []facade.AgentDetail {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.agentTmpl == nil {
		return nil
	}
	out := make([]facade.AgentDetail, 0, len(a.agentTmpl.byName)+2)
	if root := a.agentTmpl.root; root != nil {
		summary := firstLine(root.Description)
		if summary == "" {
			summary = firstLine(root.Prompt)
		}
		out = append(out, facade.AgentDetail{
			Name:        root.Name,
			Description: "(root) " + summary,
			Provider:    root.LLM.Effective(),
			Model:       root.LLM.Model,
		})
	}
	if util := a.agentTmpl.utility; util != nil {
		summary := firstLine(util.Description)
		if summary == "" {
			summary = firstLine(util.Prompt)
		}
		out = append(out, facade.AgentDetail{
			Name:        util.Name,
			Description: "(utility) " + summary,
			Provider:    util.LLM.Effective(),
			Model:       util.LLM.Model,
		})
	}
	for _, t := range a.agentTmpl.byName {
		summary := firstLine(t.Description)
		if summary == "" {
			summary = firstLine(t.Prompt)
		}
		out = append(out, facade.AgentDetail{
			Name:        t.Name,
			Description: summary,
			Provider:    t.LLM.Effective(),
			Model:       t.LLM.Model,
		})
	}
	return out
}

// validateAgentTemplate runs a single template through the existing
// validateAgents helper inside the config package. We have to wrap
// in a one-element slice because validateAgents is the public entry
// point and it walks a list.
//
// Re-using that function means baifo's schema rules live in one
// place; the editor's validator and the boot-time parser agree by
// construction.
func validateAgentTemplate(t config.AgentTemplate) error {
	// LoadAgents bundles validation with file I/O, so we use a thin
	// reflection-free dance: marshal the template into a temp YAML
	// document, hand it to LoadAgents via a memory file. Cheaper
	// alternative: re-export validateAgents from the config package.
	// We picked the export route; see config.ValidateAgents below.
	return config.ValidateAgents([]config.AgentTemplate{t})
}
