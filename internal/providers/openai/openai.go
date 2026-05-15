// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package openai adapts the adk-utils-go OpenAI model into baifo's
// provider registry. Any endpoint that speaks the OpenAI API (a local
// Ollama server, OpenRouter, vLLM, LocalAI, ...) uses this same adapter:
// declare it as type: openai and point url at the endpoint.
package openai

import (
	"context"
	"net/http"

	utilsopenai "github.com/achetronic/adk-utils-go/genai/openai"
	"google.golang.org/adk/model"

	"github.com/achetronic/baifo/internal/providers"
)

// TypeOpenAI is the provider type registered by this package. Any
// OpenAI-compatible endpoint uses it with a custom url.
const TypeOpenAI = "openai"

func init() {
	providers.Register(TypeOpenAI, build)
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
