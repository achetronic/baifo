// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"iter"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/providers"
)

// fakeModel is a deterministic model.LLM used in builder tests. It
// returns a single canned response and records the last request it
// saw, which is enough to verify wiring without going to the network.
type fakeModel struct {
	last *model.LLMRequest
}

var _ model.LLM = (*fakeModel)(nil)

func (*fakeModel) Name() string { return "fake" }
func (m *fakeModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.last = req
	return func(yield func(*model.LLMResponse, error) bool) {
		resp := &model.LLMResponse{
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: "ok"}},
			},
		}
		yield(resp, nil)
	}
}

func newRegistryWithFake(t *testing.T, fake model.LLM) *providers.Registry {
	t.Helper()
	providers.Register("fake-for-test", func(context.Context, providers.Spec, string, providers.ModelOptions) (model.LLM, error) {
		return fake, nil
	})
	t.Cleanup(providers.Reset)

	r, err := providers.NewRegistry([]config.ProviderEntry{
		{Name: "fake", Type: "fake-for-test"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestBuildRejectsEmptySpec(t *testing.T) {
	b := &Builder{Providers: newRegistryWithFake(t, &fakeModel{})}
	_, err := b.Build(context.Background(), "root", Spec{})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("got %v, want ErrInvalidSpec", err)
	}
}

func TestBuildResolvesProviderAndModel(t *testing.T) {
	b := &Builder{Providers: newRegistryWithFake(t, &fakeModel{})}
	inst, err := b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "fake",
		Model:    "fake-model-1",
		Prompt:   "You are baifo.",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if inst.ID != "root" {
		t.Errorf("ID: got %q, want %q", inst.ID, "root")
	}
	if inst.Agent == nil {
		t.Error("Agent must not be nil")
	}
}

func TestBuildRejectsMissingProviderRegistry(t *testing.T) {
	b := &Builder{}
	_, err := b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "x",
		Model:    "y",
	})
	if err == nil {
		t.Error("expected error when Providers is nil, got nil")
	}
}
