// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"context"
	"testing"

	"github.com/achetronic/baifo/internal/config"
	_ "github.com/achetronic/baifo/internal/providers/allproviders"
)

const guardEnabledAgentsYAML = `
version: 1
agents:
  - name: guard-root
    root: true
    prompt: |
      placeholder prompt
    llm:
      provider: fake
      model: fake-model
    context_guard:
      enabled: true
      strategy: threshold
`

const guardDisabledAgentsYAML = `
version: 1
agents:
  - name: plain-root
    root: true
    prompt: |
      placeholder prompt
    llm:
      provider: fake
      model: fake-model
`

func TestApp_ContextGuardStatus_EnabledThreshold(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, guardEnabledAgentsYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	st := a.ContextGuardStatus(context.Background())
	if !st.Enabled {
		t.Fatalf("guard should be enabled, got %+v", st)
	}
	if st.Strategy != "threshold" {
		t.Errorf("strategy: got %q, want threshold", st.Strategy)
	}
	// Fresh session: no real tokens recorded yet, no compaction yet.
	if st.Percent != 0 {
		t.Errorf("fresh session percent: got %d, want 0", st.Percent)
	}
	if st.Limit <= 0 {
		t.Errorf("threshold limit should be positive, got %d", st.Limit)
	}
	if st.Fingerprint != "" {
		t.Errorf("no compaction yet, fingerprint should be empty, got %q", st.Fingerprint)
	}
}

func TestApp_ContextGuardStatus_DisabledWhenNoBlock(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, guardDisabledAgentsYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	st := a.ContextGuardStatus(context.Background())
	if st.Enabled {
		t.Errorf("guard should be disabled with no context_guard block, got %+v", st)
	}
}
