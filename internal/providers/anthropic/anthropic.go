// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package anthropic adapts the adk-utils-go Anthropic model into baifo's
// provider registry.
package anthropic

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	utilsanthropic "github.com/achetronic/adk-utils-go/genai/anthropic"
	"google.golang.org/adk/model"

	"github.com/achetronic/baifo/internal/providers"
)

// providerType is the value of `providers[].type` in baifo.yaml that
// routes a provider to this adapter.
const providerType = "anthropic"

func init() {
	providers.Register(providerType, build)
	providers.RegisterAuthFlow(providerType, RunOAuthFlow)
}

func build(_ context.Context, spec providers.Spec, modelName string, opts providers.ModelOptions) (model.LLM, error) {
	headers := http.Header{}
	for k, v := range spec.Headers {
		headers.Set(k, v)
	}

	// apiKey is what we pass to the adk adapter. When OAuth is active it is
	// cleared so the adapter never emits an x-api-key header alongside the
	// Authorization: Bearer token (Anthropic rejects requests with both).
	apiKey := spec.APIKey

	var httpClient *http.Client
	if spec.Auth == "oauth" {
		if spec.ConfigDir == "" {
			return nil, fmt.Errorf("anthropic: auth is set to 'oauth' but ConfigDir is empty")
		}
		tokenFile := tokenFilePath(spec.ConfigDir, spec.Name)
		if _, err := os.Stat(tokenFile); err == nil {
			slog.Info("anthropic: using OAuth bearer token transport", "provider", spec.Name)
			httpClient = &http.Client{
				Transport: &OAuthTransport{TokenFile: tokenFile},
			}
			apiKey = "" // explicit override to prevent adk-utils-go from sending it
		} else {
			return nil, fmt.Errorf("anthropic: provider %q has auth 'oauth' but no token file. Run 'baifo provider auth %s' first", spec.Name, spec.Name)
		}
	} else if spec.Auth != "" && spec.Auth != "api_key" {
		return nil, fmt.Errorf("anthropic: unknown auth type %q", spec.Auth)
	}

	m := utilsanthropic.New(utilsanthropic.Config{
		APIKey:    apiKey,
		BaseURL:   spec.URL,
		ModelName: modelName,
		// Extended thinking is a construction-time setting for the
		// anthropic adapter (it does not read request-level
		// ThinkingConfig). When the agent requested reasoning the
		// builder passes a budget plus a max-output ceiling that
		// exceeds it, as the Anthropic API requires.
		ThinkingBudgetTokens: opts.ThinkingBudgetTokens,
		ThinkingEffort:       opts.ThinkingEffort,
		ThinkingMode:         opts.ThinkingMode,
		MaxOutputTokens:      opts.MaxOutputTokens,
		HTTPOptions: utilsanthropic.HTTPOptions{
			Client:  httpClient,
			Headers: headers,
		},
	})
	return providers.WrapStripThoughts(m), nil
}
