// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package mcps

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/auth/extauth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/achetronic/baifo/internal/config"
)

// buildOAuthHandler returns the auth.OAuthHandler the MCP transport
// should use, based on the spec's Auth block. Returns nil when the
// spec uses kind=none (no OAuth) so callers can fall back to the
// plain HTTP client.
//
// The matrix we implement, in order of precedence:
//
//   - kind=oauth + ClientID + ClientSecretRef: client_credentials
//     grant. Service-to-service, no user interaction.
//   - kind=oauth + nothing set: AuthorizationCodeHandler. We try
//     to reuse a previously-registered DCR client (from
//     DCRClientStore) so authentication on the second+ boot
//     skips the registration round trip. If no client is cached
//     we fall back to fresh DCR.
//
// The data-plane handler returned here does NOT spin a browser —
// that's wired by buildInteractiveHandler in authenticate.go. It
// only needs to refresh existing tokens, which the SDK does
// transparently when given a token source pre-loaded with the
// persisted refresh token.
func buildOAuthHandler(spec Spec, secretsLookup secretLookup, tokens *TokenStore, clients *DCRClientStore) (auth.OAuthHandler, error) {
	if spec.Auth.EffectiveKind() != config.MCPAuthKindOAuth {
		return nil, nil
	}

	// Service-to-service: client_credentials grant.
	if spec.Auth.ClientID != "" && spec.Auth.ClientSecretRef != "" {
		clientSecret, err := secretsLookup(spec.Auth.ClientSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve client_secret_ref: %w", err)
		}
		creds := &oauthex.ClientCredentials{
			ClientID: spec.Auth.ClientID,
			ClientSecretAuth: &oauthex.ClientSecretAuth{
				ClientSecret: clientSecret,
			},
		}
		h, err := extauth.NewClientCredentialsHandler(&extauth.ClientCredentialsHandlerConfig{
			Credentials: creds,
		})
		if err != nil {
			return nil, fmt.Errorf("client_credentials handler: %w", err)
		}
		return wrapPersistent(h, spec.Name, tokens), nil
	}

	// User-interactive on the data plane: we can't open a browser
	// here (the agent is running, no operator typing), so the
	// best we can do is expose the cached token (if any) and
	// let the SDK transport surface a 401 if the cache is empty
	// or stale. The interactive Authenticate() path is where
	// the fetcher actually runs.
	//
	// readOnlyAuthHandler implements OAuthHandler with a
	// TokenSource that reads from the persisted store and an
	// Authorize() that errors out — there's literally nobody
	// to authorize against on the data plane.
	return &readOnlyAuthHandler{
		mcpName: spec.Name,
		tokens:  tokens,
	}, nil
}

// readOnlyAuthHandler is the data-plane equivalent of an
// AuthorizationCodeHandler. It exposes whatever token is on disk
// as the bearer credential and refuses to start a fresh OAuth
// flow when the transport hits a 401/403 — the agent runtime is
// not a place to pop browsers. The user is expected to have run
// /mcps authenticate (or the equivalent Settings action)
// beforehand, and the resulting token survives reboots via the
// TokenStore wrapper.
//
// When the cached token has a refresh_token, oauth2's static
// source layered with the wrapped store still works for one
// request; for proper transparent refresh we'd need to know the
// token endpoint here, but on the data plane that's owned by
// the SDK transport machinery which the handler interface
// doesn't expose. The pragmatic shape: this handler keeps
// returning the cached token; if it expires, the next request
// surfaces a "token expired — run /mcps auth NAME" error.
type readOnlyAuthHandler struct {
	mcpName string
	tokens  *TokenStore
}

func (h *readOnlyAuthHandler) TokenSource(_ context.Context) (oauth2.TokenSource, error) {
	if h.tokens == nil {
		return nil, nil
	}
	tok, err := h.tokens.Load(h.mcpName)
	if err != nil || tok == nil || tok.AccessToken == "" {
		return nil, nil
	}
	return oauth2.StaticTokenSource(tok), nil
}

func (h *readOnlyAuthHandler) Authorize(_ context.Context, _ *http.Request, _ *http.Response) error {
	return fmt.Errorf("mcp %q: cannot authenticate from the data plane; run /mcps auth %s to authorise interactively",
		h.mcpName, h.mcpName)
}

