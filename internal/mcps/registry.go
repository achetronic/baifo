// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package mcps wires MCP servers declared in baifo.yaml to ADK toolsets.
//
// In Phase 2.x the package supports the two built-in MCPs (filesystem
// and browse) in-process. HTTP and stdio transports are accepted by
// the validator but not yet wired up — calling Tools on one of those
// returns ErrTransportNotImplemented.
package mcps

import (
	"errors"
	"fmt"

	"google.golang.org/adk/tool"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/mcps/builtin/browse"
	"github.com/achetronic/baifo/internal/mcps/builtin/filesystem"
	"github.com/achetronic/baifo/internal/secrets"
)

// MCP types accepted in baifo.yaml.
const (
	TypeBuiltin = "builtin"
	TypeHTTP    = "http"
	TypeStdio   = "stdio"
)

// Built-in slugs accepted when type == "builtin".
const (
	BuiltinFilesystem = "filesystem"
	BuiltinBrowse     = "browse"
)

// ErrUnsupportedType is returned when an entry uses a type that baifo
// does not know how to handle.
var ErrUnsupportedType = errors.New("unsupported MCP type")

// ErrUnsupportedBuiltin is returned when type=builtin but the `builtin`
// slug is not one of the supported ones.
var ErrUnsupportedBuiltin = errors.New("unsupported built-in MCP")

// ErrUnknownMCP is returned when a caller references an MCP name that
// was not declared in mcps[].
var ErrUnknownMCP = errors.New("unknown MCP")

// ErrTransportNotImplemented is returned by Tools when the MCP entry
// uses a transport (http, stdio) whose wiring is still pending.
var ErrTransportNotImplemented = errors.New("MCP transport not implemented yet")

// Spec is the in-memory description of one MCP declaration. It mirrors
// config.MCPEntry but lives in this package so callers do not couple
// themselves to the on-disk YAML shape.
type Spec struct {
	Name string
	Type string

	// Builtin slug; only meaningful when Type == TypeBuiltin.
	Builtin string

	// HTTP transport fields.
	Endpoint string
	Headers  map[string]string
	Insecure bool

	// Stdio transport fields.
	Command string
	Args    []string
	Env     map[string]string
	Workdir string

	// Auth carries the authentication descriptor for HTTP MCPs. The
	// zero value means no authentication; Headers are still applied on
	// top of whatever auth produces, so a fully-static "X-API-Key"
	// setup uses Auth{Kind: none} + a header.
	Auth config.MCPAuth

	// Options carries the output-cap and tuning settings for
	// type=builtin MCPs. Ignored for all other MCP types.
	Options config.MCPOptions
}

// BrowseConfig groups the options handed to the built-in browse MCP
// when the registry instantiates it. Baifo's main wiring fills these
// from baifo.yaml (download_dir, optional Tavily/Serper keys, ...).
type BrowseConfig = browse.Config

// FilesystemConfig groups the options handed to the built-in filesystem
// MCP. Kept thin on purpose so future per-agent overrides (sandbox path,
// log sink, ...) can be added here without touching every caller.
type FilesystemConfig = filesystem.Config

// Builders bundles the construction-time options for every built-in
// MCP. Callers pass it once to NewRegistry; each builtin instance is
// lazily created on first Tools() call.
type Builders struct {
	Filesystem FilesystemConfig
	Browse     BrowseConfig
}

// Registry validates the MCP declarations from baifo.yaml and resolves
// them on demand into ADK toolsets.
type Registry struct {
	specs    map[string]Spec
	builders Builders

	// secrets is the optional secrets.Store used to expand
	// ${secret:NAME} placeholders in HTTP headers and stdio env
	// values at toolset-build time. nil when the user has not
	// configured an encryption_key; placeholders pass through
	// untouched in that case.
	secrets *secrets.Store

	// tokens persists OAuth access/refresh tokens across process
	// restarts. nil-safe; when not configured, OAuth still works
	// but the user re-authenticates on every boot.
	tokens *TokenStore

	// clients persists Dynamic Client Registration credentials
	// across process restarts. nil-safe; without it every
	// interactive OAuth flow registers a brand-new client with
	// the AS, leaving zombies behind. Set via WithDCRClientStore.
	clients *DCRClientStore

	// Memoised builtin instances, created on first use.
	fs     *filesystem.Tools
	browse *browse.Tools
}

// WithSecrets attaches a secrets store to the registry so HTTP
// header values and stdio env entries can reference
// ${secret:NAME} placeholders. Idempotent; callers typically wire
// it once in App.New right after the secrets store opens.
func (r *Registry) WithSecrets(s *secrets.Store) *Registry {
	r.secrets = s
	return r
}

// WithTokenStore attaches the SQLite-backed OAuth token persistence
// so the registry can reuse tokens across process restarts and let
// the underlying handler refresh them transparently. Optional; when
// nil, OAuth flows still work but tokens are lost on every boot.
func (r *Registry) WithTokenStore(t *TokenStore) *Registry {
	r.tokens = t
	return r
}

