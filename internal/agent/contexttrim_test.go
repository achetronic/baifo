// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/genai"
)

// newUserContent builds a *genai.Content with role "user" and the given text parts.
func newUserContent(texts ...string) *genai.Content {
	c := &genai.Content{Role: "user"}
	for _, t := range texts {
		c.Parts = append(c.Parts, &genai.Part{Text: t})
	}
	return c
}

// newModelContent builds a *genai.Content with role "model" and the given text parts.
func newModelContent(texts ...string) *genai.Content {
	c := &genai.Content{Role: "model"}
	for _, t := range texts {
		c.Parts = append(c.Parts, &genai.Part{Text: t})
	}
	return c
}

// invokeCallback calls the BeforeModelCallback of the first plugin registered
// in the trim plugin and returns the result. It panics if the plugin has no
// BeforeModelCallback (test setup error).
func invokeCallback(t *testing.T, maxChars int, req *model.LLMRequest) (*model.LLMResponse, error) {
	t.Helper()
	p := BuildContextTrimPlugin(maxChars)
	if p == nil {
		t.Fatal("BuildContextTrimPlugin returned nil unexpectedly")
	}
	cb := p.BeforeModelCallback()
	if cb == nil {
		t.Fatal("plugin has no BeforeModelCallback")
	}
	// agent.CallbackContext is an interface; the callback ignores it, so nil is fine.
	return cb(nil, req)
}

// --- BuildContextTrimPlugin ---

func TestBuildContextTrimPlugin_NilWhenDisabled(t *testing.T) {
	cases := []int{0, -1, -99}
	for _, c := range cases {
		if got := BuildContextTrimPlugin(c); got != nil {
			t.Errorf("BuildContextTrimPlugin(%d) = non-nil, want nil", c)
		}
	}
}

func TestBuildContextTrimPlugin_NonNilWhenEnabled(t *testing.T) {
	if got := BuildContextTrimPlugin(100); got == nil {
		t.Error("BuildContextTrimPlugin(100) = nil, want non-nil")
	}
}

// --- callback trims user-role text ---

