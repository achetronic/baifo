package providers

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// Part and content builders shared by the test table.
func user(parts ...*genai.Part) *genai.Content {
	return &genai.Content{Role: "user", Parts: parts}
}

func modelRole(parts ...*genai.Part) *genai.Content {
	return &genai.Content{Role: "model", Parts: parts}
}

func text(s string) *genai.Part {
	return &genai.Part{Text: s}
}

func thought(s string, sig []byte) *genai.Part {
	return &genai.Part{Thought: true, Text: s, ThoughtSignature: sig}
}

func fCall(name string) *genai.Part {
	return &genai.Part{FunctionCall: &genai.FunctionCall{Name: name}}
}

func fResp(name, content string) *genai.Part {
	return &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: name, Response: map[string]any{"result": content}}}
}

// deepCopyRequest snapshots a request so mutations of the original can
// be detected field by field after the call, not just by part counts.
func deepCopyRequest(req *model.LLMRequest) *model.LLMRequest {
	if req == nil {
		return nil
	}
	out := *req
	out.Contents = make([]*genai.Content, len(req.Contents))
	for i, c := range req.Contents {
		cc := *c
		if c.Parts != nil {
			cc.Parts = make([]*genai.Part, len(c.Parts))
			for j, p := range c.Parts {
				pp := *p
				pp.ThoughtSignature = append([]byte(nil), p.ThoughtSignature...)
				cc.Parts[j] = &pp
			}
		}
		out.Contents[i] = &cc
	}
	return &out
}

