// SPDX-License-Identifier: Apache-2.0

// Package gemini adapts the upstream google.golang.org/adk Gemini model
// into baifo's provider registry. Unlike openai and anthropic, the
// underlying client lives in ADK proper rather than in adk-utils-go,
// because Google maintains it directly.
package gemini

import (
	"context"
	"fmt"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/providers"
)

const providerType = "gemini"

func init() {
	providers.Register(providerType, build)
}

// build constructs the Gemini model. ModelOptions (reasoning budget) is
// ignored here: Gemini takes reasoning request-side from
// GenerateContentConfig.ThinkingConfig (forwarded verbatim to genai by
// the ADK gemini model), which the agent builder sets.
func build(ctx context.Context, spec providers.Spec, modelName string, _ providers.ModelOptions) (model.LLM, error) {
	m, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
		APIKey: spec.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini model: %w", err)
	}
	return providers.WrapStripThoughts(m), nil
}
