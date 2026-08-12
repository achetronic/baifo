// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package providers wires LLM providers declared in baifo.yaml into ready
// to use ADK models.
//
// The package exposes a Registry that owns every configured provider and
// returns model.LLM instances on demand. Each concrete provider lives in
// its own subpackage (openai, anthropic, gemini, ...).
package providers

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/adk/v2/model"

	"github.com/achetronic/baifo/internal/config"
)

// Spec is the minimal description of a provider derived from
// config.ProviderEntry. Validation (required fields, supported type)
// lives in the provider implementation, not in the loader.
type Spec struct {
	Name    string
	Type    string
	URL     string
	Auth    string
	APIKey  string
	Headers map[string]string

	// ConfigDir is the path to the baifo configuration directory. It is
	// injected at Registry creation time so providers can locate auxiliary
	// files (like OAuth tokens). Empty when not provided.
	ConfigDir string

	// Streaming is the resolved streaming setting for this provider
	// (default ON). When false, agents backed by this provider run in
	// non-streaming mode for endpoints that do not support SSE.
	Streaming bool
}

// fromEntry converts a config.ProviderEntry into a Spec, decoupling the
// providers package from the on-disk format.
func fromEntry(e config.ProviderEntry) Spec {
	return Spec{
		Name:      e.Name,
		Type:      e.Type,
		URL:       e.URL,
		Auth:      e.Auth,
		APIKey:    e.APIKey,
		Headers:   e.Headers,
		Streaming: e.StreamingEnabled(),
	}
}

// Builder constructs a model.LLM from a Spec, a model name and the
// per-build ModelOptions. Each provider type (openai, anthropic,
// gemini, ...) registers its Builder via Register so the Registry can
// dispatch by Spec.Type.
type Builder func(ctx context.Context, spec Spec, modelName string, opts ModelOptions) (model.LLM, error)

// ModelOptions carries per-build knobs that are NOT part of the static
// provider Spec; they vary per agent, not per configured provider.
// Today the only knob is reasoning, and only the anthropic adapter
// consumes it (its extended-thinking budget is a construction-time
// setting). The openai and gemini adapters take reasoning request-side
// via GenerateContentConfig instead, so they ignore these fields.
type ModelOptions struct {
	// ThinkingBudgetTokens, when > 0, asks the provider to enable
	// extended reasoning with this many output tokens reserved for the
	// model's internal thinking. Anthropic requires it >= 1024 and
	// strictly less than the response's max output tokens.
	ThinkingBudgetTokens int

	// ThinkingEffort, when non-empty, sets the reasoning effort level
	// for models that use the newer effort-based reasoning API
	// (e.g. Anthropic Claude Opus 4.5+).
	ThinkingEffort string

	// ThinkingMode selects which Anthropic reasoning API shape to use:
	// "enabled" (classic budget-based, for Claude 3.7 / Sonnet 4 / Opus 4)
	// or "adaptive" (effort-based, for Opus 4.5+). Empty lets the adapter
	// deduce it from the other fields. Only the anthropic adapter reads it.
	ThinkingMode string

	// MaxOutputTokens, when > 0, sets the response token ceiling. The
	// anthropic adapter needs this to exceed ThinkingBudgetTokens; the
	// builder sets it accordingly when a budget is requested.
	MaxOutputTokens int
}

// builders holds the registered providers. Populated by Register calls
// in the init() of each provider subpackage. Reads happen exclusively
// after init, so no synchronisation is needed.
var builders = map[string]Builder{}

// Register associates a provider type with its Builder. Called from the
// init() of each provider subpackage. Panics on duplicate registration
// to surface conflicts at startup rather than at runtime.
func Register(providerType string, b Builder) {
	if _, ok := builders[providerType]; ok {
		panic(fmt.Sprintf("providers: duplicate registration for %q", providerType))
	}
	builders[providerType] = b
}

// AuthFlow runs an interactive login for a provider type and persists
// the resulting credentials for the provider named name under dir.
type AuthFlow func(dir, name string) error

// authFlows holds the provider types that support an interactive auth
// flow. Populated by RegisterAuthFlow calls in the init() of each
// provider subpackage, same lifecycle as builders.
var authFlows = map[string]AuthFlow{}

// RegisterAuthFlow associates a provider type with its interactive
// login. Panics on duplicate registration, mirroring Register.
func RegisterAuthFlow(providerType string, f AuthFlow) {
	if _, ok := authFlows[providerType]; ok {
		panic(fmt.Sprintf("providers: duplicate auth flow registration for %q", providerType))
	}
	authFlows[providerType] = f
}

// AuthFlowFor returns the auth flow registered for a provider type, or
// false when the type has none.
func AuthFlowFor(providerType string) (AuthFlow, bool) {
	f, ok := authFlows[providerType]
	return f, ok
}

// Reset clears every registration. Intended for tests that need to
// install a fake builder without colliding with init() registrations
// from other packages. NOT safe for production code paths.
func Reset() {
	builders = map[string]Builder{}
}

// SupportedTypes returns the list of provider types that have a Builder
// registered. Useful for diagnostics and for the TUI /providers command.
func SupportedTypes() []string {
	out := make([]string, 0, len(builders))
	for k := range builders {
		out = append(out, k)
	}
	return out
}

// ErrUnknownProvider is returned when a config references a provider
// name that was not declared in providers[].
var ErrUnknownProvider = errors.New("unknown provider")

// ErrUnsupportedType is returned when a provider entry uses a type that
// has no Builder registered.
var ErrUnsupportedType = errors.New("unsupported provider type")

// ErrUnsupportedAuth is returned when a provider entry sets auth to a
// mode its type cannot honour, e.g. auth: oauth on a type with no
// registered OAuth flow (only anthropic has one today). Failing at load
// time avoids the silent trap where a provider ignores the field and
// the user believes OAuth is active when it is not.
var ErrUnsupportedAuth = errors.New("unsupported auth mode for provider type")
