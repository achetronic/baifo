// SPDX-License-Identifier: Apache-2.0

package todos

import (
	"context"
	"iter"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"
)

// fakeState is a minimal session.State backed by a plain map.
// Mirrors session-state semantics: Get on a missing key returns
// session.ErrStateKeyNotExist, Set replaces, All snapshots.
type fakeState struct {
	data map[string]any
}

func newFakeState() *fakeState { return &fakeState{data: map[string]any{}} }

func (s *fakeState) Get(k string) (any, error) {
	v, ok := s.data[k]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}
func (s *fakeState) Set(k string, v any) error { s.data[k] = v; return nil }
func (s *fakeState) All() iter.Seq2[string, any] {
	snap := make(map[string]any, len(s.data))
	for k, v := range s.data {
		snap[k] = v
	}
	return func(yield func(string, any) bool) {
		for k, v := range snap {
			if !yield(k, v) {
				return
			}
		}
	}
}

// fakeToolContext implements just enough of tool.Context for the
// todos handlers, which only ever touch State(). Every other
// accessor returns a zero value; the embedded context.Context
// satisfies the ReadonlyContext requirement (which itself embeds
// context.Context, hence the Deadline/Done/Err/Value methods).
type fakeToolContext struct {
	context.Context
	state *fakeState
}

func newFakeToolContext() *fakeToolContext {
	return &fakeToolContext{Context: context.Background(), state: newFakeState()}
}

func (c *fakeToolContext) State() session.State                 { return c.state }
func (c *fakeToolContext) ReadonlyState() session.ReadonlyState { return c.state }
func (c *fakeToolContext) Artifacts() agent.Artifacts           { return nil }
func (c *fakeToolContext) AgentName() string                    { return "test-agent" }
func (c *fakeToolContext) AppName() string                      { return "test" }
func (c *fakeToolContext) Branch() string                       { return "" }
func (c *fakeToolContext) FunctionCallID() string               { return "" }
func (c *fakeToolContext) InvocationID() string                 { return "inv" }
func (c *fakeToolContext) SessionID() string                    { return "s" }
func (c *fakeToolContext) UserContent() *genai.Content          { return nil }
func (c *fakeToolContext) UserID() string                       { return "u" }
func (c *fakeToolContext) Actions() *session.EventActions {
	return &session.EventActions{StateDelta: map[string]any{}}
}
func (c *fakeToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return nil
}
func (c *fakeToolContext) RequestConfirmation(string, any) error { return nil }
func (c *fakeToolContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}

// Compile-time assertion: if tool.Context grows, this line fails
// and we'll know we need to stub more methods.
var _ tool.Context = (*fakeToolContext)(nil)

// Avoid the "imported and not used" warning on time when the
// embedded context.Context doesn't need it. Real bots use this
// to deadline tool calls.
var _ = time.Now

// ─── tests ─────────────────────────────────────────────────────

func TestReadTodosReturnsNilWhenUnset(t *testing.T) {
	ctx := newFakeToolContext()
	items, err := readTodos(ctx)
	if err != nil {
		t.Fatalf("readTodos: %v", err)
	}
	if items != nil {
		t.Errorf("expected nil, got %#v", items)
	}
}

func TestWriteThenReadRoundTrip(t *testing.T) {
	ctx := newFakeToolContext()
	in := []TodoItem{
		{Content: "do x", Status: "pending"},
		{Content: "do y", Status: "in_progress", ActiveForm: "Doing y"},
	}
	if err := writeTodos(ctx, in); err != nil {
		t.Fatalf("writeTodos: %v", err)
	}
	out, err := readTodos(ctx)
	if err != nil {
		t.Fatalf("readTodos: %v", err)
	}
	if len(out) != 2 || out[1].ActiveForm != "Doing y" {
		t.Errorf("round-trip mismatch: %#v", out)
	}
}

// TestReadTodosCoercesJSONRoundTrip is the regression for the
// contextguard interop: after a session is reloaded from SQLite,
// the value comes back as []any of map[string]any instead of
// []TodoItem. readTodos must handle both shapes.
func TestReadTodosCoercesJSONRoundTrip(t *testing.T) {
	ctx := newFakeToolContext()
	_ = ctx.state.Set(stateKey, []any{
		map[string]any{"content": "do x", "status": "pending"},
		map[string]any{"content": "do y", "status": "completed", "active_form": "Doing y"},
		"garbage entry that should be skipped",
	})
	out, err := readTodos(ctx)
	if err != nil {
		t.Fatalf("readTodos: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 valid items (garbage skipped), got %d: %#v", len(out), out)
	}
	if out[1].ActiveForm != "Doing y" {
		t.Errorf("active_form lost in coercion: %#v", out[1])
	}
}

func TestReadTodosRejectsUnexpectedShape(t *testing.T) {
	ctx := newFakeToolContext()
	_ = ctx.state.Set(stateKey, 42)
	_, err := readTodos(ctx)
	if err == nil {
		t.Error("expected error for non-list shape, got nil")
	}
}

func TestADKToolsRegistersAllFour(t *testing.T) {
	tools, err := New().ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	want := map[string]bool{
		"todos_list":   false,
		"todos_write":  false,
		"todos_update": false,
		"todos_clear":  false,
	}
	for _, tl := range tools {
		if _, ok := want[tl.Name()]; ok {
			want[tl.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}
}
