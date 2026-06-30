// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// withBearerAuth wraps next so every request must present
// `Authorization: Bearer <token>`. When token is empty the wrapper is
// a no-op and next is returned unchanged — auth is strictly opt-in,
// driven by a2a.credentials.token in baifo.yaml.
//
// The comparison is constant-time (crypto/subtle) so a caller cannot
// learn the token byte-by-byte through timing. Both failure modes
// return 401 with a WWW-Authenticate challenge and a small JSON body,
// but they differ per RFC 6750 §3: a request with NO token gets a bare
// `Bearer realm="baifo"` challenge, while a request with a WRONG token
// additionally gets `error="invalid_token"` so clients can distinguish
// the two and decide whether to refresh.
func withBearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, present := bearerToken(r)
		if !present {
			// No credential at all: bare challenge, no error code.
			// RFC 6750 §3 — error codes are only for requests that
			// DID present a (bad) token.
			writeUnauthorized(w, `Bearer realm="baifo"`, "missing bearer token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), expected) != 1 {
			// A token was presented but it is wrong: signal
			// invalid_token so clients can tell "I sent nothing" from
			// "what I sent is bad" and refresh accordingly.
			writeUnauthorized(w,
				`Bearer realm="baifo", error="invalid_token", error_description="the bearer token is invalid"`,
				"invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeUnauthorized emits a 401 with the given WWW-Authenticate
// challenge and a small JSON body carrying a human-readable reason.
func writeUnauthorized(w http.ResponseWriter, challenge, reason string) {
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","reason":"` + reason + `"}`))
}

// bearerToken extracts the token from an `Authorization: Bearer <t>`
// header. The scheme match is case-insensitive (RFC 7235 §2.1);
// returns ok=false when the header is absent or not a bearer header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}
