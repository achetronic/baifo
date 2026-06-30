// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// fakeRC is a minimal agent.ReadonlyContext for tests: it embeds a
// context.Context and stubs the rest of the interface.
type fakeRC struct{ context.Context }

func (f fakeRC) UserContent() *genai.Content          { return nil }
func (f fakeRC) InvocationID() string                 { return "test-inv" }
func (f fakeRC) AgentName() string                    { return "test" }
func (f fakeRC) ReadonlyState() session.ReadonlyState { return nil }
func (f fakeRC) UserID() string                       { return "u" }
func (f fakeRC) AppName() string                      { return "baifo" }
func (f fakeRC) SessionID() string                    { return "s" }
func (f fakeRC) Branch() string                       { return "" }

func rc() agent.ReadonlyContext { return fakeRC{context.Background()} }

// stubToolset is a tool.Toolset whose Tools() behaviour the test
// controls: it can return tools, return an error, hang, or panic.
type stubToolset struct {
	name  string
	tools []tool.Tool
	err   error
	hang  time.Duration
	panic bool
}

func (s *stubToolset) Name() string { return s.name }
func (s *stubToolset) Tools(_ agent.ReadonlyContext) ([]tool.Tool, error) {
	if s.panic {
		panic("boom")
	}
	if s.hang > 0 {
		time.Sleep(s.hang)
	}
	return s.tools, s.err
}

// TestResilientToolset_PassesThroughOnSuccess: a healthy toolset's
// tools and Name reach the caller unchanged.
func TestResilientToolset_PassesThroughOnSuccess(t *testing.T) {
	inner := &stubToolset{name: "mcp:good"}
	r := newResilientToolset("good", inner)

	if r.Name() != "mcp:good" {
		t.Errorf("Name() = %q, want mcp:good", r.Name())
	}
	out, err := r.Tools(rc())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil && len(out) != 0 {
		t.Errorf("expected empty tool list from empty stub, got %d", len(out))
	}
}

// TestResilientToolset_SwallowsError: an erroring toolset degrades to
// (nil, nil) so the agent turn still proceeds.
func TestResilientToolset_SwallowsError(t *testing.T) {
	inner := &stubToolset{name: "mcp:bad", err: errors.New("connection refused")}
	r := newResilientToolset("bad", inner)

	out, err := r.Tools(rc())
	if err != nil {
		t.Fatalf("error should be swallowed, got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("failed toolset should contribute no tools, got %d", len(out))
	}
}

// TestResilientToolset_SwallowsPanic: a panicking toolset is recovered
// and degrades gracefully.
func TestResilientToolset_SwallowsPanic(t *testing.T) {
	inner := &stubToolset{name: "mcp:panic", panic: true}
	r := newResilientToolset("panic", inner)

	out, err := r.Tools(rc())
	if err != nil {
		t.Fatalf("panic should be swallowed, got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("panicking toolset should contribute no tools, got %d", len(out))
	}
}

// TestResilientToolset_RespectsCancelledContext: when the invocation
// is already cancelled, Tools returns promptly without waiting for the
// inner toolset.
func TestResilientToolset_RespectsCancelledContext(t *testing.T) {
	inner := &stubToolset{name: "mcp:slow", hang: 5 * time.Second}
	r := newResilientToolset("slow", inner)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	out, err := r.Tools(fakeRC{ctx})
	if err != nil {
		t.Fatalf("cancelled context should yield nil error, got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected no tools on cancel, got %d", len(out))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("cancelled context should return promptly, took %v", elapsed)
	}
}
