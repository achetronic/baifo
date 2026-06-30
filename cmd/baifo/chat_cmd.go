// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/app"
	"github.com/achetronic/baifo/internal/config"
)

// runChatCommand handles `baifo chat`. It is a deliberately minimal,
// TUI-free harness: stdin in, stdout out, the ADK runner in between.
// No BubbleTea, no A2A, no event filtering. Useful to verify that the
// agent itself (prompt, tools, spawn) works before blaming the chrome.
//
// The harness reuses app.New for the boot sequence, so providers,
// MCPs, secrets, skills and the spawn toolset (static + dynamic) all
// behave exactly like in the TUI path. The only thing it does not do
// is wrap the agent in a2asrv: SendMessage's A2A path is intentionally
// bypassed because the goal of this command is to expose the raw ADK
// behaviour.
func runChatCommand(dir string, args []string) exitCode {
	fs := flag.NewFlagSet("baifo chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: baifo chat [flags]")
		fmt.Fprintln(os.Stderr, "\nStart a headless REPL or send a one-shot message to the root agent.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	oneShot := fs.String("message", "", "send a single message and exit (skips the REPL)")
	verbose := fs.Bool("v", false, "print every ADK event (partials, tool calls, tool results)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo chat: load config: %v\n", err)
		return exitError
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	a, err := app.New(ctx, cfg, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo chat: %v\n", err)
		return exitError
	}
	defer func() { _ = a.Close() }()

	if err := a.RootBuildError(); err != nil {
		fmt.Fprintf(os.Stderr, "baifo chat: root agent not ready: %v\n", err)
		return exitError
	}
	rootAgent := a.RootAgent()
	if rootAgent == nil {
		fmt.Fprintln(os.Stderr, "baifo chat: no root agent configured")
		return exitError
	}

	// Build a dedicated runner. We do NOT reuse the App's internal
	// adka2a executor: this command is the diagnostic baseline,
	// runner.Run is as direct as it gets.
	r, err := runner.New(runner.Config{
		AppName:           "baifo",
		Agent:             rootAgent,
		SessionService:    a.SessionService(),
		AutoCreateSession: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo chat: build runner: %v\n", err)
		return exitError
	}

	chat := &chatSession{
		runner:       r,
		userID:       a.UserID(),
		sessionID:    a.SessionID(),
		verbose:      *verbose,
		out:          os.Stdout,
		printedTools: make(map[string]bool),
	}

	if *oneShot != "" {
		if err := chat.send(ctx, *oneShot); err != nil {
			fmt.Fprintf(os.Stderr, "baifo chat: %v\n", err)
			return exitError
		}
		return exitOK
	}

	fmt.Fprintf(os.Stdout, "baifo chat (model: %s, session: %s)\nType your message. Ctrl-D or /quit to exit.\n\n",
		a.ModelName(), a.SessionID())
	return chat.repl(ctx, os.Stdin)
}

// chatSession is the per-process state of the headless REPL.
type chatSession struct {
	runner       *runner.Runner
	userID       string
	sessionID    string
	verbose      bool
	out          io.Writer
	printedTools map[string]bool
}

// repl reads one line per iteration from in and dispatches it to the
// agent. Empty lines are ignored. Lines starting with '/' are treated
// as REPL commands; anything else is sent to the agent.
func (c *chatSession) repl(ctx context.Context, in io.Reader) exitCode {
	scanner := bufio.NewScanner(in)
	// Large buffer so multi-line pastes don't get truncated. 1MiB
	// matches what the TUI editor accepts; anything larger is almost
	// certainly an accident.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		if ctx.Err() != nil {
			return exitOK
		}
		fmt.Fprint(c.out, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(c.out)
			return exitOK
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return exitOK
		}
		if err := c.send(ctx, line); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		fmt.Fprintln(c.out)
	}
}

// send drives one user turn through the runner and prints whatever
// the agent produces. The loop is a near-verbatim copy of ADK's
// official console launcher (google.golang.org/adk/cmd/launcher/
// console/console.go @ v1.2.0). The only deviation: when -v is set,
// we also print tool calls / tool results from ev.Content as a debug
// trace. Default mode keeps the upstream behaviour unchanged.
func (c *chatSession) send(ctx context.Context, text string) error {
	userMsg := genai.NewContentFromText(text, genai.RoleUser)

	prevText := ""
	for event, err := range c.runner.Run(ctx, c.userID, c.sessionID, userMsg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}) {
		if err != nil {
			fmt.Fprintf(c.out, "\nAGENT_ERROR: %v\n", err)
			continue
		}

		if c.verbose {
			c.dumpToolActivity(event)
		}

		if event.LLMResponse.Content == nil {
			continue
		}

		txt := ""
		for _, p := range event.LLMResponse.Content.Parts {
			txt += p.Text
		}

		// In SSE mode, always print partial responses and capture them.
		if !event.IsFinalResponse() {
			fmt.Fprint(c.out, txt)
			prevText += txt
			continue
		}

		// Only print final response if it doesn't match previously captured text.
		if txt != prevText {
			fmt.Fprint(c.out, txt)
		}
		prevText = ""
	}
	fmt.Fprintln(c.out)
	return nil
}

// dumpToolActivity prints function calls and function responses
// when the user asked for verbose output. Kept separate from send()
// so the main loop stays a faithful copy of the official launcher.
func (c *chatSession) dumpToolActivity(event *session.Event) {
	if event == nil || event.Content == nil {
		return
	}
	for _, p := range event.Content.Parts {
		if p == nil {
			continue
		}
		switch {
		case p.FunctionCall != nil:
			b, _ := json.MarshalIndent(p.FunctionCall.Args, "", "  ")
			str := fmt.Sprintf("[tool call] %s(%s)", p.FunctionCall.Name, string(b))
			if !c.printedTools[str] {
				fmt.Fprintf(c.out, "\n%s\n", str)
				c.printedTools[str] = true
			}
		case p.FunctionResponse != nil:
			b, _ := json.MarshalIndent(p.FunctionResponse.Response, "", "  ")
			str := fmt.Sprintf("[tool result] %s -> %s", p.FunctionResponse.Name, string(b))
			if !c.printedTools[str] {
				fmt.Fprintf(c.out, "\n%s\n", str)
				c.printedTools[str] = true
			}
		}
	}
}
