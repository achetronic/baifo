// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package mcps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/version"
)

// AuthenticateOptions tweaks how Authenticate behaves. Zero value
// is the default ("reuse cached token if possible, prompt only
// when needed"). The Force flag is the only knob we expose today;
// add more here when the need arises rather than growing the
// Authenticate signature.
type AuthenticateOptions struct {
	// Force forgets any cached token + DCR client registration
	// and runs the full interactive flow from scratch. Used by
	// the `/mcps auth NAME --force` CLI path when the user
	// suspects a stuck credential.
	Force bool
}

// AuthenticateResult is what Authenticate returns on a successful
// OAuth flow. Today it carries only the MCP name plus a flag
// telling the caller whether we actually opened a browser or
// reused the cached token; the TUI uses it to print a precise
// chat row.
type AuthenticateResult struct {
	MCPName    string
	Reused     bool // true when an existing cached token was still valid
	ServerHint string
}

// Authenticate kicks the OAuth dance for the named MCP. The
// behaviour depends on auth.kind and credentials configured:
//
//   - client_credentials: the SDK handler picks up a 401 challenge,
//     fetches a fresh token at the discovered token endpoint, and
//     persists it. No user interaction needed.
//
//   - authorization_code (DCR / CIMD / preregistered): the SDK
//     handler's AuthorizationCodeFetcher is wired to open the
//     user's browser and spin a local server to receive the
//     callback. We first try to reuse a token persisted from a
//     previous run; if that works the call returns immediately
//     with Reused=true.
//
// The "kick" is a real MCP Initialize round trip — we connect a
// fresh mcp.Client against the transport, which triggers the
// SDK's auth machinery on the first 401/403. Doing it through
// the real protocol (instead of a synthetic HTTP GET) means a
// well-behaved MCP that authenticates at the JSON-RPC layer
// (i.e. inside Initialize, not on every HTTP method) still
// reaches the OAuth flow.
//
// Authenticate is single-shot: when it returns, either a token
// has been persisted or an error explains why not. Callers
// should treat it as blocking for the duration of the round
// trip (seconds in the service flow, up to ~minute in the
// interactive one).
func (r *Registry) Authenticate(ctx context.Context, name string, opts AuthenticateOptions) (*AuthenticateResult, error) {
	spec, err := r.Spec(name)
	if err != nil {
		return nil, err
	}
	if spec.Type != TypeHTTP {
		return nil, fmt.Errorf("mcp %q: authenticate only supported for http transport", name)
	}
	if spec.Auth.EffectiveKind() != config.MCPAuthKindOAuth {
		return nil, fmt.Errorf("mcp %q: auth.kind is %q, not oauth", name, spec.Auth.EffectiveKind())
	}

	// --force: wipe cached token + DCR client BEFORE wiring the
	// handler so the SDK can't reuse anything from previous boots.
	if opts.Force {
		if r.tokens != nil {
			_ = r.tokens.Delete(name)
		}
		if r.clients != nil {
			_ = r.clients.Delete(name)
		}
	} else if r.tokens != nil {
		// Fast path: do we already have a non-expired token?
		// If yes, skip the whole flow entirely.
		if tok, _ := r.tokens.Load(name); tok != nil && tok.Valid() {
			return &AuthenticateResult{MCPName: name, Reused: true}, nil
		}
	}

	lookup := func(secName string) (string, error) {
		if r.secrets == nil {
			return "", fmt.Errorf("no secrets store configured")
		}
		return r.secrets.Get(secName)
	}

	// Service-to-service is the same handler the data plane
	// uses. The SDK will Authorize it on the first 401 the MCP
	// returns to the Initialize call.
	if spec.Auth.ClientID != "" && spec.Auth.ClientSecretRef != "" {
		handler, err := buildOAuthHandler(spec, lookup, r.tokens, r.clients)
		if err != nil {
			return nil, err
		}
		if err := initializeWithHandler(ctx, r, spec, handler); err != nil {
			return nil, err
		}
		return &AuthenticateResult{MCPName: name}, nil
	}

	// User-interactive: bind the loopback server FIRST so we
	// know the redirect URI before talking to DCR. The fetcher
	// closes over the listener and serves /callback on it.
	listener, redirectURL, err := bindLoopback()
	if err != nil {
		return nil, err
	}
	defer listener.Close()

	fetcher := interactiveFetcher(ctx, listener)
	cfg, err := authorizationCodeConfig(spec, r.clients, fetcher)
	if err != nil {
		return nil, err
	}
	// Pin the redirect_uri to the loopback URL bindLoopback
	// actually picked. The DCR metadata pool already lists
	// every candidate (cimdRedirectURIs), so leaving it
	// untouched lets the AS exact-match this specific URI
	// against the list without rejecting it. For DCR (where
	// the AS only sees what we PUT in the registration
	// request), the SDK uses cfg.RedirectURL as the request
	// `redirect_uri`, so the AS will exact-match it too.
	cfg.RedirectURL = redirectURL

	h, err := auth.NewAuthorizationCodeHandler(cfg)
	if err != nil {
		return nil, fmt.Errorf("authorization_code handler: %w", err)
	}
	handler := wrapPersistent(h, spec.Name, r.tokens)

	if err := initializeWithHandler(ctx, r, spec, handler); err != nil {
		return nil, err
	}

	// Persist whichever client credentials the SDK ended up
	// using (cached or freshly registered). If the cached path
	// fired we re-persist the same values (no-op); if DCR
	// registered a new client, this is when we capture it.
	persistRegisteredClient(spec, cfg, r.clients)

	return &AuthenticateResult{MCPName: name}, nil
}

