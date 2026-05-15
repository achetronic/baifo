// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package mcps

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"

	"github.com/achetronic/baifo/internal/secrets"
)

// Toolset returns a lazily-connected ADK Toolset for the named MCP.
// Use this for HTTP and stdio transports; builtin MCPs go through
// Tools() instead because they are in-process and don't need the
// MCP wire protocol.
//
// The toolset connects to the server lazily on the first ListTools
// call (driven by the agent runtime). That means boot stays fast
// even when an MCP server is slow or unreachable; the failure
// surfaces at the user's first turn instead, with a clear "MCP
// server X is unreachable" message coming back from the model loop.
//
// We do NOT cache the toolset between calls. Each Toolset() returns
// a fresh instance so reload events (config edits, secret rotation)
// produce a brand-new client with the updated transport. The MCP
// client itself memoises its connection internally, so this is not
// a correctness issue, only a small allocation.
func (r *Registry) Toolset(name string) (tool.Toolset, error) {
	spec, err := r.Spec(name)
	if err != nil {
		return nil, err
	}
	switch spec.Type {
	case TypeBuiltin:
		// Builtin MCPs are in-process; we expose them as a thin
		// wrapper around the existing tool list so callers can use
		// Toolset() uniformly. Returns ErrUnsupportedBuiltin if the
		// slug isn't one of the known ones.
		tools, err := r.builtinTools(spec)
		if err != nil {
			return nil, err
		}
		return &staticToolset{name: "mcp:" + spec.Name, tools: tools}, nil

	case TypeHTTP:
		if spec.Endpoint == "" {
			return nil, fmt.Errorf("mcp %q: http transport requires endpoint", name)
		}
		headers, err := expandHeaders(spec.Headers, r.secrets)
		if err != nil {
			return nil, fmt.Errorf("mcp %q: expand headers: %w", name, err)
		}
		client := httpClientForMCP(headers, spec.Insecure)

		// Build the OAuth handler when the user opted in. We pass
		// the secrets lookup so client_credentials can dereference
		// client_secret_ref at handler-construction time — same
		// secrets store the headers use.
		lookup := func(secName string) (string, error) {
			if r.secrets == nil {
				return "", fmt.Errorf("no secrets store configured")
			}
			return r.secrets.Get(secName)
		}
		oauthHandler, err := buildOAuthHandler(spec, lookup, r.tokens, r.clients)
		if err != nil {
			return nil, fmt.Errorf("mcp %q: oauth handler: %w", name, err)
		}

		transport := &mcp.StreamableClientTransport{
			Endpoint:     spec.Endpoint,
			HTTPClient:   client,
			MaxRetries:   5,
			OAuthHandler: oauthHandler,
		}
		ts, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
		if err != nil {
			return nil, fmt.Errorf("mcp %q: build http toolset: %w", name, err)
		}
		return ts, nil

	case TypeStdio:
		if spec.Command == "" {
			return nil, fmt.Errorf("mcp %q: stdio transport requires command", name)
		}
		env, err := expandHeaders(spec.Env, r.secrets)
		if err != nil {
			return nil, fmt.Errorf("mcp %q: expand env: %w", name, err)
		}
		transport := buildStdioTransport(spec, env)
		ts, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
		if err != nil {
			return nil, fmt.Errorf("mcp %q: build stdio toolset: %w", name, err)
		}
		return ts, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, spec.Type)
	}
}

// IsExternal reports whether the named MCP uses an external transport
// (http or stdio). Used by the agent builder to decide between the
// Tools() and Toolset() paths.
func (r *Registry) IsExternal(name string) (bool, error) {
	spec, err := r.Spec(name)
	if err != nil {
		return false, err
	}
	return spec.Type == TypeHTTP || spec.Type == TypeStdio, nil
}

