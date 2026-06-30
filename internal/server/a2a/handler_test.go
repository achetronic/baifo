// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
)

// fakeAgent is a no-op agent.Agent stand-in. We only need a non-nil
// value here; the executor never actually drives it in these tests
// because we only exercise discovery endpoints.
type fakeAgent struct{}

func (fakeAgent) Name() string        { return "fake" }
func (fakeAgent) Description() string { return "fake" }

// TestHandler_ServesGlobalAgentCardList covers the discovery endpoint
// every A2A client hits first: /api/a2a/.well-known/agent-card.json
// without a query string returns the full list of cards.
func TestHandler_ServesGlobalAgentCardList(t *testing.T) {
	h := NewHandler("http://localhost:7777", false)
	// We can't actually build a real ExecutorConfig without an ADK
	// agent and session.Service, so we skip the JSON-RPC handler
	// wire-up and seed the cards directly. The discovery path does
	// not touch the handlers map.
	h.cards["root"] = &a2a.AgentCard{Name: "baifo", URL: "http://localhost:7777/api/a2a/root"}
	h.cards["code-reviewer"] = &a2a.AgentCard{Name: "code-reviewer", URL: "http://localhost:7777/api/a2a/code-reviewer"}

	req := httptest.NewRequest(http.MethodGet, "/api/a2a/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var cards []*a2a.AgentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &cards); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, rec.Body.String())
	}
	if len(cards) != 2 {
		t.Errorf("got %d cards, want 2", len(cards))
	}
}

// TestHandler_ServesPerAgentCard is the second discovery flavour:
// /api/a2a/<id>/.well-known/agent-card.json returns just that card.
func TestHandler_ServesPerAgentCard(t *testing.T) {
	h := NewHandler("http://localhost:7777", false)
	h.cards["root"] = &a2a.AgentCard{Name: "baifo"}

	req := httptest.NewRequest(http.MethodGet, "/api/a2a/root/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"baifo"`) {
		t.Errorf("response missing card name: %s", rec.Body.String())
	}
}

// TestHandler_UnknownAgentReturns404 documents the not-found shape
// so the TUI client knows what to expect when an agent slug is bad.
func TestHandler_UnknownAgentReturns404(t *testing.T) {
	h := NewHandler("http://localhost:7777", false)

	req := httptest.NewRequest(http.MethodPost, "/api/a2a/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// TestExtractAgentID covers the path parsing helper so the routing
// rules stay tight (one segment after the prefix, optional suffix).
func TestExtractAgentID(t *testing.T) {
	cases := []struct{ in, prefix, suffix, want string }{
		{"/api/a2a/root", "/api/a2a/", "", "root"},
		{"/api/a2a/code-reviewer/.well-known/agent-card.json", "/api/a2a/", "/.well-known/agent-card.json", "code-reviewer"},
		{"/api/a2a/dyn_abc/whatever", "/api/a2a/", "", "dyn_abc"},
		{"/api/a2a/", "/api/a2a/", "", ""},
	}
	for _, tc := range cases {
		got := extractAgentID(tc.in, tc.prefix, tc.suffix)
		if got != tc.want {
			t.Errorf("extractAgentID(%q, %q, %q) = %q, want %q", tc.in, tc.prefix, tc.suffix, got, tc.want)
		}
	}
}

// Compile-time check: errMisuse remains referenced. (Keeps the
// package's small set of exported / package-level symbols stable
// across edits.)
var _ = errMisuse

// TestHandler_SecuritySchemes_WithAuth checks that when requireAuth is true,
// Rebuild sets SecuritySchemes and Security on every card, and that the
// JSON output contains the expected fields. The token value must never
// appear in the card.
func TestHandler_SecuritySchemes_WithAuth(t *testing.T) {
	h := NewHandler("http://localhost:7777", true)
	card := &a2a.AgentCard{
		Name:        "baifo",
		Description: "root agent",
		URL:         "http://localhost:7777/api/a2a/root",
	}
	h.applySecurity(card)
	h.cards["root"] = card

	// SecuritySchemes must contain a "bearer" entry.
	if card.SecuritySchemes == nil {
		t.Fatal("SecuritySchemes is nil, want non-nil when requireAuth=true")
	}
	scheme, found := card.SecuritySchemes["bearer"]
	if !found {
		t.Fatal("SecuritySchemes missing \"bearer\" key")
	}
	schemeJSON, err := json.Marshal(scheme)
	if err != nil {
		t.Fatalf("marshal scheme: %v", err)
	}
	if !strings.Contains(string(schemeJSON), `"type":"http"`) {
		t.Errorf("scheme JSON missing type:http, got %s", schemeJSON)
	}
	if !strings.Contains(string(schemeJSON), `"scheme":"bearer"`) {
		t.Errorf("scheme JSON missing scheme:bearer, got %s", schemeJSON)
	}

	// Security must reference "bearer".
	if len(card.Security) == 0 {
		t.Fatal("Security is empty, want one requirement entry")
	}
	if _, ok := card.Security[0]["bearer"]; !ok {
		t.Error("Security[0] missing \"bearer\" key")
	}

	// Full card JSON must contain securitySchemes.
	cardJSON, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	if !strings.Contains(string(cardJSON), `"securitySchemes"`) {
		t.Errorf("card JSON missing securitySchemes, got %s", cardJSON)
	}
}

// TestHandler_SecuritySchemes_NoAuth checks that when requireAuth is false,
// SecuritySchemes and Security are nil and the serialised card omits those
// keys entirely (omitempty on the struct tags).
func TestHandler_SecuritySchemes_NoAuth(t *testing.T) {
	h := NewHandler("http://localhost:7777", false)
	card := &a2a.AgentCard{Name: "baifo", URL: "http://localhost:7777/api/a2a/root"}
	h.applySecurity(card)
	h.cards["root"] = card
	if card.SecuritySchemes != nil {
		t.Errorf("SecuritySchemes should be nil when requireAuth=false, got %v", card.SecuritySchemes)
	}
	if card.Security != nil {
		t.Errorf("Security should be nil when requireAuth=false, got %v", card.Security)
	}

	cardJSON, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	if strings.Contains(string(cardJSON), "securitySchemes") {
		t.Errorf("card JSON must not contain securitySchemes when requireAuth=false, got %s", cardJSON)
	}
	if strings.Contains(string(cardJSON), `"security"`) {
		t.Errorf("card JSON must not contain security when requireAuth=false, got %s", cardJSON)
	}
}
