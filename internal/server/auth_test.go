// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"

	"github.com/achetronic/baifo/internal/server/a2a"
)

// okHandler is a sentinel that records whether the request reached the
// wrapped handler (i.e. auth let it through).
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

// TestWithBearerAuth_EmptyTokenIsNoOp confirms that without a configured
// token the wrapper passes every request through unchanged.
func TestWithBearerAuth_EmptyTokenIsNoOp(t *testing.T) {
	reached := false
	h := withBearerAuth("", okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/api/a2a/root", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Error("empty token should let the request through")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestWithBearerAuth_ValidToken lets a correct credential through.
func TestWithBearerAuth_ValidToken(t *testing.T) {
	reached := false
	h := withBearerAuth("s3cret", okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/api/a2a/root", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached || rec.Code != http.StatusOK {
		t.Errorf("valid token rejected: reached=%v code=%d", reached, rec.Code)
	}
}

// TestWithBearerAuth_CaseInsensitiveScheme accepts "bearer"/"BEARER".
func TestWithBearerAuth_CaseInsensitiveScheme(t *testing.T) {
	for _, scheme := range []string{"bearer", "BEARER", "Bearer"} {
		reached := false
		h := withBearerAuth("tok", okHandler(&reached))
		req := httptest.NewRequest(http.MethodGet, "/api/a2a/root", nil)
		req.Header.Set("Authorization", scheme+" tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !reached {
			t.Errorf("scheme %q should be accepted", scheme)
		}
	}
}

// TestWithBearerAuth_Rejects covers the failure modes: no header, wrong
// token, wrong scheme. Each must 401 and never reach the handler, and
// must carry a WWW-Authenticate challenge.
func TestWithBearerAuth_Rejects(t *testing.T) {
	cases := map[string]string{
		"no header":    "",
		"wrong token":  "Bearer nope",
		"wrong scheme": "Basic dXNlcjpwYXNz",
		"bare token":   "s3cret",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			reached := false
			h := withBearerAuth("s3cret", okHandler(&reached))
			req := httptest.NewRequest(http.MethodGet, "/api/a2a/root", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if reached {
				t.Error("handler must not be reached on auth failure")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 must carry a WWW-Authenticate challenge")
			}
		})
	}
}

// TestWithBearerAuth_ChallengeDistinguishesMissingVsInvalid verifies
// the RFC 6750 §3 distinction: a request with no token must NOT carry
// error="invalid_token", while a request with a wrong token must.
func TestWithBearerAuth_ChallengeDistinguishesMissingVsInvalid(t *testing.T) {
	h := withBearerAuth("s3cret", okHandler(new(bool)))

	// Missing credential → bare challenge, no error code.
	missing := httptest.NewRequest(http.MethodGet, "/api/a2a/root", nil)
	mrec := httptest.NewRecorder()
	h.ServeHTTP(mrec, missing)
	if got := mrec.Header().Get("WWW-Authenticate"); strings.Contains(got, "invalid_token") {
		t.Errorf("missing-token challenge must not include invalid_token, got %q", got)
	}

	// Wrong credential → challenge with error="invalid_token".
	wrong := httptest.NewRequest(http.MethodGet, "/api/a2a/root", nil)
	wrong.Header.Set("Authorization", "Bearer nope")
	wrec := httptest.NewRecorder()
	h.ServeHTTP(wrec, wrong)
	if got := wrec.Header().Get("WWW-Authenticate"); !strings.Contains(got, `error="invalid_token"`) {
		t.Errorf("wrong-token challenge must include error=\"invalid_token\", got %q", got)
	}
}

// TestServerNew_HealthzNotGatedByAuth confirms /healthz stays open even
// when a token is configured, so liveness probes work without a
// credential.
func TestServerNew_HealthzNotGatedByAuth(t *testing.T) {
	srv := New(stubCore{}, Config{AuthToken: "s3cret"})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Error("/healthz must not require auth")
	}
}

// TestServerNew_A2AGatedByAuth confirms the A2A surface is protected
// when a token is set.
func TestServerNew_A2AGatedByAuth(t *testing.T) {
	srv := New(stubCore{}, Config{AuthToken: "s3cret"})

	req := httptest.NewRequest(http.MethodGet, "/api/a2a/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("A2A endpoint should be 401 without token, got %d", rec.Code)
	}
}

// stubCore is a no-op Core for tests that only exercise routing/auth,
// not agent execution.
type stubCore struct{}

func (stubCore) Agents() []a2a.AgentEntry        { return nil }
func (stubCore) SessionService() session.Service { return nil }
func (stubCore) MemoryService() memory.Service   { return nil }
func (stubCore) RootName() string                { return "test" }
func (stubCore) RootBuildError() error           { return nil }
