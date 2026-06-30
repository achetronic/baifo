// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package providers

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"

	"google.golang.org/adk/model"

	"github.com/achetronic/baifo/internal/config"
)

// fakeModel is a minimal stand-in for model.LLM used in tests. We only
// need an instance whose identity we can compare.
type fakeModel struct {
	id string
}

// Ensure fakeModel implements model.LLM at compile time. If the
// interface changes upstream we want to see it here, not at runtime.
var _ model.LLM = (*fakeModel)(nil)

func (*fakeModel) Name() string { return "fake" }
func (*fakeModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {}
}

// withFakeBuilder swaps the global builders map for the test and
// restores it on cleanup. Concurrent tests would clash on this global,
// so we serialise via testBuildersMu.
var testBuildersMu sync.Mutex

func withFakeBuilder(t *testing.T, typ string, b Builder) {
	t.Helper()
	testBuildersMu.Lock()
	t.Cleanup(testBuildersMu.Unlock)

	original := builders
	builders = map[string]Builder{typ: b}
	t.Cleanup(func() { builders = original })
}

func TestNewRegistryRejectsUnknownType(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})

	_, err := NewRegistry([]config.ProviderEntry{
		{Name: "x", Type: "unknown-type"},
	})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("got %v, want ErrUnsupportedType", err)
	}
}

// withFakeAuthFlow installs a fake OAuth flow for typ and restores the
// authFlows map on cleanup. It must be called after withFakeBuilder,
// which already holds testBuildersMu for the duration of the test, so
// this helper mutates the global without re-locking.
func withFakeAuthFlow(t *testing.T, typ string) {
	t.Helper()
	original := authFlows
	authFlows = map[string]AuthFlow{typ: func(string, string) error { return nil }}
	t.Cleanup(func() { authFlows = original })
}

func TestNewRegistryRejectsOAuthOnTypeWithoutFlow(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})
	withFakeAuthFlow(t, "other") // a flow exists, but not for "known"

	_, err := NewRegistry([]config.ProviderEntry{
		{Name: "x", Type: "known", Auth: "oauth"},
	})
	if !errors.Is(err, ErrUnsupportedAuth) {
		t.Errorf("got %v, want ErrUnsupportedAuth", err)
	}
}

func TestNewRegistryAcceptsOAuthWhenFlowRegistered(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})
	withFakeAuthFlow(t, "known")

	_, err := NewRegistry([]config.ProviderEntry{
		{Name: "x", Type: "known", Auth: "oauth"},
	})
	if err != nil {
		t.Errorf("oauth on a type with a registered flow should be accepted, got %v", err)
	}
}

func TestNewRegistryAcceptsDefaultAuthModes(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})
	// No auth flow registered for "known": empty and api_key must still
	// be accepted, since they need no flow.
	for _, auth := range []string{"", "api_key"} {
		_, err := NewRegistry([]config.ProviderEntry{
			{Name: "x", Type: "known", Auth: auth},
		})
		if err != nil {
			t.Errorf("auth %q should be accepted without a flow, got %v", auth, err)
		}
	}
}

func TestNewRegistryRejectsDuplicates(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})

	_, err := NewRegistry([]config.ProviderEntry{
		{Name: "dup", Type: "known"},
		{Name: "dup", Type: "known"},
	})
	if err == nil {
		t.Fatal("expected duplicate name error, got nil")
	}
}

func TestNewRegistryRejectsMissingName(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})

	_, err := NewRegistry([]config.ProviderEntry{
		{Type: "known"},
	})
	if err == nil {
		t.Fatal("expected missing-name error, got nil")
	}
}

func TestModelReturnsUnknownProviderError(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})

	r, err := NewRegistry([]config.ProviderEntry{
		{Name: "anthropic-main", Type: "known"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	_, err = r.Model(context.Background(), "does-not-exist", "model-x")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("got %v, want ErrUnknownProvider", err)
	}
}

func TestModelRequiresModelID(t *testing.T) {
	withFakeBuilder(t, "known", func(context.Context, Spec, string, ModelOptions) (model.LLM, error) {
		return &fakeModel{}, nil
	})

	r, err := NewRegistry([]config.ProviderEntry{
		{Name: "p", Type: "known"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	_, err = r.Model(context.Background(), "p", "")
	if err == nil {
		t.Error("expected error when model id is empty, got nil")
	}
}

func TestModelCachesPerNameAndModelID(t *testing.T) {
	var calls int
	withFakeBuilder(t, "known", func(_ context.Context, _ Spec, modelID string, _ ModelOptions) (model.LLM, error) {
		calls++
		return &fakeModel{id: modelID}, nil
	})

	r, err := NewRegistry([]config.ProviderEntry{
		{Name: "p", Type: "known"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	ctx := context.Background()
	m1, err := r.Model(ctx, "p", "gpt-5")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	m2, err := r.Model(ctx, "p", "gpt-5")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if m1 != m2 {
		t.Errorf("expected cached model identity, got different instances")
	}
	if calls != 1 {
		t.Errorf("Builder called %d times, want 1", calls)
	}

	// Different model id must trigger a fresh build.
	m3, err := r.Model(ctx, "p", "claude-sonnet")
	if err != nil {
		t.Fatalf("Model: %v", err)
	}
	if m3 == m1 {
		t.Error("expected fresh model for distinct modelID, got cached one")
	}
	if calls != 2 {
		t.Errorf("Builder called %d times, want 2", calls)
	}
}