// authorizationCodeConfig assembles the AuthorizationCodeHandlerConfig
// shared by the data plane (no fetcher) and the interactive
// authenticate flow (fetcher non-nil).
//
// Client-registration precedence, in order of preference:
//
//  1. Cached DCR client (from DCRClientStore). If we've
//     registered a client with this AS before, we keep using
//     the same client_id/client_secret across reboots so the
//     AS doesn't accumulate zombie registrations. Becomes the
//     SDK's PreregisteredClient.
//  2. Client ID Metadata Document (CIMD). We publish a stable
//     JSON document at brandingCIMDURL describing Magec Lite;
//     the AS fetches that URL and uses the URL itself as our
//     client_id. Recommended by the MCP spec — SHOULD support.
//  3. Dynamic Client Registration (RFC 7591). The legacy
//     fallback for ASs that don't speak CIMD yet. The SDK
//     registers a fresh client on the fly using
//     defaultDCRMetadata(); we capture the resulting
//     credentials post-flow and persist them so the next boot
//     re-enters the precedence chain at step 1.
//
// The SDK tries the three options in the order documented in
// AuthorizationCodeHandlerConfig (Preregistered, then CIMD,
// then DCR), short-circuiting on the first one configured. We
// populate ALL THREE so the SDK can degrade gracefully
// regardless of what the specific AS supports.
//
// The redirect_uri picked by bindLoopback (the interactive
// flow's allocated port) is appended to cfg.RedirectURL and
// must already be present in the CIMD redirect_uris pool —
// otherwise the AS's exact-match validation (RFC 9700 §4.5)
// rejects the redirect.
func authorizationCodeConfig(spec Spec, clients *DCRClientStore, fetcher auth.AuthorizationCodeFetcher) (*auth.AuthorizationCodeHandlerConfig, error) {
	cfg := &auth.AuthorizationCodeHandlerConfig{
		AuthorizationCodeFetcher: fetcher,
	}

	// Resolve the registration mode first: it gates both what we
	// advertise (CIMD/DCR) and whether a cached client may be reused.
	// auth.registration (auto | cimd | dcr):
	//   - auto: advertise both; the MCP SDK picks CIMD when the AS
	//     supports it, else DCR.
	//   - cimd: advertise only the Client ID Metadata Document.
	//   - dcr:  advertise only Dynamic Client Registration. Use this
	//     when the AS announces CIMD support but rejects our client_id
	//     URL (domain not whitelisted), which would otherwise make the
	//     SDK pick CIMD and fail with no fallback.
	// See config.MCPAuth.Registration.
	mode := spec.Auth.EffectiveRegistration()
	advertiseCIMD := mode != config.MCPRegistrationDCR
	advertiseDCR := mode != config.MCPRegistrationCIMD

	// 1) Cached DCR client (highest precedence) — but ONLY when the
	// mode still permits DCR. A cached client is always the product of
	// a previous Dynamic Client Registration; reusing it when the user
	// has switched to registration=cimd would silently keep using the
	// old DCR client and make the mode look ignored. In cimd mode we
	// skip it so CIMD is what actually happens.
	if clients != nil && advertiseDCR {
		cached, err := clients.Load(spec.Name)
		if err != nil {
			return nil, fmt.Errorf("load cached dcr client: %w", err)
		}
		if cached != nil {
			creds := &oauthex.ClientCredentials{ClientID: cached.ClientID}
			if cached.ClientSecret != "" {
				creds.ClientSecretAuth = &oauthex.ClientSecretAuth{
					ClientSecret: cached.ClientSecret,
				}
			}
			cfg.PreregisteredClient = creds
		}
	}

	// 2) CIMD. The MCP SDK prefers it over DCR whenever this config is
	// set and the AS advertises support (see authorization_code.go
	// handleRegistration). The brand document at brandingCIMDURL must
	// be served (HTTPS, 200, CORS-open) for an AS to dereference our
	// client_id; if it is not live yet, an AS that supports CIMD will
	// reject the flow. That is why the per-MCP `dcr` override exists —
	// it is the operator's call, not a hidden global flag.
	if advertiseCIMD {
		cfg.ClientIDMetadataDocumentConfig = &auth.ClientIDMetadataDocumentConfig{
			URL: brandingCIMDURL,
		}
	}

	// 3) DCR. Populate Metadata and RedirectURL with the WHOLE port
	// pool so both CIMD's exact-match check (against the URL the AS
	// fetched from brandingCIMDURL) and DCR's exact-match check
	// (against what we PUT into the registration request) accept any
	// of the ports bindLoopback might end up picking. Skipped only
	// when the mode is "cimd".
	if advertiseDCR {
		dcr := defaultDCRMetadata()
		dcr.RedirectURIs = cimdRedirectURIs()
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{Metadata: dcr}
		cfg.RedirectURL = dcr.RedirectURIs[0]
	} else if advertiseCIMD {
		// CIMD-only: still need a redirect URL from the pool for the
		// SDK's exact-match validation.
		cfg.RedirectURL = cimdRedirectURIs()[0]
	}

	return cfg, nil
}

// secretLookup is the minimal interface oauth.go needs to resolve
// client_secret_ref values from the encrypted secrets store. We
// define a function type rather than coupling to *secrets.Store so
// the registry stays decoupled from the secrets package's API.
type secretLookup func(name string) (string, error)

