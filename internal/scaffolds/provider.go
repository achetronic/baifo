// SPDX-License-Identifier: Apache-2.0

package scaffolds

// Provider returns the YAML template for new LLM providers.
// If you add a field to config.ProviderEntry, update this scaffold.
func Provider(suggestedName string) string {
	name := suggestedName
	if name == "" {
		name = "my-provider"
	}
	return `name: ` + name + `

type: anthropic             # anthropic, openai, gemini

# Authentication mode: api_key (default) or oauth (anthropic-only;
# run 'baifo provider auth anthropic' once, api_key is then ignored)
# auth: api_key

# API key (secret expansion recommended to avoid hardcoding)
api_key: "\${secret:ANTHROPIC_API_KEY}"

# Optional endpoint override. Any OpenAI-compatible endpoint (a local
# Ollama server, OpenRouter, vLLM, ...) is type: openai with url set here.
# url: "https://api.anthropic.com"

# Optional extra HTTP headers (supports ${secret:NAME} expansion)
# headers:
#   X-Org-ID: "acme"
#   Authorization-Extra: "\${secret:EXTRA_TOKEN}"

# Toggle streaming responses over SSE (defaults to true)
# streaming: false
`
}
