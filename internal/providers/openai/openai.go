// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package openai adapts the adk-utils-go OpenAI model into baifo's
// provider registry. Any endpoint that speaks the OpenAI API (a local
// Ollama server, OpenRouter, vLLM, LocalAI, ...) uses this same adapter:
// declare it as type: openai and point url at the endpoint.
package openai

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"

	utilsopenai "github.com/achetronic/adk-utils-go/genai/openai/completions"
	"google.golang.org/adk/v2/model"

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
// agent builder sets. The reasoning dialect is picked from the endpoint
// host, so each known gateway gets its own wire rules and unknown hosts
// keep the OpenAI-pure default.
func build(_ context.Context, spec providers.Spec, modelName string, _ providers.ModelOptions) (model.LLM, error) {
	headers := http.Header{}
	for k, v := range spec.Headers {
		headers.Set(k, v)
	}
	m := utilsopenai.New(utilsopenai.Config{
		APIKey:    spec.APIKey,
		BaseURL:   spec.URL,
		ModelName: modelName,
		Dialect:   dialectFor(spec.URL, spec.Name),
		HTTPOptions: utilsopenai.HTTPOptions{
			Headers: headers,
		},
	})
	return providers.WrapStripThoughts(m), nil
}

// dialectFor picks the reasoning dialect for a known endpoint host. The
// fallback is nil, the OpenAI-pure shape, which is also the correct
// behaviour for compatible servers that follow the documented wire
// shape: nothing to read, nothing to send back.
func dialectFor(rawURL, providerName string) utilsopenai.Dialect {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	var dialect utilsopenai.Dialect
	switch u.Hostname() {
	case "openrouter.ai":
		dialect = utilsopenai.OpenRouter
	case "api.deepseek.com":
		dialect = utilsopenai.DeepSeek
	case "hyper.charm.land":
		// Hyper exposes reasoning_content in streamed deltas and accepts
		// the field on replay; its non-streaming responses carry no
		// reasoning, which the text dialect reads as "nothing there".
		dialect = utilsopenai.NewTextDialect()
	}
	if dialect != nil {
		slog.Info("openai provider: reasoning dialect selected",
			"provider", providerName, "host", u.Hostname(), "dialect", dialect.Name())
	}
	return dialect
}
