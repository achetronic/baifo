// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// TestExampleConfigBootCleanlyWithSecret is a regression guard for two
// bugs we hit when the example config moved to Gemini:
//
//  1. config.Load ran os.ExpandEnv on the raw YAML, which silently
//     erased ${secret:NAME} placeholders because `:` is not a legal
//     env var name char. The provider then received an empty api_key.
//  2. providers.NewRegistry never resolved ${secret:NAME} on api_key
//     anyway, so even if the placeholder survived parsing it would
//     have been handed to the model SDK verbatim.
//
// This test sets up the bundled config/baifo.yaml with a plaintext
// secret pre-seeded and asserts the root agent builds cleanly. If
// either bug regresses, RootBuildError will be non-nil.
func TestExampleConfigBootCleanlyWithSecret(t *testing.T) {
	dir := t.TempDir()

	baifoYAML := `version: 1
providers:
  - name: gemini
    type: gemini
    api_key: ${secret:GEMINI_API_KEY}
`
	agentsYAML := `version: 1
agents:
  - root: true
    name: coordinator
    prompt: "You are a helpful assistant."
    llm:
      provider: gemini
      model: gemini-1.5-flash
`

	if err := os.WriteFile(filepath.Join(dir, "baifo.yaml"), []byte(baifoYAML), 0o600); err != nil {
		t.Fatalf("write baifo.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents.yaml"), []byte(agentsYAML), 0o600); err != nil {
		t.Fatalf("write agents.yaml: %v", err)
	}

	// Pre-stage a plaintext secret so the api_key expansion has
	// something real to resolve. The value is bogus on purpose —
	// the test only cares that boot completes, not that the model
	// actually answers.
	secretsYAML := `version: 1
encrypted: false
secrets:
  GEMINI_API_KEY:
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-01T00:00:00Z
    value: plain:v1:bm90LWEtcmVhbC1rZXk=
`
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte(secretsYAML), 0o600); err != nil {
		t.Fatalf("write secrets.yaml: %v", err)
	}

	cfg, err := config.Load(filepath.Join(dir, "baifo.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Sanity: the placeholder must survive parsing untouched.
	if cfg.Providers[0].APIKey != "${secret:GEMINI_API_KEY}" {
		t.Fatalf("placeholder eaten by config loader: got %q",
			cfg.Providers[0].APIKey)
	}

	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if buildErr := a.RootBuildError(); buildErr != nil {
		t.Fatalf("RootBuildError: %v", buildErr)
	}
	if a.RootName() != "coordinator" {
		t.Errorf("RootName = %q, want %q", a.RootName(), "coordinator")
	}
}
