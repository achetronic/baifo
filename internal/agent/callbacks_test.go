// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"iter"
	"testing"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/secrets"
)

// fakeToolCtx is the smallest tool.Context we can build that
// satisfies the interface. We only need FunctionCallID and State()
// on the happy path; the others must exist for the type to match
// but the callback never reaches them.
type fakeToolCtx struct {
	context.Context
	state *fakeState
	id    string
}

func newFakeToolCtx() *fakeToolCtx {
	return &fakeToolCtx{Context: context.Background(), state: newFakeState(), id: "call-1"}
}

func (c *fakeToolCtx) FunctionCallID() string               { return c.id }
func (c *fakeToolCtx) State() session.State                 { return c.state }
func (c *fakeToolCtx) ReadonlyState() session.ReadonlyState { return c.state }
func (c *fakeToolCtx) Artifacts() adkagent.Artifacts        { return nil }
func (c *fakeToolCtx) AgentName() string                    { return "" }
func (c *fakeToolCtx) AppName() string                      { return "test" }
func (c *fakeToolCtx) Branch() string                       { return "" }
func (c *fakeToolCtx) InvocationID() string                 { return "inv" }
func (c *fakeToolCtx) SessionID() string                    { return "s" }
func (c *fakeToolCtx) UserContent() *genai.Content          { return nil }
func (c *fakeToolCtx) UserID() string                       { return "u" }
func (c *fakeToolCtx) Actions() *session.EventActions {
	return &session.EventActions{StateDelta: map[string]any{}}
}
func (c *fakeToolCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return nil
}
func (c *fakeToolCtx) RequestConfirmation(string, any) error { return nil }
func (c *fakeToolCtx) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}

// Artifacts on the real tool.Context is from package agent (agent.Artifacts).
// We don't need it for the callback's code path. To satisfy the
// interface we shadow it as a nil pointer in the unused-field
// assertion below.