// secretPlaceholderRE matches the same syntax the secrets pipeline
// understands: ${secret:NAME}. Duplicated here (instead of imported
// from internal/secrets) so the registry doesn't take a hard
// dependency on the secrets package's private helpers — only on its
// Store interface.
var secretPlaceholderRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9_\-]+)\}`)

// expandHeaders walks a map and replaces every ${secret:NAME}
// placeholder in the VALUES with the real secret. Keys are taken
// literally (no expansion). Missing secrets surface as errors so a
// typo in baifo.yaml doesn't silently send a request with the
// literal "${secret:foo}" header value.
//
// When store is nil (encryption_key not set), placeholders pass
// through unchanged — same posture as the rest of baifo: secrets are
// optional, so a config that doesn't reference any still boots.
func expandHeaders(in map[string]string, store *secrets.Store) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if store == nil || !secretPlaceholderRE.MatchString(v) {
			out[k] = v
			continue
		}
		var resolveErr error
		expanded := secretPlaceholderRE.ReplaceAllStringFunc(v, func(match string) string {
			name := secretPlaceholderRE.FindStringSubmatch(match)[1]
			value, err := store.Get(name)
			if err != nil {
				resolveErr = fmt.Errorf("resolve secret %q: %w", name, err)
				return match
			}
			return value
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		out[k] = expanded
	}
	return out, nil
}

// httpClientForMCP returns an http.Client that injects the configured
// headers into every outgoing request and (optionally) skips TLS
// verification. Every client transparently carries the branded
// User-Agent (see branding.go) so MCP server operators can
// recognise us in their access logs.
//
// The default client is reused when no customisation is needed,
// to avoid allocating per call — but we always wrap with the
// User-Agent transport because http.DefaultClient does not
// advertise any.
func httpClientForMCP(headers map[string]string, insecure bool) *http.Client {
	base := http.RoundTripper(boundedMCPTransport(insecure))
	// Always wrap with the branded User-Agent transport. The
	// headerTransport (when present) sits on top so the user's
	// explicit headers still win.
	base = withUserAgent(base)
	if len(headers) == 0 {
		return &http.Client{Transport: base}
	}
	return &http.Client{
		Transport: &headerTransport{base: base, headers: headers},
	}
}

// MCP connection-phase timeouts. These bound how long we wait to
// ESTABLISH a connection and receive response HEADERS — they do NOT
// cap the response body, so a Streamable MCP that holds the
// connection open to stream events is unaffected. The point is to
// turn an unreachable or unresponsive endpoint (a dead host, a wrong
// URL, an OAuth wall that never answers) into a prompt error instead
// of a hang. Without these, ListTools on a bad MCP blocks the whole
// agent turn — and because the root sees every MCP by default, one
// bad entry in baifo.yaml silently freezes the coordinator.
const (
	mcpDialTimeout           = 8 * time.Second
	mcpTLSHandshakeTimeout   = 8 * time.Second
	mcpResponseHeaderTimeout = 20 * time.Second
)

// boundedMCPTransport returns an *http.Transport with connection and
// response-header timeouts set, cloned from the stdlib defaults so we
// keep sane connection pooling. insecure flips TLS verification off
// (still bounded by the same timeouts).
//
// We deliberately do NOT set http.Client.Timeout: that caps the whole
// request including the streamed body, which would sever long-lived
// Streamable-HTTP MCP sessions mid-flight. Bounding only the dial /
// TLS / header phases gives us fast failure on dead endpoints while
// letting healthy streams run as long as they like.
func boundedMCPTransport(insecure bool) *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t = &http.Transport{}
	}
	tr := t.Clone()
	tr.DialContext = (&net.Dialer{
		Timeout:   mcpDialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	tr.TLSHandshakeTimeout = mcpTLSHandshakeTimeout
	tr.ResponseHeaderTimeout = mcpResponseHeaderTimeout
	if insecure {
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
	}
	return tr
}

// headerTransport is an http.RoundTripper that adds a fixed set of
// headers to every outgoing request. Used to forward the user's
// `headers:` block from baifo.yaml (Authorization, X-Tenant-ID, ...)
// to the MCP server transparently.
//
// Header values are NOT expanded for ${secret:NAME} here — the
// expansion happens at config-load time. By the time the transport
// sees them, the values are already plain text. Future improvement:
// late-bind so token refresh doesn't require a reconnect.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Clone the request so we don't mutate the caller's; this is the
	// contract every well-behaved RoundTripper follows.
	rc := r.Clone(r.Context())
	for k, v := range t.headers {
		rc.Header.Set(k, v)
	}
	return t.base.RoundTrip(rc)
}

// staticToolset adapts a fixed []tool.Tool into the tool.Toolset
// interface. Used by Toolset() to make the builtin path
// interface-compatible with the external one.
type staticToolset struct {
	name  string
	tools []tool.Tool
}

func (s *staticToolset) Name() string { return s.name }
func (s *staticToolset) Tools(_ agent.ReadonlyContext) ([]tool.Tool, error) {
	return s.tools, nil
}

// buildStdioTransport returns the SDK transport for a stdio MCP.
// Pulled out as a helper so both the toolset path (Toolset) and
// the test-connection path (TestConnection) build the SAME
// transport without duplicating the env-merge / workdir logic.
func buildStdioTransport(spec Spec, env map[string]string) *mcp.CommandTransport {
	cmd := exec.Command(spec.Command, spec.Args...)
	if spec.Workdir != "" {
		cmd.Dir = spec.Workdir
	}
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	return &mcp.CommandTransport{Command: cmd}
}
