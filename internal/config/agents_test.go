// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentsYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, AgentsFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadAgentsParsesValidFile(t *testing.T) {
	path := writeAgentsYAML(t, `
version: 1
agents:
  - name: deep-researcher
    description: research stuff
    prompt: |
      you are a research agent
    llm:
      provider: openai-main
      model: gpt-5
    skills: [web-research]
    mcps: [browse]
`)
	f, err := LoadAgents(path)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if len(f.Agents) != 1 {
		t.Fatalf("agents: got %d, want 1", len(f.Agents))
	}
	a := f.Agents[0]
	if a.Name != "deep-researcher" || a.LLM.Effective() != "openai-main" || a.LLM.Model != "gpt-5" {
		t.Errorf("agent: %+v", a)
	}
}

func TestLoadAgentsMissingFileReturnsEmpty(t *testing.T) {
	f, err := LoadAgents(filepath.Join(t.TempDir(), "no-such.yaml"))
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if len(f.Agents) != 0 {
		t.Errorf("expected empty list, got %d", len(f.Agents))
	}
}

func TestLoadAgentsRejectsDuplicates(t *testing.T) {
	path := writeAgentsYAML(t, `
agents:
  - name: dup
    prompt: x
    llm:
      provider: p
      model: m
  - name: dup
    prompt: x
    llm:
      provider: p
      model: m
`)
	_, err := LoadAgents(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestLoadAgentsRejectsMissingPrompt(t *testing.T) {
	path := writeAgentsYAML(t, `
agents:
  - name: nope
    llm:
      provider: p
      model: m
`)
	_, err := LoadAgents(path)
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Errorf("expected prompt error, got %v", err)
	}
}

func TestLoadAgentsRejectsMissingLLM(t *testing.T) {
	path := writeAgentsYAML(t, `
agents:
  - name: nope
    prompt: hi
`)
	_, err := LoadAgents(path)
	if err == nil {
		t.Fatal("expected error for missing llm fields")
	}
}

func TestLoadAgentsRejectsInvalidReasoning(t *testing.T) {
	path := writeAgentsYAML(t, `
agents:
  - name: nope
    prompt: hi
    llm:
      provider: p
      model: m
      reasoning: ultra
`)
	_, err := LoadAgents(path)
	if err == nil || !strings.Contains(err.Error(), "reasoning") {
		t.Errorf("expected reasoning error, got %v", err)
	}
}

func TestLoadAgentsAcceptsValidReasoning(t *testing.T) {
	path := writeAgentsYAML(t, `
agents:
  - name: ok
    prompt: hi
    llm:
      provider: p
      model: m
      reasoning: high
`)
	f, err := LoadAgents(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Agents[0].LLM.Reasoning != "high" {
		t.Errorf("reasoning not parsed: %q", f.Agents[0].LLM.Reasoning)
	}
}

func TestLoadAgentsExpandsEnv(t *testing.T) {
	t.Setenv("BAIFO_AGENT_NAME", "from-env")
	path := writeAgentsYAML(t, `
agents:
  - name: ${BAIFO_AGENT_NAME}
    prompt: hi
    llm:
      provider: p
      model: m
`)
	f, err := LoadAgents(path)
	if err != nil {
		t.Fatalf("LoadAgents: %v", err)
	}
	if f.Agents[0].Name != "from-env" {
		t.Errorf("env expansion failed: %q", f.Agents[0].Name)
	}
}
