// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package openai adapts the adk-utils-go OpenAI model into baifo's
// provider registry. The same underlying client is reused for the three
// flavours that speak the OpenAI API: openai, openai-compatible and
// ollama (each registered as a separate provider type so users see them
// labelled accurately in baifo.yaml).
package openai

import (
	"context"
	"net/http"

	utilsopenai "github.com/achetronic/adk-utils-go/genai/openai"
	"google.golang.org/adk/model"

	"github.com/achetronic/baifo/internal/providers"
)

// Provider types registered by this package.
const (
	TypeOpenAI           = "openai"
	TypeOpenAICompatible = "openai-compatible"
	TypeOllama           = "ollama"
)

func init() {
	providers.Register(TypeOpenAI, build)
	providers.Register(TypeOpenAICompatible, build)
	providers.Register(TypeOllama, build)
}

// build constructs the OpenAI-flavoured model. ModelOptions (reasoning
// budget) is ignored here: the openai adapter takes reasoning effort
// request-side from GenerateContentConfig.ThinkingConfig, which the
// agent builder sets — there is no construction-time reasoning knob.
func build(_ context.Context, spec providers.Spec, modelName string, _ providers.ModelOptions) (model.LLM, error) {
	headers := http.Header{}
	for k, v := range spec.Headers {
		headers.Set(k, v)
	}
	m := utilsopenai.New(utilsopenai.Config{
		APIKey:    spec.APIKey,
		BaseURL:   spec.URL,
		ModelName: modelName,
		HTTPOptions: utilsopenai.HTTPOptions{
			Headers: headers,
		},
	})
	return providers.WrapStripThoughts(m), nil
}
