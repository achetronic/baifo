// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/config/yamledit"
	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/providers"
	"github.com/achetronic/baifo/internal/scaffolds"
)

// providersSectionKey is the top-level YAML key under which provider
// entries live in baifo.yaml. Mirrors agentsSectionKey / mcps key
// patterns and the single source of truth for the section name.
const providersSectionKey = "providers"

// ProviderYAML implements facade.Facade. Reads baifo.yaml from disk and
// returns the exact YAML chunk for the named provider, comments
// preserved. Falls back to a struct-reconstructed view when the
// entry exists in memory but not on disk (rare, only after a
// pre-disk in-memory upsert).
func (a *App) ProviderYAML(name string) (string, error) {
	root, err := yamledit.LoadFile(config.FilePath(a.configDir))
	if err != nil {
		return "", fmt.Errorf("load yaml: %w", err)
	}
	if node := yamledit.FindEntry(root, providersSectionKey, name); node != nil {
		data, err := yamlMarshal(node)
		if err != nil {
			return "", fmt.Errorf("marshal: %w", err)
		}
		return data, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg == nil {
		return "", fmt.Errorf("no config loaded")
	}
	for _, p := range a.cfg.Providers {
		if p.Name == name {
			node := yamledit.BuildProviderEntry(p)
			data, err := yamlMarshal(node)
			if err != nil {
				return "", fmt.Errorf("marshal: %w", err)
			}
			return data, nil
		}
	}
	return "", fmt.Errorf("provider %q not found", name)
}

// ProviderScaffold implements facade.Facade by delegating to the package-
// level scaffold function. Method on App for interface conformance.
func (a *App) ProviderScaffold(suggestedName string) string {
	return scaffolds.Provider(suggestedName)
}

// UpsertProvider implements facade.Facade. Parses the buffer as a
// ProviderEntry, validates it through providers.NewRegistry, then
// writes through yamledit so comments and unrelated keys in
// baifo.yaml survive intact.
//
// Validation goes through the same registry constructor the boot
// path uses, so a provider that passes here is guaranteed to pass
// on next start \u2014 no surprise at reload time.
func (a *App) UpsertProvider(ctx context.Context, yamlText string) error {
	var entry config.ProviderEntry
	if err := yamlUnmarshal(yamlText, &entry); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if entry.Name == "" {
		return fmt.Errorf("missing name")
	}
	// Validate the single entry by running it through a one-off
	// registry. Same trick MCPs use.
	if _, err := providers.NewRegistry([]config.ProviderEntry{entry}); err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	// Re-parse for comment-preserving write.
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return fmt.Errorf("parse node: %w", err)
	}
	node := mappingOfDoc(&doc)
	if node == nil {
		return fmt.Errorf("expected a mapping at the top of the buffer")
	}

	path := config.FilePath(a.configDir)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load yaml: %w", err)
	}
	if err := yamledit.UpsertEntry(root, providersSectionKey, entry.Name, node); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// DeleteProvider implements facade.Facade.
func (a *App) DeleteProvider(ctx context.Context, name string) error {
	path := config.FilePath(a.configDir)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load yaml: %w", err)
	}
	if err := yamledit.DeleteEntry(root, providersSectionKey, name); err != nil {
		return err
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// ProviderDetails implements facade.Facade. Returns a public, lightweight
// view per provider for /provider list and the Settings overlay.
// The API key is NEVER returned \u2014 only its presence is signalled.
func (a *App) ProviderDetails() []facade.ProviderDetail {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg == nil {
		return nil
	}
	out := make([]facade.ProviderDetail, 0, len(a.cfg.Providers))
	for _, p := range a.cfg.Providers {
		out = append(out, facade.ProviderDetail{
			Name:   p.Name,
			Type:   p.Type,
			URL:    p.URL,
			HasKey: p.APIKey != "",
		})
	}
	return out
}