// initializeWithHandler opens a fresh MCP client session against
// the named MCP's transport, using the supplied OAuth handler.
// The SDK's StreamableClientTransport handles the auth dance
// automatically: when ANY request returns 401/403, the transport
// calls handler.Authorize() (which fires our interactive fetcher
// for the user-flow case), waits for the token, and retries.
//
// After Connect (which sends Initialize) we follow up with a
// ListTools call. Some servers — Magnific is the canonical
// example — answer Initialize anonymously and only enforce auth
// when you start asking for the protocol surface. Without the
// follow-up call, "authenticate" would silently succeed while
// the user never saw a browser.
//
// We discard the session as soon as the round trip completes —
// this function is about ensuring the OAuth flow ran end-to-end,
// not about using the MCP for tools. The token persistence side
// effect happens inside the wrapped handler.
func initializeWithHandler(ctx context.Context, r *Registry, spec Spec, handler auth.OAuthHandler) error {
	headers, err := expandHeaders(spec.Headers, r.secrets)
	if err != nil {
		return fmt.Errorf("expand headers: %w", err)
	}
	httpClient := httpClientForMCP(headers, spec.Insecure)

	transport := &mcp.StreamableClientTransport{
		Endpoint:     spec.Endpoint,
		HTTPClient:   httpClient,
		MaxRetries:   0, // fail fast — Authorize itself owns the retry
		OAuthHandler: handler,
	}

	// Healthy deadline: the interactive fetcher caps itself at
	// 2 minutes, plus a bit for the initial Initialize and the
	// token exchange. Service-to-service flows complete in
	// well under a second.
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{
		Name:       brandingName,
		Version:    version.Tag(),
		WebsiteURL: brandingHomeURL,
	}, nil)
	session, err := client.Connect(callCtx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = session.Close() }()

	// Force a second round trip so servers that defer auth
	// past Initialize still trip the OAuth flow. We don't care
	// about the result — its only purpose is to provoke a
	// 401/403 that the transport hands to handler.Authorize().
	if _, err := session.ListTools(callCtx, nil); err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	return nil
}

// bindLoopback returns a TCP listener on 127.0.0.1 plus the
// absolute redirect URL the OAuth server should redirect to
// once consent is granted.
//
// We try the well-known CIMD port pool first (see
// brandingCallbackPorts) so the URL we hand the AS is one of
// the redirect_uris listed in our published Client ID Metadata
// Document. RFC 9700 / RFC 9728 require exact-string match on
// redirect_uri, so the AS only accepts URIs that the metadata
// pre-declared.
//
// If every port in the pool is busy (eight concurrent OAuth
// flows? rare) we fall back to an ephemeral port. That breaks
// the CIMD contract for the duration of that flow, so the AS
// will reject the redirect — but it lets DCR-only ASs still
// work because DCR registrations carry whatever
// redirect_uri the SDK sends at registration time.
func bindLoopback() (net.Listener, string, error) {
	for _, port := range brandingCallbackPorts {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return listener, loopbackRedirectURL(port), nil
		}
		// Port in use → try the next one. Any non-bind error
		// (permission denied on a privileged port, listener
		// limits, …) is worth surfacing right away.
		if !isAddrInUse(err) {
			return nil, "", fmt.Errorf("bind loopback :%d: %w", port, err)
		}
	}

	// Pool exhausted — last resort, ephemeral port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("bind loopback: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		if closeErr := listener.Close(); closeErr != nil {
			slog.Warn("failed to close listener after bad address type", "error", closeErr)
		}
		return nil, "", fmt.Errorf("bind loopback: unexpected address type %T", listener.Addr())
	}
	return listener, loopbackRedirectURL(addr.Port), nil
}

