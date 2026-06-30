// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// crudYAML seeds an agents.yaml with a flagged root plus one
// regular sub-agent so the CRUD invariants have something to
// chew on.
const crudYAML = `
version: 1
agents:
  - name: baifo
    root: true
    prompt: |
      you are baifo
    llm:
      provider: fake
      model: fake-model
  - name: helper
    prompt: |
      you are a helper
    llm:
      provider: fake
      model: fake-model
`

// TestAgentCRUD_RejectsDeletingRoot is the core safety: the root
// entry must not be removable through the standard CRUD. The
// error surfaces in the chat when the user tries.
func TestAgentCRUD_RejectsDeletingRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, crudYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	err = a.DeleteAgent(context.Background(), "baifo")
	if err == nil {
		t.Fatal("expected DeleteAgent on root to fail")
	}
	if !strings.Contains(err.Error(), "root agent") {
		t.Errorf("error should mention root: %v", err)
	}
}

// TestAgentCRUD_RejectsDemoting_Root tests the invariant that you
// cannot save the root entry with root: false — that would leave
// baifo without an entry point.
func TestAgentCRUD_RejectsDemoting_Root(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, crudYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Same entry, without the root flag. The upsert validator
	// should refuse this.
	demoted := `name: baifo
description: ""
prompt: |
  you are baifo
llm:
  provider: fake
  model: fake-model
`
	err = a.UpsertAgent(context.Background(), demoted)
	if err == nil {
		t.Fatal("expected UpsertAgent demoting the root to fail")
	}
	if !strings.Contains(err.Error(), "root: true") {
		t.Errorf("error should mention root: true requirement: %v", err)
	}
}

// TestAgentCRUD_RejectsSecondRoot prevents the user from flagging
// a sub-agent as root while another is already flagged.
func TestAgentCRUD_RejectsSecondRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, crudYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Try to flag the helper as a second root.
	body := `name: helper
root: true
prompt: |
  you are a helper
llm:
  provider: fake
  model: fake-model
`
	err = a.UpsertAgent(context.Background(), body)
	if err == nil {
		t.Fatal("expected UpsertAgent flagging a second root to fail")
	}
	if !strings.Contains(err.Error(), "already the root") {
		t.Errorf("error should mention existing root: %v", err)
	}
}

// TestAgentCRUD_AllowsEditingRoot makes sure regular field edits
// on the root entry (description, prompt, ...) still go through
// — the protection is targeted, not blanket.
func TestAgentCRUD_AllowsEditingRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, crudYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	body := `name: baifo
root: true
description: New description
prompt: |
  you are baifo — updated
llm:
  provider: fake
  model: fake-model
`
	if err := a.UpsertAgent(context.Background(), body); err != nil {
		t.Fatalf("UpsertAgent on root should succeed when flag is kept: %v", err)
	}
}
