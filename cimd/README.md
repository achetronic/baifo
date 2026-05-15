# CIMD — Magec Lite OAuth Client ID Metadata Document

This directory holds the JSON document that Magec Lite publishes
as its OAuth client identifier, per the draft
[draft-ietf-oauth-client-id-metadata-document](https://datatracker.ietf.org/doc/draft-ietf-oauth-client-id-metadata-document/)
spec.

## What is this?

CIMD ("Client ID Metadata Document") is an alternative to
[RFC 7591 Dynamic Client Registration](https://datatracker.ietf.org/doc/html/rfc7591).
Instead of registering a fresh `client_id` with every
authorisation server we touch, we publish *one* JSON document
at a stable HTTPS URL and use that URL itself as our `client_id`
in OAuth flows. Every authorisation server that understands
CIMD just fetches the URL, reads the metadata, and uses it to
authorise the OAuth round trip.

The MCP specification [recommends CIMD over DCR](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)
("SHOULD support" vs "MAY support") because it avoids the
zombie-client problem that DCR creates on the AS side: a fresh
`client_id` per user per device per re-install vs one stable id
for every install of Magec Lite, forever.

## Hosting requirements

Serve `cimd.json` from this directory at:

```
https://lite.magec.dev/oauth/cimd.json
```

The URL is hardcoded in `internal/mcps/branding.go` as
`brandingCIMDURL`. **Once published it MUST NOT change**: every
authorisation server we register with stores the URL as our
client identifier and a 404 there invalidates every existing
client. If we ever need to retire it, the path stays alive
forever and we ship a new URL alongside; deprecating CIMDs is
out of scope here.

Hosting requirements per the spec:

- **HTTPS only** (no plain HTTP).
- **HTTP 200 OK** response (any redirect / 3xx is rejected by
  conformant ASs).
- **Content-Type**: `application/json` (or
  `application/oauth-client-metadata+json`, both accepted).
- **CORS**: `Access-Control-Allow-Origin: *` so browser-based
  authorisation servers can fetch the document during a flow.
- **Cacheable**: a reasonable `Cache-Control: public, max-age=…`
  is fine; ASs cache aggressively. 1 hour is a good baseline.
- **The `client_id` field inside the JSON MUST EQUAL the URL
  the document is served from**, byte-for-byte. The spec uses
  this self-assertion as proof that whoever controls
  `lite.magec.dev` published the document.

GitHub Pages / Cloudflare Pages / Vercel are all fine. A
Cloudflare Pages deploy that maps `cimd/cimd.json` to
`/oauth/cimd.json` is the simplest path.

## Field-by-field rationale

| Field | Value | Why |
|---|---|---|
| `client_id` | `https://lite.magec.dev/oauth/cimd.json` | Self-asserting URL — must equal the URL the doc is served at. |
| `client_name` | `Magec Lite` | Shown on the consent screen when the user authorises a new MCP. Same brand we send in the User-Agent. |
| `client_uri` | `https://lite.magec.dev` | Click-through from the consent screen so users can verify the brand. |
| `logo_uri` | `https://lite.magec.dev/logo.png` | Optional but ASs that render it on the consent screen reduce phishing risk. Must be HTTPS. |
| `tos_uri` / `policy_uri` | … | Optional. Many ASs require them; publishing dummies is safer than omitting. |
| `application_type` | `native` | Per RFC 7591 §2: we're a desktop CLI with loopback redirect, not a hosted web app. Affects PKCE requirements (mandatory for native). |
| `grant_types` | `authorization_code`, `refresh_token` | The only grants Magec Lite uses today. |
| `response_types` | `code` | We do PKCE authorization code; never implicit. |
| `token_endpoint_auth_method` | `none` | Public client — we ship no `client_secret` in the binary, period. PKCE is what secures the token exchange instead. |
| `redirect_uris` | 8 × `http://127.0.0.1:330xx/callback` | A pool of well-known loopback ports. **Authorisation servers require exact-match redirect URIs** (RFC 9700 §4.5); ephemeral ports break that, so we declare a pool and the CLI picks the first one that's free. |

## Why a port pool and not "any localhost port"?

The CIMD spec adopts RFC 9700's exact-string-match for
`redirect_uri`. So `http://127.0.0.1/callback` (no port) does
NOT match `http://127.0.0.1:54321/callback` even though both
are loopback — the canonical example is the
[claude-code #37747](https://github.com/anthropics/claude-code/issues/37747)
bug where redirect_uri without a port broke the flow.

The pragmatic workaround the ecosystem has converged on:

1. The CLI declares a fixed pool of ports in its CIMD doc.
2. At authorise time, the CLI tries to `bind()` each port in
   the pool in order, picks the first one that succeeds, and
   passes that exact URI as `redirect_uri`.
3. The AS does its exact-match check; since the URI WAS in the
   declared pool, it accepts.

8 ports is generous — we'd need 8 concurrent
authentications on the same machine to exhaust the pool. If
that ever becomes a problem we publish a new CIMD with a
bigger pool (the old URL stays alive for existing
authorisations).

## Updating this document

- Editing the metadata is allowed; published clients re-fetch
  every time they auth, so a change ships immediately to all
  ASs.
- **Do NOT change `client_id`**. That field IS the identity;
  changing it is equivalent to deleting the client.
- Removing a redirect URI is a breaking change for anyone
  caught mid-flow on that port. Add freely; remove never.
- Adding fields (e.g. `software_statement` once we get a JWT
  attestation) is forward-compatible: ASs that don't
  understand them ignore them per RFC 7591 §2.

## When CIMD is not enough

A handful of authorisation servers don't support CIMD yet
(it's a 2025/2026-era addition). For those, Magec Lite falls
back to RFC 7591 Dynamic Client Registration automatically —
no user action required. See `internal/mcps/oauth.go` for the
precedence logic.