// fakeState is a stub session.State for the same reason.
type fakeState struct{ data map[string]any }

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
	return func(yield func(string, any) bool) {
		for k, v := range s.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

// fakeTool is the smallest tool.Tool we can return from a callback
// invocation. The expander only reads Name(); Description and
// IsLongRunning are never touched by it but must exist to satisfy
// the interface.
type fakeTool struct{ name string }

func (t fakeTool) Name() string        { return t.name }
func (t fakeTool) Description() string { return "" }
func (t fakeTool) IsLongRunning() bool { return false }

// ─── tests ─────────────────────────────────────────────────────

// TestBeforeExpand_OpaqueToolBypassesExpansion is the regression for
// the side-channel-via-spawn-args fix: when the tool name is in the
// opaque set, the expander must not rewrite ${secret:NAME} inside the
// args. Without the guard, a spawn call carrying a secret placeholder
// in the child's prompt would bake the raw value into the worker's
// system prompt at construction time.
func TestBeforeExpand_OpaqueToolBypassesExpansion(t *testing.T) {
	store, err := secrets.NewStore(t.TempDir(), "test-key-32-bytes-long-padding!!")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	if err := store.Set("API_KEY", "real-value", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	opaque := map[string]struct{}{"spawn_dynamic_agent": {}}
	cb := makeBeforeExpand(store, secrets.AllowAll{}, opaque)

	args := map[string]any{
		"prompt": "use ${secret:API_KEY} carefully",
	}
	if _, err := cb(newFakeToolCtx(), fakeTool{name: "spawn_dynamic_agent"}, args); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	if args["prompt"] != "use ${secret:API_KEY} carefully" {
		t.Errorf("opaque tool args were rewritten: got %q", args["prompt"])
	}
}

// TestBeforeExpand_NonOpaqueToolStillExpands pins the inverse: a
// regular tool (HTTP MCP, exec, whatever) keeps its existing
// "expand-on-call" behaviour. This is the load-bearing case for
// real-world secret use (Bearer headers, env vars, ...).
func TestBeforeExpand_NonOpaqueToolStillExpands(t *testing.T) {
	store, err := secrets.NewStore(t.TempDir(), "test-key-32-bytes-long-padding!!")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	if err := store.Set("API_KEY", "real-value", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	opaque := map[string]struct{}{"spawn_dynamic_agent": {}}
	cb := makeBeforeExpand(store, secrets.AllowAll{}, opaque)

	args := map[string]any{
		"command": "curl -H 'Authorization: Bearer ${secret:API_KEY}' x",
	}
	if _, err := cb(newFakeToolCtx(), fakeTool{name: "exec"}, args); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	want := "curl -H 'Authorization: Bearer real-value' x"
	if args["command"] != want {
		t.Errorf("non-opaque tool not expanded: got %q want %q", args["command"], want)
	}
}

// TestBeforeExpand_NilOpaqueSetIsHarmless asserts the legacy path:
// without an opaque set, every tool flows through the expander as
// before. Guards against accidentally regressing the default when
// callers (e.g. tests, prototypes) build a Builder without setting
// OpaqueTools.
func TestBeforeExpand_NilOpaqueSetIsHarmless(t *testing.T) {
	store, err := secrets.NewStore(t.TempDir(), "test-key-32-bytes-long-padding!!")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	if err := store.Set("API_KEY", "real-value", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	cb := makeBeforeExpand(store, secrets.AllowAll{}, nil)

	args := map[string]any{"command": "echo ${secret:API_KEY}"}
	if _, err := cb(newFakeToolCtx(), fakeTool{name: "exec"}, args); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	if args["command"] != "echo real-value" {
		t.Errorf("nil opaque set should preserve legacy expansion, got %q", args["command"])
	}
}

// ─── makeAfterRedact: comprehensive pass ────────────────────────

// TestAfterRedact_ScrubsStoreValueNeverRequested is the regression
// for the "tools that emit secrets they were not given" gap. The
// LLM never wrote ${secret:LEAKED} for this call, but the tool
// result happens to contain the raw value. With ScrubAllResponses
// enabled and a permissive min length, the redactor must rewrite
// the value back to the placeholder before the model sees it.
func TestAfterRedact_ScrubsStoreValueNeverRequested(t *testing.T) {
	store, err := secrets.NewStore(t.TempDir(), "test-key-32-bytes-long-padding!!")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	if err := store.Set("LEAKED", "supersecret_value_xyz", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	cb := makeAfterRedact(store, true /*scrubAll*/, 4 /*minLen*/)

	result := map[string]any{
		"stdout": "the response body included supersecret_value_xyz somewhere",
	}
	if _, err := cb(newFakeToolCtx(), fakeTool{name: "exec"}, nil, result, nil); err != nil {
		t.Fatalf("callback error: %v", err)
	}
	want := "the response body included ${secret:LEAKED} somewhere"
	if result["stdout"] != want {
		t.Errorf("comprehensive scrub failed: got %q want %q", result["stdout"], want)
	}
}

// TestAfterRedact_MinLengthFloorSkipsShortValues pins the
// false-positive guard. A secret stored with a very short value
// must be excluded from the comprehensive pass even when scrubAll
// is on; otherwise unrelated substrings of the same length would
// be mangled in every tool result.
func TestAfterRedact_MinLengthFloorSkipsShortValues(t *testing.T) {
	store, err := secrets.NewStore(t.TempDir(), "test-key-32-bytes-long-padding!!")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	// Short value — would cause havoc if scrubbed verbatim.
	if err := store.Set("SHORT_PIN", "1234", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	cb := makeAfterRedact(store, true /*scrubAll*/, 8 /*minLen*/)

	result := map[string]any{
		"stdout": "process exited with code 1234 after 1234 ms",
	}
	if _, err := cb(newFakeToolCtx(), fakeTool{name: "exec"}, nil, result, nil); err != nil {
		t.Fatalf("callback error: %v", err)
	}
	// Untouched: "1234" is shorter than minLen=8 so the
	// comprehensive pass skipped it. The text in the result is
	// preserved verbatim — no false positives.
	if result["stdout"] != "process exited with code 1234 after 1234 ms" {
		t.Errorf("short value should NOT be scrubbed: got %q", result["stdout"])
	}
}

// TestAfterRedact_ScrubAllDisabledStillRunsTargetedPass confirms
// that the targeted pass (values the LLM actually substituted in
// this call) keeps working even when the comprehensive pass is
// disabled. This is the load-bearing legacy behaviour.
func TestAfterRedact_ScrubAllDisabledStillRunsTargetedPass(t *testing.T) {
	store, err := secrets.NewStore(t.TempDir(), "test-key-32-bytes-long-padding!!")
	if err != nil {
		t.Fatalf("open secrets store: %v", err)
	}
	if err := store.Set("API_KEY", "secret_value_xyz_long", ""); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	// Simulate that the BeforeToolCallback substituted API_KEY for
	// this very FunctionCallID. Without going through the full
	// pipeline we populate expandedPairs directly, mirroring what
	// makeBeforeExpand would have done.
	ctx := newFakeToolCtx()
	expandedPairs.Store(ctx.FunctionCallID(), map[string]string{
		"API_KEY": "secret_value_xyz_long",
	})

	// Comprehensive pass disabled, but the targeted pass should
	// still wipe the value we just substituted.
	cb := makeAfterRedact(store, false /*scrubAll*/, 8)

	result := map[string]any{"echo": "we wrote secret_value_xyz_long"}
	if _, err := cb(ctx, fakeTool{name: "exec"}, nil, result, nil); err != nil {
		t.Fatalf("callback error: %v", err)
	}
	want := "we wrote ${secret:API_KEY}"
	if result["echo"] != want {
		t.Errorf("targeted pass should still redact: got %q want %q", result["echo"], want)
	}
}

// TestAfterRedact_NilStoreIsSafe pins the defensive default: when
// the Builder is constructed without a store (tests, prototypes),
// the comprehensive pass must short-circuit instead of crashing.
func TestAfterRedact_NilStoreIsSafe(t *testing.T) {
	cb := makeAfterRedact(nil /*store*/, true, 8)
	result := map[string]any{"text": "anything here including ghp_xyz123"}
	if _, err := cb(newFakeToolCtx(), fakeTool{name: "exec"}, nil, result, nil); err != nil {
		t.Fatalf("callback error: %v", err)
	}
	if result["text"] != "anything here including ghp_xyz123" {
		t.Errorf("nil store should leave result untouched: got %q", result["text"])
	}
}

// TestFilterByMinLen documents the filter directly so future
// changes to the contract are caught without going through the
// full callback path.
func TestFilterByMinLen(t *testing.T) {
	in := map[string]string{
		"long":  "this-is-long-enough",
		"short": "abc",
		"empty": "",
	}
	out := filterByMinLen(in, 8)
	if _, ok := out["long"]; !ok {
		t.Error("long value should pass the filter")
	}
	if _, ok := out["short"]; ok {
		t.Error("short value should be filtered out")
	}
	if _, ok := out["empty"]; ok {
		t.Error("empty value should be filtered out")
	}

	// minLen<=0 disables the filter entirely.
	out = filterByMinLen(in, 0)
	if len(out) != 3 {
		t.Errorf("minLen<=0 should pass everything, got %d entries", len(out))
	}
}