// isAddrInUse reports whether err is a "port already in use"
// error. We branch on the substring rather than the typed error
// to stay portable across OSes (errno names differ between
// linux, darwin and windows).
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"address already in use", "bind: address already in use", "Only one usage of each socket address"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// interactiveFetcher returns an AuthorizationCodeFetcher that
// opens the user's browser and serves the /callback endpoint on
// the supplied listener. The listener was bound by bindLoopback
// before this fetcher was constructed so the redirect URI is
// already known to DCR by authorize time.
//
// Two-minute deadline so a forgotten browser tab doesn't hang
// baifo forever. Cancellation also propagates from ctx.
func interactiveFetcher(parentCtx context.Context, listener net.Listener) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		type result struct {
			code  string
			state string
			err   error
		}
		ch := make(chan result, 1)

		mux := http.NewServeMux()
		mux.HandleFunc(brandingCallbackPath, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if errStr := q.Get("error"); errStr != "" {
				ch <- result{err: fmt.Errorf("authorization server returned %q (%s)",
					errStr, q.Get("error_description"))}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("Authorization failed. You can close this tab and return to " + brandingName + "."))
				return
			}
			code := q.Get("code")
			state := q.Get("state")
			if code == "" {
				ch <- result{err: errors.New("authorization callback missing code parameter")}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("Authorization failed. You can close this tab and return to " + brandingName + "."))
				return
			}
			ch <- result{code: code, state: state}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Authorization complete. You can close this tab and return to " + brandingName + "."))
		})

		server := &http.Server{Handler: mux}
		go func() { _ = server.Serve(listener) }()
		defer func() { _ = server.Shutdown(context.Background()) }()

		// Best-effort open of the user's browser. On environments
		// where openBrowser fails (CI, SSH session without DISPLAY),
		// the URL is still resolvable via args.URL if the caller
		// chooses to print it.
		_ = openBrowser(args.URL)

		// Honour both the inner-context cancellation (from the
		// SDK) and the parent context that baifo owns.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("authorization cancelled: %w", ctx.Err())
		case <-parentCtx.Done():
			return nil, fmt.Errorf("authorization cancelled: %w", parentCtx.Err())
		case <-time.After(2 * time.Minute):
			return nil, errors.New("authorization timed out after 2 minutes")
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			return &auth.AuthorizationResult{Code: r.code, State: r.state}, nil
		}
	}
}

// persistRegisteredClient captures the credentials the SDK ended
// up using after a successful Authorize and writes them to the
// DCRClientStore so the next boot reuses them.
//
// We pull the credentials from cfg.PreregisteredClient: the SDK
// populates that field with the cached creds when one was
// supplied; when DCR registered a new client, the SDK ALSO sets
// PreregisteredClient on the cfg as part of its internal
// normalisation (see auth/authorization_code.go > performDCR).
//
// When the SDK didn't manage to identify a client (the AS didn't
// require one, or the field didn't get populated), we silently
// no-op: there's nothing useful to persist.
func persistRegisteredClient(spec Spec, cfg *auth.AuthorizationCodeHandlerConfig, clients *DCRClientStore) {
	if clients == nil || cfg == nil || cfg.PreregisteredClient == nil {
		return
	}
	c := cfg.PreregisteredClient
	if c.ClientID == "" {
		return
	}
	secret := ""
	if c.ClientSecretAuth != nil {
		secret = c.ClientSecretAuth.ClientSecret
	}
	_ = clients.Save(spec.Name, &PersistedDCRClient{
		ClientID:     c.ClientID,
		ClientSecret: secret,
	})
}

// openBrowser opens the given URL using the OS-native command. We
// hand-roll the OS detection because the project already minimises
// dependencies; the matrix is small (linux/mac/windows) and stable.
func openBrowser(rawURL string) error {
	if _, err := url.Parse(rawURL); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// ClearAuth drops every cached credential baifo holds for the
// named MCP — the access/refresh token in TokenStore AND the
// Dynamic Client Registration credentials in DCRClientStore. The
// next Authenticate (or first agent message that touches the
// MCP) will start from scratch: re-register a client if DCR is
// needed, open the browser for the user, mint a fresh token.
//
// Used by `/mcps logout NAME` and the Settings "Forget
// credentials" action when an authorisation gets stuck (consent
// revoked upstream, AS rotated keys, refresh token poisoned).
//
// Returns nil even when nothing was cached — the operation is
// idempotent on purpose; the user just wants "no creds left",
// not a report.
func (r *Registry) ClearAuth(name string) error {
	// Cheap sanity check so a typoed name doesn't silently
	// succeed: the MCP must exist in the registry. Beyond that
	// we don't care what type it is — clearing the buckets is
	// safe even for builtins, which never have entries.
	if _, err := r.Spec(name); err != nil {
		return err
	}
	if r.tokens != nil {
		if err := r.tokens.Delete(name); err != nil {
			return fmt.Errorf("delete token: %w", err)
		}
	}
	if r.clients != nil {
		if err := r.clients.Delete(name); err != nil {
			return fmt.Errorf("delete dcr client: %w", err)
		}
	}
	return nil
}
