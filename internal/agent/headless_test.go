// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// TestHeadlessTurn boots a Builder + a fake model and runs one full
// turn through the ADK runner. It asserts that:
//   - the Builder produced a usable agent.Agent;
//   - sending a user message yields at least one model event;
//   - the fake model received the request (i.e. the wiring works).
//
// This is the integration test promised by TODO.md Phase 2:
// "Headless test: send a message via the Facade, get a streamed reply."
// The Facade itself lives in internal/app (future Phase 3 work), so we
// drive the runner directly here.
func TestHeadlessTurn(t *testing.T) {
	fake := &fakeModel{}
	b := &Builder{Providers: newRegistryWithFake(t, fake)}

	inst, err := b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "fake",
		Model:    "fake-model-1",
		Prompt:   "You are baifo.",
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
		Parts: []*genai.Part{{Text: "hello"}},
	}

	var seen int
	for ev, err := range r.Run(context.Background(), "u1", "s1", msg, agent.RunConfig{}) {
		if err != nil {
			t.Fatalf("Run yielded error: %v", err)
		}
		if ev == nil {
			continue
		}
		seen++
	}
	if seen == 0 {
		t.Error("expected at least one event from the runner, got 0")
	}
	if fake.last == nil {
		t.Error("fake model never received an LLMRequest")
	}
}
