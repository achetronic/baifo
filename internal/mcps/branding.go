// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package mcps

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/achetronic/baifo/internal/version"
)

// branding.go centralises every user-visible identity string we
// send out as an MCP client. When the product gets renamed (today
// the working name is "Magec Lite" in user-facing surfaces;
// internally the binary is still called baifo), this is the only
// file that should change.
//
// The four surfaces we identify on are:
//
//   - User-Agent header on every outgoing HTTP request to MCP
//     servers and OAuth authorisation servers, so operators can
//     see in their access logs WHO is hitting their endpoints.
//   - OAuth client_name + client_uri in Dynamic Client
//     Registration so the consent screen (when the user
//     authorises a new MCP) shows a recognisable brand.
//   - The loopback redirect URI path. Using a distinctive path
//     (instead of the generic /callback) makes it obvious in
//     the user's address bar what just took their browser
//     hostage during the auth flow.
//   - The mcp.Implementation block we ship inside Initialize so
//     servers can log which client connected.
//
// Version stamping lives in internal/version (single source of
// truth for build metadata). Renaming the product is a one-file
// change here; rebranding the binary version is a one-file
// change there.
const (
	// brandingName is the user-facing product name. It rides
	// inside the User-Agent and the OAuth ClientName.
	brandingName = "Magec Lite"

	// brandingHomeURL is the URL the brand publishes itself at,
	// surfaced to authorisation servers via the OAuth
	// ClientURI metadata. Users see it on the consent screen.
	brandingHomeURL = "https://lite.magec.dev"

	// brandingCallbackPath is the loopback redirect path
	// embedded in every dynamic registration request. Stays
	// generic — the distinctive identity already lives in the
	// User-Agent and the OAuth ClientName, so the path doesn't
	// need to carry product trivia and a plain /callback reads
	// cleaner in the user's address bar during the auth flow.
	brandingCallbackPath = "/callback"

	// brandingCIMDURL is where the OAuth Client ID Metadata
	// Document (draft-ietf-oauth-client-id-metadata-document)
	// is published. The URL itself acts as Magec Lite's
	// permanent OAuth client_id when talking to authorisation
	// servers that support CIMD.
	//
	// The actual JSON lives in cimd/cimd.json in the repository
	// and must be served (HTTPS, 200 OK, CORS-open) at this
	// exact URL. See cimd/README.md for hosting requirements.
	//
	// CRITICAL: once published, this URL is immutable. Every
	// AS that has registered us stores it as our identity; a
	// 404 here invalidates every existing authorisation.
	brandingCIMDURL = "https://lite.magec.dev/oauth/cimd.json"
)

// brandingCallbackPorts is the pool of loopback ports declared
// in the CIMD redirect_uris list. The CLI tries them in order
// and uses the first one that bind()s — that exact URI is then
// passed to the authorisation server, which exact-matches it
// against the pool from the metadata document (RFC 9700 §4.5).
//
// Keep this list in lockstep with the `redirect_uris` array in
// cimd/cimd.json — they're TWO sides of the same contract and
// must agree byte-for-byte or the AS rejects the flow.
//
// Eight ports is generous for our usage: even running a couple
// of concurrent /mcps auth flows is rare, eight overlapping
// flows is impossible without the user being on something
// else.
var brandingCallbackPorts = []int{
	33001, 33002, 33003, 33004,
	33005, 33006, 33007, 33008,
}

// userAgent returns the value to set on the User-Agent header
// for every outgoing request to MCP servers (data plane and
// OAuth control plane alike). Shape: "Magec Lite/<version>" —
// minimal, brand + version, no parenthesised URL trail. The
// home URL is published via OAuth client metadata (ClientURI)
// where operators expect it.
func userAgent() string {
	return fmt.Sprintf("%s/%s", brandingName, version.Tag())
}

// dcrClientName returns the human-readable name the OAuth
// authorisation server uses when prompting the user for consent.
// We keep it as the bare brand: an operator who runs many MCPs
// against the same AS sees "Magec Lite" once on every consent
// screen, which is exactly what they expect — distinguishing
// per-MCP is the AS's job (different client_id, different
// audience), not a job for the visible name.
func dcrClientName() string {
	return brandingName
}

// loopbackRedirectURL composes the absolute redirect URL from
// the listener's chosen port and the branded callback path. The
// authorisation server will redirect the browser here once
// consent is granted.
func loopbackRedirectURL(port int) string {
	u := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", port),
		Path:   brandingCallbackPath,
	}
	return u.String()
}

// cimdRedirectURIs returns the full redirect_uris list that
// MATCHES the CIMD JSON published at brandingCIMDURL. Used as
// the AuthorizationCodeHandlerConfig.RedirectURL allow-list so
// the SDK's exact-match validation (RFC 9700 §4.5) accepts
// whichever port bindLoopback ended up picking.
func cimdRedirectURIs() []string {
	out := make([]string, 0, len(brandingCallbackPorts))
	for _, p := range brandingCallbackPorts {
		out = append(out, loopbackRedirectURL(p))
	}
	return out
}

// withUserAgent wraps base so every request carries the branded
// User-Agent. Headers explicitly set by baifo.yaml win (the user
// is the boss), but a missing User-Agent gets ours injected.
//
// Designed to be composable with headerTransport: when both are
// in play, headerTransport stays the outer layer (it sees the
// User-Agent we set inside) and the user's headers can still
// override the brand if they really want to.
type uaTransport struct{ base http.RoundTripper }

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		// Clone before mutating: stdlib contract for RoundTrippers.
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", userAgent())
	}
	return t.base.RoundTrip(r)
}

func withUserAgent(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &uaTransport{base: base}
}