func TestCallback_TrimsOversizedUserPart(t *testing.T) {
	const cap = 10
	long := strings.Repeat("x", 50)
	req := &model.LLMRequest{
		Contents: []*genai.Content{newUserContent(long)},
	}

	resp, err := invokeCallback(t, cap, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must return (nil, nil) — non-nil response would short-circuit the model call.
	if resp != nil {
		t.Errorf("callback returned non-nil *model.LLMResponse; a non-nil response short-circuits the model call")
	}

	got := req.Contents[0].Parts[0].Text
	if len(got) <= cap {
		// Fine: it was trimmed (with marker it is longer than cap, but the
		// payload part is at most cap bytes).
	}
	if !strings.Contains(got, "[context-trim:") {
		t.Errorf("trimmed text has no marker; got %q", got)
	}
	if strings.HasPrefix(got, strings.Repeat("x", cap+1)) {
		t.Errorf("text still has more than %d leading 'x' bytes; not trimmed", cap)
	}
}

func TestCallback_UnderCapPartsUntouched(t *testing.T) {
	const cap = 100
	short := "hello"
	req := &model.LLMRequest{
		Contents: []*genai.Content{newUserContent(short)},
	}

	_, _ = invokeCallback(t, cap, req)

	if got := req.Contents[0].Parts[0].Text; got != short {
		t.Errorf("under-cap part was modified: got %q, want %q", got, short)
	}
}

// --- model-role content is NOT trimmed ---

func TestCallback_ModelRoleNotTrimmed(t *testing.T) {
	const cap = 5
	long := strings.Repeat("m", 200)
	req := &model.LLMRequest{
		Contents: []*genai.Content{newModelContent(long)},
	}

	_, _ = invokeCallback(t, cap, req)

	if got := req.Contents[0].Parts[0].Text; got != long {
		t.Errorf("model-role content was trimmed; got len %d, want %d", len(got), len(long))
	}
}

// --- user-role Thought parts are NOT trimmed ---

func TestCallback_ThoughtPartNotTrimmed(t *testing.T) {
	const cap = 5
	long := strings.Repeat("t", 200)
	c := &genai.Content{Role: "user"}
	c.Parts = append(c.Parts, &genai.Part{Text: long, Thought: true})
	req := &model.LLMRequest{Contents: []*genai.Content{c}}

	_, _ = invokeCallback(t, cap, req)

	if got := c.Parts[0].Text; got != long {
		t.Errorf("Thought part was trimmed; got len %d, want %d", len(got), len(long))
	}
}

// --- callback returns (nil, nil) ---

func TestCallback_ReturnsNilNil(t *testing.T) {
	// This is a load-bearing assertion: a non-nil *model.LLMResponse returned
	// from BeforeModelCallback short-circuits the entire model call, replacing
	// the real response with the callback's return value. We must never do that.
	const cap = 10
	long := strings.Repeat("a", 50)
	req := &model.LLMRequest{
		Contents: []*genai.Content{newUserContent(long)},
	}

	resp, err := invokeCallback(t, cap, req)
	if resp != nil {
		t.Error("callback returned non-nil LLMResponse; this would short-circuit the model call")
	}
	if err != nil {
		t.Errorf("callback returned non-nil error: %v", err)
	}
}

// --- multi-byte rune boundary safety ---

func TestCallback_MultiByteRuneBoundary(t *testing.T) {
	// "日本語" is 3 runes × 3 bytes each = 9 bytes.
	// With cap=5, the naive cut at byte 5 would land mid-rune (byte 4 of
	// "本" which starts at byte 3). trimUserPart must walk back to the
	// last safe boundary (byte 3 = end of "日").
	text := "日本語" // 9 bytes
	const cap = 5
	req := &model.LLMRequest{
		Contents: []*genai.Content{newUserContent(text)},
	}

	_, _ = invokeCallback(t, cap, req)

	got := req.Contents[0].Parts[0].Text
	// The result must be valid UTF-8.
	if !strings.HasPrefix(got, "日") {
		t.Errorf("rune boundary not respected: result starts with invalid sequence, got %q", got)
	}
	if !strings.Contains(got, "[context-trim:") {
		t.Errorf("trimmed text has no marker; got %q", got)
	}
}

// --- WithContextTrim ---

func TestWithContextTrim_NilTrimIsNoop(t *testing.T) {
	original := runner.PluginConfig{}
	got := WithContextTrim(original, nil)
	if len(got.Plugins) != 0 {
		t.Errorf("WithContextTrim(cfg, nil) changed Plugins to len %d, want 0", len(got.Plugins))
	}
}

func TestWithContextTrim_PrependsTrimPlugin(t *testing.T) {
	trim := BuildContextTrimPlugin(100)
	if trim == nil {
		t.Fatal("expected non-nil trim plugin")
	}
	cfg := runner.PluginConfig{}
	got := WithContextTrim(cfg, trim)
	if len(got.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(got.Plugins))
	}
	if got.Plugins[0] != trim {
		t.Errorf("first plugin is not the trim plugin")
	}
}

func TestWithContextTrim_PrependsBeforeExisting(t *testing.T) {
	trim := BuildContextTrimPlugin(100)
	guard := BuildContextTrimPlugin(200) // a second plugin as stand-in for the guard
	cfg := runner.PluginConfig{Plugins: []*plugin.Plugin{guard}}
	got := WithContextTrim(cfg, trim)
	if len(got.Plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(got.Plugins))
	}
	if got.Plugins[0] != trim {
		t.Errorf("trim plugin must be first; got different plugin at index 0")
	}
	if got.Plugins[1] != guard {
		t.Errorf("existing plugin must be second; got different plugin at index 1")
	}
}