// WithDCRClientStore attaches the SQLite-backed Dynamic Client
// Registration persistence so the registry can reuse client_id /
// client_secret across boots and avoid registering a fresh client
// with the authorisation server on every authenticate call.
// Optional; when nil, every interactive flow re-registers.
func (r *Registry) WithDCRClientStore(c *DCRClientStore) *Registry {
	r.clients = c
	return r
}

// NewRegistry parses entries from baifo.yaml, validates each one, and
// returns a Registry ready to be queried by name.
func NewRegistry(entries []config.MCPEntry, b Builders) (*Registry, error) {
	specs := make(map[string]Spec, len(entries))
	for i, e := range entries {
		spec, err := validate(e)
		if err != nil {
			return nil, fmt.Errorf("mcps[%d] (%s): %w", i, e.Name, err)
		}
		if _, dup := specs[spec.Name]; dup {
			return nil, fmt.Errorf("mcps[%d]: duplicate name %q", i, spec.Name)
		}
		specs[spec.Name] = spec
	}
	return &Registry{specs: specs, builders: b}, nil
}

// Names returns every registered MCP name. Useful for the TUI listing.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.specs))
	for n := range r.specs {
		out = append(out, n)
	}
	return out
}

// Spec returns the declaration for a given name, or ErrUnknownMCP.
func (r *Registry) Spec(name string) (Spec, error) {
	s, ok := r.specs[name]
	if !ok {
		return Spec{}, fmt.Errorf("%w: %q", ErrUnknownMCP, name)
	}
	return s, nil
}

// Tools resolves the MCP named name into an ADK tool list. Built-in
// MCPs are constructed lazily and cached on first call so multiple
// agents that share an MCP also share its in-memory state (undo,
// scratch, processes). HTTP and stdio transports return
// ErrTransportNotImplemented for now.
func (r *Registry) Tools(name string) ([]tool.Tool, error) {
	spec, err := r.Spec(name)
	if err != nil {
		return nil, err
	}
	switch spec.Type {
	case TypeBuiltin:
		return r.builtinTools(spec)
	case TypeHTTP, TypeStdio:
		return nil, fmt.Errorf("%w: %s (mcp %q)", ErrTransportNotImplemented, spec.Type, name)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, spec.Type)
	}
}

// builtinTools returns the toolset of the requested built-in MCP. The
// builtin instances are memoised so two agents pointing at the same
// builtin share its mutable state.
func (r *Registry) builtinTools(spec Spec) ([]tool.Tool, error) {
	switch spec.Builtin {
	case BuiltinFilesystem:
		if r.fs == nil {
			cfg := r.builders.Filesystem
			cfg.MaxExecOutputChars = spec.Options.EffectiveExecOutputChars()
			cfg.MaxReadFileChars = spec.Options.EffectiveReadFileChars()
			cfg.MaxSearchOutputChars = spec.Options.EffectiveSearchOutputChars()
			r.fs = filesystem.New(cfg)
		}
		return r.fs.ADKTools()
	case BuiltinBrowse:
		if r.browse == nil {
			r.browse = browse.New(r.builders.Browse)
		}
		return r.browse.ADKTools()
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedBuiltin, spec.Builtin)
	}
}

// validate enforces the per-type invariants and returns the resulting
// Spec. The work is intentionally lightweight — we only refuse entries
// that we know we cannot serve later.
func validate(e config.MCPEntry) (Spec, error) {
	if e.Name == "" {
		return Spec{}, errors.New("missing name")
	}
	spec := Spec{
		Name:     e.Name,
		Type:     e.Type,
		Builtin:  e.Builtin,
		Endpoint: e.Endpoint,
		Headers:  e.Headers,
		Insecure: e.Insecure,
		Command:  e.Command,
		Args:     e.Args,
		Env:      e.Env,
		Workdir:  e.Workdir,
		Auth:     e.Auth,
		Options:  e.Options,
	}

	switch e.Auth.EffectiveKind() {
	case config.MCPAuthKindNone, config.MCPAuthKindOAuth:
		// ok
	default:
		return Spec{}, fmt.Errorf("unsupported auth kind: %q", e.Auth.Kind)
	}

	switch e.Type {
	case TypeBuiltin:
		switch e.Builtin {
		case BuiltinFilesystem, BuiltinBrowse:
			// ok
		default:
			return Spec{}, fmt.Errorf("%w: %q", ErrUnsupportedBuiltin, e.Builtin)
		}
	case TypeHTTP:
		if e.Endpoint == "" {
			return Spec{}, errors.New("http MCP requires endpoint")
		}
	case TypeStdio:
		if e.Command == "" {
			return Spec{}, errors.New("stdio MCP requires command")
		}
	default:
		return Spec{}, fmt.Errorf("%w: %q", ErrUnsupportedType, e.Type)
	}

	return spec, nil
}
