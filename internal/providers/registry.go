// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package providers

import (
	"context"
	"fmt"

	"google.golang.org/adk/model"

	"github.com/achetronic/baifo/internal/config"
)

// Registry resolves provider names declared in baifo.yaml into ready
// model.LLM instances. It validates the configuration eagerly (so bad
// entries surface at startup) and caches built models per (name, modelID)
// pair to avoid repeated client construction.
type Registry struct {
	specs map[string]Spec
	cache map[cacheKey]model.LLM
	retry RetryPolicy
}

type cacheKey struct {
	name    string
	modelID string
	// thinkingBudget is part of the key because for constructor-level
	// reasoning providers (anthropic) two agents sharing a provider+model
	// but asking for different reasoning budgets need distinct clients.
	thinkingBudget int
}

// Option configures a Registry at construction. Kept variadic so the
// many existing NewRegistry call sites (including throw-away validation
// registries) need no changes when they don't care about retries.
type Option func(*Registry)

// WithRetry installs the retry policy applied to every model the
// Registry hands out. A disabled policy (MaxAttempts <= 1) is a no-op:
// models are returned unwrapped.
func WithRetry(p RetryPolicy) Option {
	return func(r *Registry) { r.retry = p }
}

// WithConfigDir sets the configuration directory used to locate
// auxiliary files (like OAuth tokens) for the providers.
func WithConfigDir(dir string) Option {
	return func(r *Registry) {
		for k, spec := range r.specs {
			spec.ConfigDir = dir
			r.specs[k] = spec
		}
	}
}

// NewRegistry builds a Registry from the providers section of the
// config. Each entry is validated: a non-empty Name and a Type that has
// a Builder registered. Optional Options configure cross-cutting
// behaviour such as the retry policy.
func NewRegistry(entries []config.ProviderEntry, opts ...Option) (*Registry, error) {
	specs := make(map[string]Spec, len(entries))
	for i, e := range entries {
		if e.Name == "" {
			return nil, fmt.Errorf("providers[%d]: missing name", i)
		}
		if _, ok := specs[e.Name]; ok {
			return nil, fmt.Errorf("providers[%d]: duplicate name %q", i, e.Name)
		}
		if _, ok := builders[e.Type]; !ok {
			return nil, fmt.Errorf("providers[%d] (%s): %w %q", i, e.Name, ErrUnsupportedType, e.Type)
		}
		// auth defaults to api_key. Any other mode must be backed by a
		// registered flow for the type; oauth on a type without one
		// (gemini, openai) is rejected here rather than silently ignored
		// by the builder.
		if e.Auth != "" && e.Auth != "api_key" {
			if _, ok := authFlows[e.Type]; !ok {
				return nil, fmt.Errorf("providers[%d] (%s): %w: %q has no %q flow", i, e.Name, ErrUnsupportedAuth, e.Type, e.Auth)
			}
		}
		specs[e.Name] = fromEntry(e)
	}
	r := &Registry{
		specs: specs,
		cache: make(map[cacheKey]model.LLM),
	}
	for _, o := range opts {
		o(r)
	}
	return r, nil
}

// Names returns the list of registered provider names, suitable for
// listing in the TUI.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.specs))
	for name := range r.specs {
		out = append(out, name)
	}
	return out
}

// StreamingEnabled reports whether agents backed by the named provider
// should run in streaming mode. Unknown providers default to true so a
// misconfigured reference never silently disables streaming (which
// Anthropic requires for long turns); the unknown-name error surfaces
// later at Model resolution.
func (r *Registry) StreamingEnabled(providerName string) bool {
	spec, ok := r.specs[providerName]
	if !ok {
		return true
	}
	return spec.Streaming
}

// Model returns (and caches) the model.LLM for the given provider name
// and model id. Optional ModelOptions carry per-agent knobs (today:
// reasoning budget) that some providers apply at construction time;
// they are folded into the cache key so distinct options yield distinct
// cached clients. The Builder of the provider type is invoked the first
// time; subsequent calls with the same arguments return the cached
// instance.
func (r *Registry) Model(ctx context.Context, providerName, modelID string, opts ...ModelOptions) (model.LLM, error) {
	if modelID == "" {
		return nil, fmt.Errorf("model id is required")
	}
	spec, ok := r.specs[providerName]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, providerName)
	}

	var mo ModelOptions
	if len(opts) > 0 {
		mo = opts[0]
	}

	key := cacheKey{name: providerName, modelID: modelID, thinkingBudget: mo.ThinkingBudgetTokens}
	if m, ok := r.cache[key]; ok {
		return m, nil
	}

	build := builders[spec.Type]
	m, err := build(ctx, spec, modelID, mo)
	if err != nil {
		return nil, fmt.Errorf("build provider %q: %w", providerName, err)
	}
	// Wrap with the retry policy (no-op when disabled) so every code
	// path that resolves a model gets the same backoff behaviour.
	m = withRetry(m, r.retry)
	r.cache[key] = m
	return m, nil
}
