// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package mcps

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/achetronic/baifo/internal/version"
)

// TestConnectionResult is the outcome of a TestConnection call.
// Fields are populated best-effort: ServerName / ServerVersion may
// be empty if the server doesn't supply InitializeResult.ServerInfo
// (it's optional per the MCP spec), and ToolCount is 0 when the
// server returns no tools.
type TestConnectionResult struct {
	MCPName       string
	ServerName    string
	ServerVersion string
	ToolCount     int
	Elapsed       time.Duration
}

// TestConnection opens a fresh MCP session against the named
// server and lists its tools as a smoke test. Returns a populated
// TestConnectionResult on success or a descriptive error on
// failure. Built-in MCPs short-circuit to "always healthy"
// because they live in-process — there's nothing remote to test.
//
// We use a deliberate 15 second deadline so a hung MCP doesn't
// freeze the TUI; that's enough for one round trip through OAuth
// + protocol initialise + ListTools on any reasonable network.
func (r *Registry) TestConnection(ctx context.Context, name string) (*TestConnectionResult, error) {
	spec, err := r.Spec(name)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	switch spec.Type {
	case TypeBuiltin:
		return &TestConnectionResult{
			MCPName: name,
			Elapsed: time.Since(start),
		}, nil

	case TypeHTTP, TypeStdio:
		// Both transports go through Toolset which already does
		// the right thing wrt headers, OAuth, secret expansion,
		// and stdio process spawn. We don't need the toolset
		// itself; we just need its underlying client session,
		// which Toolset constructs lazily. So instead of going
		// through ADK's toolset, build the transport ourselves
		// (it's the SAME transport Toolset uses) and connect.
		transport, closer, err := r.testTransport(spec)
		if err != nil {
			return nil, err
		}
		if closer != nil {
			defer closer()
		}

		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		client := mcp.NewClient(&mcp.Implementation{
			Name:       brandingName,
			Version:    version.Tag(),
			WebsiteURL: brandingHomeURL,
		}, nil)
		session, err := client.Connect(callCtx, transport, nil)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		defer func() { _ = session.Close() }()

		// ListTools doubles as a protocol-level ping: a server
		// that completed initialize but can't ListTools is
		// telling us something is wrong upstream.
		tools, err := session.ListTools(callCtx, nil)
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}

		out := &TestConnectionResult{
			MCPName:   name,
			ToolCount: len(tools.Tools),
			Elapsed:   time.Since(start),
		}
		if info := session.InitializeResult(); info != nil && info.ServerInfo != nil {
			out.ServerName = info.ServerInfo.Name
			out.ServerVersion = info.ServerInfo.Version
		}
		return out, nil

	default:
		return nil, fmt.Errorf("mcp %q: unsupported type %q", name, spec.Type)
	}
}

// testTransport builds the same transport Toolset() would
// internally — but returns it directly so TestConnection can own
// a fresh mcp.Client session without going through ADK's toolset
// wrapper. closer is non-nil when the transport owns side-
// effectful state (stdio process, listener) that TestConnection
// must release.
func (r *Registry) testTransport(spec Spec) (mcp.Transport, func(), error) {
	switch spec.Type {
	case TypeHTTP:
		if spec.Endpoint == "" {
			return nil, nil, fmt.Errorf("mcp %q: http transport requires endpoint", spec.Name)
		}
		headers, err := expandHeaders(spec.Headers, r.secrets)
		if err != nil {
			return nil, nil, fmt.Errorf("mcp %q: expand headers: %w", spec.Name, err)
		}
		client := httpClientForMCP(headers, spec.Insecure)
		lookup := func(secName string) (string, error) {
			if r.secrets == nil {
				return "", fmt.Errorf("no secrets store configured")
			}
			return r.secrets.Get(secName)
		}
		oauthHandler, err := buildOAuthHandler(spec, lookup, r.tokens, r.clients)
		if err != nil {
			return nil, nil, fmt.Errorf("mcp %q: oauth handler: %w", spec.Name, err)
		}
		return &mcp.StreamableClientTransport{
			Endpoint:     spec.Endpoint,
			HTTPClient:   client,
			MaxRetries:   0, // fail fast during the smoke test
			OAuthHandler: oauthHandler,
		}, nil, nil
	case TypeStdio:
		// Stdio path is straightforward: spawn the process and
		// let the SDK handle the pipes. CommandTransport closes
		// the child on session.Close so we don't need a
		// separate closer here.
		env, err := expandHeaders(spec.Env, r.secrets)
		if err != nil {
			return nil, nil, fmt.Errorf("mcp %q: expand env: %w", spec.Name, err)
		}
		t := buildStdioTransport(spec, env)
		return t, nil, nil
	}
	return nil, nil, fmt.Errorf("mcp %q: unsupported type %q", spec.Name, spec.Type)
}
