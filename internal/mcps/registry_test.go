// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package mcps

import (
	"errors"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

func newRegistry(t *testing.T, entries []config.MCPEntry) *Registry {
	t.Helper()
	r, err := NewRegistry(entries, Builders{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestNewRegistryAcceptsValidEntries(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "filesystem", Type: TypeBuiltin, Builtin: BuiltinFilesystem},
		{Name: "browse", Type: TypeBuiltin, Builtin: BuiltinBrowse},
		{Name: "github", Type: TypeHTTP, Endpoint: "https://example.com/mcp"},
		{Name: "k8s", Type: TypeStdio, Command: "kubernetes-mcp"},
	})
	if len(r.Names()) != 4 {
		t.Errorf("Names: got %d, want 4", len(r.Names()))
	}
}

func TestNewRegistryRejectsUnsupportedBuiltin(t *testing.T) {
	_, err := NewRegistry([]config.MCPEntry{
		{Name: "x", Type: TypeBuiltin, Builtin: "no-such-builtin"},
	}, Builders{})
	if !errors.Is(err, ErrUnsupportedBuiltin) {
		t.Errorf("got %v, want ErrUnsupportedBuiltin", err)
	}
}

func TestNewRegistryRejectsUnsupportedType(t *testing.T) {
	_, err := NewRegistry([]config.MCPEntry{
		{Name: "x", Type: "carrier-pigeon"},
	}, Builders{})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("got %v, want ErrUnsupportedType", err)
	}
}

func TestNewRegistryRejectsHTTPWithoutEndpoint(t *testing.T) {
	_, err := NewRegistry([]config.MCPEntry{
		{Name: "x", Type: TypeHTTP},
	}, Builders{})
	if err == nil {
		t.Error("expected error for http MCP without endpoint, got nil")
	}
}

func TestNewRegistryRejectsStdioWithoutCommand(t *testing.T) {
	_, err := NewRegistry([]config.MCPEntry{
		{Name: "x", Type: TypeStdio},
	}, Builders{})
	if err == nil {
		t.Error("expected error for stdio MCP without command, got nil")
	}
}

func TestNewRegistryRejectsDuplicates(t *testing.T) {
	_, err := NewRegistry([]config.MCPEntry{
		{Name: "dup", Type: TypeBuiltin, Builtin: BuiltinFilesystem},
		{Name: "dup", Type: TypeBuiltin, Builtin: BuiltinBrowse},
	}, Builders{})
	if err == nil {
		t.Error("expected duplicate-name error, got nil")
	}
}

func TestSpecReturnsUnknownMCP(t *testing.T) {
	r := newRegistry(t, nil)
	_, err := r.Spec("does-not-exist")
	if !errors.Is(err, ErrUnknownMCP) {
		t.Errorf("got %v, want ErrUnknownMCP", err)
	}
}

func TestToolsReturnsBuiltinFilesystem(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "filesystem", Type: TypeBuiltin, Builtin: BuiltinFilesystem},
	})
	tools, err := r.Tools("filesystem")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 12 {
		t.Errorf("filesystem tools count: got %d, want 12", len(tools))
	}
}

func TestToolsReturnsBuiltinBrowse(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "browse", Type: TypeBuiltin, Builtin: BuiltinBrowse},
	})
	tools, err := r.Tools("browse")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 3 {
		t.Errorf("browse tools count: got %d, want 3", len(tools))
	}
}

func TestToolsHTTPReturnsNotImplemented(t *testing.T) {
	// Tools() (the legacy entry point) is still builtin-only.
	// External transports go through Toolset() instead — see
	// TestToolsetReturnsToolsetForHTTP below.
	r := newRegistry(t, []config.MCPEntry{
		{Name: "gh", Type: TypeHTTP, Endpoint: "https://example.com/mcp"},
	})
	_, err := r.Tools("gh")
	if !errors.Is(err, ErrTransportNotImplemented) {
		t.Errorf("got %v, want ErrTransportNotImplemented", err)
	}
}

func TestToolsetReturnsToolsetForHTTP(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "gh", Type: TypeHTTP, Endpoint: "https://example.com/mcp"},
	})
	ts, err := r.Toolset("gh")
	if err != nil {
		t.Fatalf("Toolset(http): %v", err)
	}
	if ts == nil {
		t.Errorf("Toolset(http) returned nil toolset without error")
	}
}

func TestToolsetReturnsToolsetForStdio(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "k8s", Type: TypeStdio, Command: "/bin/true"},
	})
	ts, err := r.Toolset("k8s")
	if err != nil {
		t.Fatalf("Toolset(stdio): %v", err)
	}
	if ts == nil {
		t.Errorf("Toolset(stdio) returned nil toolset without error")
	}
}

func TestIsExternalDistinguishesTransports(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "fs", Type: TypeBuiltin, Builtin: BuiltinFilesystem},
		{Name: "gh", Type: TypeHTTP, Endpoint: "https://example.com/mcp"},
		{Name: "k8s", Type: TypeStdio, Command: "/bin/true"},
	})
	for _, c := range []struct {
		name string
		want bool
	}{{"fs", false}, {"gh", true}, {"k8s", true}} {
		got, err := r.IsExternal(c.name)
		if err != nil {
			t.Errorf("IsExternal(%q): %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("IsExternal(%q): got %v want %v", c.name, got, c.want)
		}
	}
}

func TestToolsReturnsUnknownMCPForBadName(t *testing.T) {
	r := newRegistry(t, nil)
	_, err := r.Tools("ghost")
	if !errors.Is(err, ErrUnknownMCP) {
		t.Errorf("got %v, want ErrUnknownMCP", err)
	}
}

func TestToolsMemoisesBuiltinInstances(t *testing.T) {
	r := newRegistry(t, []config.MCPEntry{
		{Name: "fs", Type: TypeBuiltin, Builtin: BuiltinFilesystem},
	})
	if _, err := r.Tools("fs"); err != nil {
		t.Fatalf("first Tools: %v", err)
	}
	first := r.fs
	if _, err := r.Tools("fs"); err != nil {
		t.Fatalf("second Tools: %v", err)
	}
	if r.fs != first {
		t.Error("builtin filesystem instance was rebuilt instead of memoised")
	}
}