// defaultDCRMetadata returns the ClientRegistrationMetadata baifo
// sends when it falls back to Dynamic Client Registration (RFC
// 7591). The redirect URI uses a loopback address with the
// hardcoded "default" port; the interactive flow REPLACES this
// list with a port-aware URI before authorize time (see
// authenticate.go > interactiveFetcher). The data-plane code
// never opens a browser, so the loopback URI here is only a
// placeholder needed by the SDK validator.
//
// The metadata is independent of which MCP we're authenticating
// against — every baifo instance registers the same client name
// (the brand) regardless of target. Per-MCP identity surfaces
// through the unique client_id the AS issues us, not through
// the human-visible name.
func defaultDCRMetadata() *oauthex.ClientRegistrationMetadata {
	return &oauthex.ClientRegistrationMetadata{
		ClientName:    dcrClientName(),
		ClientURI:     brandingHomeURL,
		RedirectURIs:  []string{loopbackRedirectURL(0)},
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
	}
}

// httpClientForOAuth returns the http.Client the OAuth handler uses
// for token / metadata requests. It is the SAME client that the MCP
// transport uses for the data plane: the handler may need to inject
// headers (Bearer, X-Tenant-ID) that baifo has already set up.
func httpClientForOAuth(headers map[string]string, insecure bool) *http.Client {
	return httpClientForMCP(headers, insecure)
}

// wrapPersistent attaches token-persistence to the given handler
// when a TokenStore is configured. The wrapper:
//
//  1. Pre-loads any token persisted from a previous boot at
//     TokenSource time so the SDK's token source has a starting
//     point (and can refresh it transparently against the AS).
//  2. Writes every refreshed token back to disk after Authorize.
//
// Returns the bare handler when tokens is nil so callers can use
// the helper unconditionally.
func wrapPersistent(inner auth.OAuthHandler, mcpName string, tokens *TokenStore) auth.OAuthHandler {
	if tokens == nil {
		return inner
	}
	return &persistentHandler{inner: inner, mcpName: mcpName, store: tokens}
}

// persistentHandler wraps an auth.OAuthHandler so the access token
// (and refresh token if present) survive a process restart. Two
// hooks:
//
//   - TokenSource: on first call we check the store for a
//     previously-issued token. If we have one and it's still
//     valid (or refreshable), we return a token source seeded
//     with it; otherwise we delegate to the inner handler.
//   - Authorize: forward to the inner handler, then snapshot
//     whatever token the source returns and persist it.
//
// The wrapper is intentionally tiny: we do not own the OAuth
// state machine, only the persistence layer around it.
type persistentHandler struct {
	inner   auth.OAuthHandler
	mcpName string
	store   *TokenStore

	mu        sync.Mutex
	preloaded oauth2.TokenSource // cached token-source built from disk; nil until first hit
	loaded    bool               // true after we've consulted the disk once
}

func (p *persistentHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	// Inner first: if the SDK already holds a fresh token (because
	// Authorize ran earlier in this process), prefer it over the
	// disk cache to avoid lock contention and stale refreshes.
	if ts, err := p.inner.TokenSource(ctx); err == nil && ts != nil {
		if tok, err := ts.Token(); err == nil && tok != nil && tok.AccessToken != "" {
			// Persist (it's idempotent) so first-boot tokens
			// land on disk even if the user never explicitly
			// re-authenticates.
			_ = p.store.Save(p.mcpName, tok)
			return ts, nil
		}
	}

	// Inner has nothing yet: try disk.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.preloaded != nil {
		return p.preloaded, nil
	}
	if p.loaded {
		// We already looked; nothing on disk. Return whatever
		// the inner handler had (likely nil).
		return p.inner.TokenSource(ctx)
	}
	p.loaded = true
	tok, err := p.store.Load(p.mcpName)
	if err != nil || tok == nil || tok.AccessToken == "" {
		return p.inner.TokenSource(ctx)
	}
	// Build a refreshing source from the persisted token. The
	// oauth2 package handles refresh transparently when the
	// access token expires, using the persisted refresh_token
	// against the AS's token endpoint. Note: we use a static
	// TokenSource here because we don't know the AS's token URL
	// at this layer — the SDK's inner source DOES, and once we
	// hand the token over via Authorize-by-401 it will refresh
	// from the right endpoint. Until then, oauth2.StaticTokenSource
	// is enough to satisfy the data-plane "have I got a bearer".
	p.preloaded = oauth2.StaticTokenSource(tok)
	return p.preloaded, nil
}

func (p *persistentHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	// Reset the disk cache: Authorize means the inner handler is
	// about to mint a new token and we want its result, not the
	// stale on-disk one.
	p.mu.Lock()
	p.preloaded = nil
	p.loaded = false
	p.mu.Unlock()

	if err := p.inner.Authorize(ctx, req, resp); err != nil {
		return err
	}
	ts, err := p.inner.TokenSource(ctx)
	if err != nil || ts == nil {
		return nil
	}
	tok, err := ts.Token()
	if err != nil {
		// Token retrieval can fail when the source is configured
		// but not primed yet; don't surface this as an Authorize
		// failure because the data-plane request can still succeed
		// with the in-memory token.
		return nil
	}
	if p.store != nil {
		_ = p.store.Save(p.mcpName, tok)
	}
	return nil
}
