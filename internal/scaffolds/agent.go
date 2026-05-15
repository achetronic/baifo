// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package scaffolds

// Agent returns the YAML template for new agents.
// If you add a field to config.AgentTemplate, update this scaffold.
func Agent(suggestedName string) string {
	name := suggestedName
	if name == "" {
		name = "my-agent"
	}
	return `name: ` + name + `              # Unique identifier

# Set to true for the primary/entry-point agent. Only one agent should be root.
# root: false

# Set to true for the background utility agent: the cheap model baifo
# uses for internal chores (session titles, context compaction).
# At most one agent may set it.
# utility: false

description: A short description of this agent.

# System prompt (usually a multi-line string)
prompt: |
  You are a specialized agent. Replace this with your instructions.

llm:
  provider: anthropic     # Provider name from baifo.yaml
  model: claude-sonnet-4
  # reasoning: medium     # Optional: reasoning level (minimal | low | medium | high)
  # reasoning_api: ""     # Optional: enabled | adaptive (empty = auto-detect)

# Skills available to the agent (must exist under .baifo/skills/ or be installed)
# skills:
#   - post-writer

# MCPs available to the agent (must be configured in baifo.yaml)
# mcps:
#   - filesystem
#   - browse

# Secrets the agent can access using the ${secret:NAME} syntax
# allowed_secrets:
#   - GITHUB_TOKEN

# Context window management
# context_guard:
#   enabled: false
#   strategy: threshold    # threshold (token-aware summary) or sliding_window (rotates history)
#   max_tokens: 0          # Max tokens to keep (0 for auto-detect)
#   max_turns: 0           # Max turns to keep (0 for sliding_window default)
`
}
