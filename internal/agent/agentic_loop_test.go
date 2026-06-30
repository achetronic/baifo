// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"iter"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// twoTurnFakeModel emits a tool call on the first GenerateContent
// invocation and a final text answer on the second one. It records
// EVERY request it sees so the test can compare round 1 vs round 2.
// Used by TestAgenticLoopOrdering to inspect what baifo actually sends
// to the model in a turn that involves one tool call.
type twoTurnFakeModel struct {
	calls    int
	requests []*model.LLMRequest
}

var _ model.LLM = (*twoTurnFakeModel)(nil)

func (*twoTurnFakeModel) Name() string { return "fake" }

func (m *twoTurnFakeModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	// Deep-snapshot the request so subsequent ADK mutations don't
	// rewrite history. We marshal/unmarshal to a generic map; that
	// is enough fidelity for the test log and avoids importing
	// every nested ADK type just to clone.
	snap := cloneRequest(req)
	m.requests = append(m.requests, snap)
	m.calls++

	return func(yield func(*model.LLMResponse, error) bool) {
		if m.calls == 1 {
			// Round 1: ask for the diagnostic tool.
			resp := &model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{FunctionCall: &genai.FunctionCall{
							ID:   "call-1",
							Name: "diag",
							Args: map[string]any{"q": "ping"},
						}},
					},
				},
			}
			yield(resp, nil)
			return
		}
		// Round 2 (after the tool response was fed back in): final text.
		resp := &model.LLMResponse{
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: "tool said pong"}},
			},
		}
		yield(resp, nil)
	}
}

// cloneRequest returns a JSON-round-trip copy of req. Good enough for
// the diagnostic dump; not a defensive clone for production code.
func cloneRequest(req *model.LLMRequest) *model.LLMRequest {
	raw, err := json.Marshal(req)
	if err != nil {
		return req
	}
	var out model.LLMRequest
	if err := json.Unmarshal(raw, &out); err != nil {
		return req
	}
	return &out
}

// TestAgenticLoopOrdering drives one full turn with one tool call so
// we can see, in order:
//
//  1. What lands in LLMRequest #1 (system instruction + user 'hola' +
//     tool declarations).
//  2. What lands in LLMRequest #2 (everything above, PLUS the
//     assistant turn with the FunctionCall, PLUS the FunctionResponse
//     part fed back to the model).
//
// The test logs both requests as pretty JSON. The shape of those
// requests is the ground truth of what the model sees; if Gemini
// answers something weird, it's because of the content here.
func TestAgenticLoopOrdering(t *testing.T) {
	fake := &twoTurnFakeModel{}
	b := &Builder{Providers: newRegistryWithFake(t, fake)}

	// A single fake tool. The agent doesn't need MCPs / Skills /
	// worker tools to exercise the agentic loop; one in-process
	// tool is enough to force the round-trip.
	diagTool, err := functiontool.New(
		functiontool.Config{
			Name:        "diag",
			Description: "diagnostic tool; returns a fixed string",
		},
		func(_ tool.Context, _ struct{ Q string }) (struct{ Echo string }, error) {
			return struct{ Echo string }{Echo: "pong"}, nil
		},
	)
	if err != nil {
		t.Fatalf("functiontool.New: %v", err)
	}

	inst, err := b.Build(context.Background(), "root", Spec{
		Name:       "baifo",
		Provider:   "fake",
		Model:      "fake-model-1",
		Prompt:     "You are baifo. Reply naturally.",
		ExtraTools: []tool.Tool{diagTool},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	r, err := runner.New(runner.Config{
		AppName:           "baifo-test",
		Agent:             inst.Agent,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		t.Fatalf("runner.New: %v", err)
	}

	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: "hola"}},
	}
	for ev, err := range r.Run(context.Background(), "u1", "s1", msg, agent.RunConfig{}) {
		_ = ev
		if err != nil {
			t.Fatalf("Run yielded error: %v", err)
		}
	}

	if len(fake.requests) < 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", len(fake.requests))
	}
	for i, req := range fake.requests {
		dump, _ := json.MarshalIndent(req, "", "  ")
		t.Logf("=== LLMRequest #%d ===\n%s", i+1, dump)
	}
}
