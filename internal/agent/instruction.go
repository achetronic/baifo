// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package agent

import (
	"google.golang.org/adk/agent"
)

// staticInstructionProvider returns an InstructionProvider that always
// yields the given prompt. We use this instead of llmagent.Config's
// Instruction field to bypass ADK's {var} template substitution, which
// otherwise mangles user prompts that contain curly braces (code
// blocks, JSON examples, ...).
func staticInstructionProvider(prompt string) func(agent.ReadonlyContext) (string, error) {
	return func(agent.ReadonlyContext) (string, error) {
		return prompt, nil
	}
}
