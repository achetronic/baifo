// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// TestLLMRequestShapeForFirstTurn is a diagnostic test that dumps the
// exact LLMRequest the model receives on the first user turn. It
// helps answer questions like:
//
//   - Does the system prompt arrive intact, in the right slot (system
//     instruction vs first user message)?
//   - Is there ANY extra content sandwiched between the system prompt
//     and the user's first message ("hola")?
//   - In what role are tool descriptions / functions attached?
//
// Alby reported that the model was answering 'Understood' to 'hola'
// as if it had received a briefing rather than a greeting; this test
// gives us the ground truth we can reason about.
func TestLLMRequestShapeForFirstTurn(t *testing.T) {
	const systemPrompt = "You are baifo. Reply naturally to the user."
	fake := &fakeModel{}
	b := &Builder{Providers: newRegistryWithFake(t, fake)}

	inst, err := b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "fake",
		Model:    "fake-model-1",
		Prompt:   systemPrompt,
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
		_ = err
	}
	if fake.last == nil {
		t.Fatal("fake model never received a request")
	}

	// Dump the request so the test log is the source of truth for
	// what arrives at the model boundary. The reflection-y dump is
	// intentional: a structural diff against a golden file would be
	// brittle while ADK is still moving, but the printed shape is
	// readable enough to reason about.
	raw, err := json.MarshalIndent(fake.last, "", "  ")
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	t.Logf("LLMRequest received by the model on first user turn:\n%s", raw)

	dump := string(raw)
	// Sanity checks: system prompt should appear, user message should
	// appear, in some shape ADK exposes.
	if !strings.Contains(dump, systemPrompt) {
		t.Errorf("system prompt not present in LLMRequest")
	}
	if !strings.Contains(dump, "hola") {
		t.Errorf("user message 'hola' not present in LLMRequest")
	}
}