func TestStripStaleThoughts(t *testing.T) {
	tests := []struct {
		name     string
		input    *model.LLMRequest
		expected *model.LLMRequest
	}{
		{
			name:     "nil request",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty contents",
			input:    &model.LLMRequest{Contents: []*genai.Content{}},
			expected: &model.LLMRequest{Contents: []*genai.Content{}},
		},
		{
			name: "history ending with a plain user message strips all thoughts",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(thought("think1", []byte("sig1")), text("ans1")),
					user(text("world")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(text("ans1")),
					user(text("world")),
				},
			},
		},
		{
			name: "active tool loop preserves the model thought",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think2", []byte("sig2")), fCall("fn1")),
					user(fResp("fn1", "res1")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think2", []byte("sig2")), fCall("fn1")),
					user(fResp("fn1", "res1")),
				},
			},
		},
		{
			name: "split model turn preserves thoughts on both trailing model contents",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think3", []byte("sig3")), text("wait")),
					modelRole(thought("think4", []byte("sig4")), fCall("fn2")),
					user(fResp("fn2", "res2")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think3", []byte("sig3")), text("wait")),
					modelRole(thought("think4", []byte("sig4")), fCall("fn2")),
					user(fResp("fn2", "res2")),
				},
			},
		},
		{
			name: "resolved loop followed by a new user message strips all thoughts",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think5", []byte("sig5")), fCall("fn3")),
					user(fResp("fn3", "res3")),
					modelRole(thought("think6", []byte("sig6")), text("done")),
					user(text("new question")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(fCall("fn3")),
					user(fResp("fn3", "res3")),
					modelRole(text("done")),
					user(text("new question")),
				},
			},
		},
		{
			name: "two loops: stale thoughts of the resolved loop go, active loop thoughts stay",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("first task")),
					modelRole(thought("old", []byte("sig-old")), fCall("fnA")),
					user(fResp("fnA", "resA")),
					modelRole(text("first done")),
					user(text("second task")),
					modelRole(thought("live", []byte("sig-live")), fCall("fnB")),
					user(fResp("fnB", "resB")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("first task")),
					modelRole(fCall("fnA")),
					user(fResp("fnA", "resA")),
					modelRole(text("first done")),
					user(text("second task")),
					modelRole(thought("live", []byte("sig-live")), fCall("fnB")),
					user(fResp("fnB", "resB")),
				},
			},
		},
		{
			name: "thought under a user role inside the active run is stripped",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think7", []byte("sig7")), fCall("fn4")),
					user(thought("rogue", []byte("sig8")), fResp("fn4", "res4")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call tool")),
					modelRole(thought("think7", []byte("sig7")), fCall("fn4")),
					user(fResp("fn4", "res4")),
				},
			},
		},
		{
			name: "signature-only thought (empty text) is stripped like any other",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(thought("", []byte("redacted-blob")), text("ans")),
					user(text("next")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(text("ans")),
					user(text("next")),
				},
			},
		},
		{
			name: "history ending with a model content keeps its thought",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(thought("think8", []byte("sig8")), text("hi")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(thought("think8", []byte("sig8")), text("hi")),
				},
			},
		},
		{
			name: "history made only of model and tool contents keeps every thought",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					modelRole(thought("t1", []byte("s1")), fCall("fn")),
					user(fResp("fn", "res")),
					modelRole(thought("t2", []byte("s2")), text("more")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					modelRole(thought("t1", []byte("s1")), fCall("fn")),
					user(fResp("fn", "res")),
					modelRole(thought("t2", []byte("s2")), text("more")),
				},
			},
		},
		{
			name: "tool response mixed with plain text still counts as tool response",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call")),
					modelRole(thought("live", []byte("sig")), fCall("fn")),
					user(text("tool said:"), fResp("fn", "res")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call")),
					modelRole(thought("live", []byte("sig")), fCall("fn")),
					user(text("tool said:"), fResp("fn", "res")),
				},
			},
		},
		{
			name: "content with zero parts is passed through untouched",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					modelRole(),
					user(text("next")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("hello")),
					{Role: "model", Parts: []*genai.Part{}},
					user(text("next")),
				},
			},
		},
		{
			name: "empty role defaults to user: thought stripped",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "", Parts: []*genai.Part{thought("think9", []byte("sig9")), text("hi")}},
					user(text("next")),
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "", Parts: []*genai.Part{text("hi")}},
					user(text("next")),
				},
			},
		},
		{
			name: "empty role inside the active run: thought stripped",
			input: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call")),
					{Role: "", Parts: []*genai.Part{thought("rogue", []byte("sig10")), fResp("fn", "res")}},
				},
			},
			expected: &model.LLMRequest{
				Contents: []*genai.Content{
					user(text("call")),
					{Role: "", Parts: []*genai.Part{fResp("fn", "res")}},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := deepCopyRequest(tc.input)

			got := StripStaleThoughts(tc.input)

			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("output mismatch\nexpected: %+v\ngot:      %+v", tc.expected, got)
			}

			// The input must come back byte-identical: the function
			// promises shallow clones, never in-place edits.
			if !reflect.DeepEqual(tc.input, snapshot) {
				t.Errorf("input was mutated\nbefore: %+v\nafter:  %+v", snapshot, tc.input)
			}
		})
	}
}

// fakeLLM records the request GenerateContent receives so tests can
// assert what the wrapper forwarded to the underlying model.
type fakeLLM struct {
	model.LLM // panics if any other method is called; fine for the test
	gotReq    *model.LLMRequest
}

func (f *fakeLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	f.gotReq = req
	return func(yield func(*model.LLMResponse, error) bool) {}
}

// TestWrapStripThoughtsFiltersBeforeDelegating pins the decorator every
// provider build function relies on: the wrapped model must receive the
// request with stale thoughts already stripped, and the caller's request
// must stay untouched.
func TestWrapStripThoughtsFiltersBeforeDelegating(t *testing.T) {
	staleThought := &genai.Part{Text: "old reasoning", Thought: true}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleModel, Parts: []*genai.Part{staleThought, {Text: "answer"}}},
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "next question"}}},
		},
	}
	snapshot := deepCopyRequest(req)

	fake := &fakeLLM{}
	wrapped := WrapStripThoughts(fake)
	for range wrapped.GenerateContent(context.Background(), req, false) {
	}

	if fake.gotReq == nil {
		t.Fatal("wrapped model never received the request")
	}
	for _, c := range fake.gotReq.Contents {
		for _, p := range c.Parts {
			if p.Thought {
				t.Errorf("stale thought leaked through the wrapper: %+v", p)
			}
		}
	}
	if !reflect.DeepEqual(req, snapshot) {
		t.Error("caller's request was mutated by the wrapper")
	}
}
