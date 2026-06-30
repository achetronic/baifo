// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

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
