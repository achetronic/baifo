// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/secrets"
)

// TestExpandProviderSecrets_NilStoreLeavesPlaceholders confirms that
// when no secrets store is wired (dev without encryption_key) the
// expander does not error and keeps the placeholder. The downstream
// "api key is required" error then carries the useful hint.
func TestExpandProviderSecrets_NilStoreLeavesPlaceholders(t *testing.T) {
	in := []config.ProviderEntry{
		{Name: "p", Type: "openai", APIKey: "${secret:KEY}"},
	}
	out, err := ExpandSecrets(in, nil)
	if err != nil {
		t.Fatalf("expandProviderSecrets: %v", err)
	}
	if out[0].APIKey != "${secret:KEY}" {
		t.Errorf("api_key with nil store: got %q, want placeholder preserved", out[0].APIKey)
	}
}

// TestExpandProviderSecrets_ResolvesAPIKey is the happy path.
func TestExpandProviderSecrets_ResolvesAPIKey(t *testing.T) {
	store := newPlainStore(t, map[string]string{
		"KEY": "real-value",
	})
	in := []config.ProviderEntry{
		{Name: "p", Type: "openai", APIKey: "${secret:KEY}"},
	}
	out, err := ExpandSecrets(in, store)
	if err != nil {
		t.Fatalf("expandProviderSecrets: %v", err)
	}
	if out[0].APIKey != "real-value" {
		t.Errorf("api_key: got %q, want %q", out[0].APIKey, "real-value")
	}
}

// TestExpandProviderSecrets_ResolvesHeaders applies the same logic to
// headers values so custom Authorization or similar headers can also
// reference secrets.
func TestExpandProviderSecrets_ResolvesHeaders(t *testing.T) {
	store := newPlainStore(t, map[string]string{
		"BEARER": "tok",
	})
	in := []config.ProviderEntry{
		{
			Name: "p", Type: "openai",
			Headers: map[string]string{
				"Authorization": "Bearer ${secret:BEARER}",
				"X-Static":      "always",
			},
		},
	}
	out, err := ExpandSecrets(in, store)
	if err != nil {
		t.Fatalf("expandProviderSecrets: %v", err)
	}
	if out[0].Headers["Authorization"] != "Bearer tok" {
		t.Errorf("Authorization: got %q, want %q", out[0].Headers["Authorization"], "Bearer tok")
	}
	if out[0].Headers["X-Static"] != "always" {
		t.Errorf("X-Static should be untouched: %q", out[0].Headers["X-Static"])
	}
}

// TestExpandProviderSecrets_MissingSecretErrors fails loudly so the
// user sees a clear message instead of a silent empty-key boot.
func TestExpandProviderSecrets_MissingSecretErrors(t *testing.T) {
	store := newPlainStore(t, map[string]string{})
	in := []config.ProviderEntry{
		{Name: "p", Type: "openai", APIKey: "${secret:MISSING}"},
	}
	_, err := ExpandSecrets(in, store)
	if err == nil {
		t.Fatal("expected error for missing secret, got nil")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("error should mention the secret name, got: %v", err)
	}
}

// TestExpandProviderSecrets_DoesNotMutateInput keeps callers honest:
// the original slice should be safe to keep around (e.g. for a
// concurrent reload comparison).
func TestExpandProviderSecrets_DoesNotMutateInput(t *testing.T) {
	store := newPlainStore(t, map[string]string{"K": "v"})
	in := []config.ProviderEntry{
		{Name: "p", Type: "openai", APIKey: "${secret:K}"},
	}
	_, _ = ExpandSecrets(in, store)
	if in[0].APIKey != "${secret:K}" {
		t.Errorf("input was mutated: got %q", in[0].APIKey)
	}
}

// newPlainStore returns a freshly-initialised plaintext-mode secrets
// store seeded with the given pairs. Plaintext mode keeps the test
// hermetic (no fixed encryption key needed).
func newPlainStore(t *testing.T, seed map[string]string) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := secrets.NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for k, v := range seed {
		if err := s.Set(k, v, ""); err != nil {
			t.Fatalf("seed Set(%q): %v", k, err)
		}
	}
	_ = context.Background()
	return s
}
