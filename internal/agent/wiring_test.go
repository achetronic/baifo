// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/mcps"
)

func TestBuildResolvesMCPTools(t *testing.T) {
	registry, err := mcps.NewRegistry([]config.MCPEntry{
		{Name: "filesystem", Type: mcps.TypeBuiltin, Builtin: mcps.BuiltinFilesystem},
		{Name: "browse", Type: mcps.TypeBuiltin, Builtin: mcps.BuiltinBrowse},
	}, mcps.Builders{})
	if err != nil {
		t.Fatalf("mcps.NewRegistry: %v", err)
	}

	b := &Builder{
		Providers: newRegistryWithFake(t, &fakeModel{}),
		MCPs:      registry,
	}

	inst, err := b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "fake",
		Model:    "fake-model-1",
		Prompt:   "You are baifo.",
		MCPs:     []string{"filesystem", "browse"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if inst.Agent == nil {
		t.Fatal("Agent must not be nil")
	}
}

func TestBuildRejectsSpecMCPsWithoutRegistry(t *testing.T) {
	b := &Builder{Providers: newRegistryWithFake(t, &fakeModel{})}
	_, err := b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "fake",
		Model:    "fake-model-1",
		MCPs:     []string{"filesystem"},
	})
	if err == nil {
		t.Fatal("expected error when Spec.MCPs is set but Builder.MCPs is nil")
	}
}

func TestBuildSurfacesUnknownMCPError(t *testing.T) {
	registry, err := mcps.NewRegistry(nil, mcps.Builders{})
	if err != nil {
		t.Fatalf("mcps.NewRegistry: %v", err)
	}
	b := &Builder{
		Providers: newRegistryWithFake(t, &fakeModel{}),
		MCPs:      registry,
	}
	_, err = b.Build(context.Background(), "root", Spec{
		Name:     "baifo",
		Provider: "fake",
		Model:    "fake-model-1",
		MCPs:     []string{"does-not-exist"},
	})
	if err == nil {
		t.Fatal("expected error for unknown MCP, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should mention the MCP name, got: %v", err)
	}
}
