// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"testing"
)

// TestDemoSpecialRows is a manual visualiser, not an assertion test.
// Run it with:
//
//	go test ./internal/tui/ -run TestDemoSpecialRows -v
//
// It renders the context-guard notice (collapsed + expanded) and the
// agent-error row with full ANSI colour so you can eyeball the header
// band styling in a real terminal.
func TestDemoSpecialRows(t *testing.T) {
	theme := NewTheme()
	chat := newChatView(theme, true)
	chat.SetSize(72, 40)

	rows := []struct {
		caption string
		msg     Message
	}{
		{
			caption: "context guard · collapsed",
			msg: Message{
				Kind: MessageNotice,
				Text: "The user asked baifo to fix a silent-hang bug after exec; " +
					"baifo traced it to eventFromA2A dropping failed task events, " +
					"patched it, added regression tests and rebuilt the binary.",
			},
		},
		{
			caption: "context guard · expanded",
			msg: Message{
				Kind:     MessageNotice,
				Expanded: true,
				Text: "The user asked baifo to fix a silent-hang bug after exec; " +
					"baifo traced it to eventFromA2A dropping failed task events, " +
					"patched it, added regression tests and rebuilt the binary.",
			},
		},
		{
			caption: "context guard · expanded (long, multi-paragraph)",
			msg: Message{
				Kind:     MessageNotice,
				Expanded: true,
				Text: "The user is working on baifo, a terminal UI " +
					"for orchestrating LLM agents. The conversation started with a " +
					"silent-hang bug: after running an exec tool the model would " +
					"stop replying with no error shown.\n\n" +
					"baifo traced the root cause to eventFromA2A in internal/app/" +
					"app.go, which silently dropped every TaskStatusUpdateEvent " +
					"including failed ones, so a turn that errored ended with no " +
					"text and no error. It patched the translation to surface " +
					"TaskStateFailed as a visible agent-error event, added " +
					"regression tests, and rebuilt the binary.\n\n" +
					"Then the work moved to styling: the context-guard notice and " +
					"the agent-error row were reworked into a homogeneous family of " +
					"special rows with a coloured header band, an explicit \"Enter " +
					"to expand\" affordance, and an expandable body rendered inside " +
					"the same rounded brown box the tool-execution rows use. The " +
					"last fix made that box keep its background colour on every " +
					"line instead of falling back to black.",
			},
		},
		{
			caption: "agent error · collapsed",
			msg: Message{
				Kind: MessageAgentError,
				Text: `llm error response: "overloaded_error: the model is temporarily overloaded, please retry shortly"`,
			},
		},
		{
			caption: "agent error · expanded",
			msg: Message{
				Kind:     MessageAgentError,
				Expanded: true,
				Text:     `llm error response: "overloaded_error: the model is temporarily overloaded, please retry shortly"`,
			},
		},
	}

	var b []byte
	b = append(b, []byte("\n========== special rows ==========\n")...)
	for _, r := range rows {
		b = append(b, []byte("\n--- "+r.caption+" ---\n")...)
		b = append(b, []byte(chat.renderMessage(r.msg, 0, false, false))...)
		b = append(b, '\n')
	}
	// Print raw (with ANSI) straight to stdout so colours survive;
	// t.Log would prefix every line and mangle the alignment.
	fmt.Print(string(b))
}
