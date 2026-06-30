// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package main

import "strings"

// providerChoice captures the provider the first-run wizard configured.
// It is the single input to the template-injection helpers below, which
// turn the static scaffold templates into a ready-to-run configuration
// without losing any of the inline documentation those templates carry.
type providerChoice struct {
	// name is the provider id written to baifo.yaml and referenced by
	// the root agent's llm.provider in agents.yaml.
	name string

	// typ is the provider type: "anthropic" | "openai" | "gemini".
	typ string

	// oauth is true when the user picked OAuth (anthropic only). In that
	// case no API key is asked for and no secret is written; the entry
	// carries `auth: oauth` and the user logs in with `baifo provider auth`.
	oauth bool

	// url is the endpoint override for an OpenAI-compatible provider
	// (Ollama, OpenRouter, vLLM, ...). Empty for the canonical providers.
	url string

	// apiKey is the raw secret value the user typed. Empty when oauth.
	apiKey string

	// secretName is the secrets.yaml key the API key is stored under and
	// referenced from baifo.yaml as ${secret:NAME}. Empty when oauth.
	secretName string

	// model is the model id set on the root agent.
	model string
}

// applyProviderToBaifoYAML replaces the empty `providers: []` line in the
// baifo.yaml template with a populated single-provider list. Everything
// else in the template (the explanatory comment block above the line
// included) is preserved verbatim.
func applyProviderToBaifoYAML(tmpl string, c providerChoice) string {
	var b strings.Builder
	b.WriteString("providers:\n")
	b.WriteString("  - name: " + c.name + "\n")
	b.WriteString("    type: " + c.typ + "\n")
	if c.url != "" {
		b.WriteString("    url: " + c.url + "\n")
	}
	if c.oauth {
		// Trailing line, no newline: the template's own newline after
		// `providers: []` stays in place.
		b.WriteString("    auth: oauth")
	} else {
		b.WriteString("    api_key: ${secret:" + c.secretName + "}")
	}
	return strings.Replace(tmpl, "providers: []", b.String(), 1)
}

// applyProviderToAgentsYAML points the ROOT agent at the chosen provider
// and model. agents.yaml carries two empty llm blocks (the root and the
// utility agent); only the first — the root — is filled. The utility
// agent is deliberately left empty so it falls back to the root's model.
func applyProviderToAgentsYAML(tmpl string, c providerChoice) string {
	out := strings.Replace(tmpl, `      provider: ""`, `      provider: "`+c.name+`"`, 1)
	out = strings.Replace(out, `      model: ""`, `      model: "`+c.model+`"`, 1)
	return out
}
