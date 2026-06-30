// SPDX-License-Identifier: Apache-2.0

// Package a2a exposes baifo's agents over the A2A (Agent-to-Agent)
// protocol so the TUI (and, later, remote clients) can converse with
// any agent without going through the in-process Facade.
//
// Layout follows Magec's convention:
//
//	/api/a2a/<agent_id>                          -> JSON-RPC handler
//	/api/a2a/<agent_id>/.well-known/agent-card.json
//	/api/a2a/.well-known/agent-card.json?agent=ID  (global discovery)
//
// One handler is registered per agent; the routing layer lives in
// ServeA2A so the parent mux only needs a single Handle("/api/a2a/").
package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adka2a"
	"google.golang.org/adk/session"
)

// PathPrefix is the URL prefix every A2A endpoint sits under. Kept
// public so the TUI client and the main mux can refer to a single
// constant.
const PathPrefix = "/api/a2a/"

// wellKnownSuffix is the discovery suffix appended to a per-agent URL
// to fetch its Agent Card.
const wellKnownSuffix = "/.well-known/agent-card.json"

// protocolVersion advertised in every Agent Card. Matches the version
// implemented by a2asrv as of writing.
const protocolVersion = "0.2.5"

// AgentEntry is the minimal description Handler needs to register one
// agent. The caller (internal/server) constructs these from the App's
// concrete state; this package does not import internal/app to avoid
// a cycle.
type AgentEntry struct {
	// ID is the slug used in the URL (e.g. "root", "code-reviewer",
	// "dyn_abc12345"). Must be unique per Handler.
	ID string

	// Name and Description are surfaced in the Agent Card. Free-form.
	Name        string
	Description string

	// Agent is the live ADK agent. The executor will use it as the
	// running root for every incoming task.
	Agent agent.Agent

	// Streaming selects the run's streaming mode. True (the default
	// for most providers) runs in SSE; false is for OpenAI-compatible
	// endpoints that do not implement Server-Sent Events. Resolved by
	// the caller from the agent's provider config.
	Streaming bool
}

// Handler routes /api/a2a/* requests to the right per-agent JSON-RPC
// handler and serves Agent Card discovery endpoints. It is safe to
// call Rebuild while serving: handlers and cards are swapped under a
// write lock and existing in-flight requests keep their reference.
type Handler struct {
	mu          sync.RWMutex
	handlers    map[string]http.Handler
	cards       map[string]*a2a.AgentCard
	publicURL   string
	requireAuth bool // true when bearer auth is active; cards advertise the scheme
}

// NewHandler returns a Handler bound to the given public URL. The URL
// is embedded in every Agent Card so external callers know where to
// reach each agent; pass the loopback URL when the server is local.
// Set requireAuth to true when the server enforces bearer authentication
// so Rebuild can annotate each card with the matching security scheme.
// The token value is never passed here and never written into any card.
func NewHandler(publicURL string, requireAuth bool) *Handler {
	return &Handler{
		handlers:    make(map[string]http.Handler),
		cards:       make(map[string]*a2a.AgentCard),
		publicURL:   strings.TrimRight(publicURL, "/"),
		requireAuth: requireAuth,
	}
}

// BuildRequestHandler constructs an a2asrv.RequestHandler for a single
// agent. Exported so callers that do not need the HTTP/JSON-RPC layer
// (notably the TUI, which talks to the same handler in-process) can
// reuse the exact same executor wiring used by Rebuild.
func BuildRequestHandler(entry AgentEntry, sessionSvc session.Service, memorySvc memory.Service) a2asrv.RequestHandler {
	streamMode := agent.StreamingModeSSE
	if !entry.Streaming {
		streamMode = agent.StreamingModeNone
	}
	execCfg := adka2a.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        entry.ID,
			Agent:          entry.Agent,
			SessionService: sessionSvc,
			MemoryService:  memorySvc,
		},
		RunConfig: agent.RunConfig{StreamingMode: streamMode},
	}
	executor := adka2a.NewExecutor(execCfg)
	return a2asrv.NewHandler(executor, a2asrv.WithLogger(slog.Default()))
}

// Rebuild swaps the live set of agents. Every entry produces one
// JSON-RPC handler (via BuildRequestHandler + a2asrv.NewJSONRPCHandler)
// and one Agent Card. Pass the shared sessionSvc and memorySvc so
// every executor goes through the same persistence layer.
func (h *Handler) Rebuild(entries []AgentEntry, sessionSvc session.Service, memorySvc memory.Service) {
	handlers := make(map[string]http.Handler, len(entries))
	cards := make(map[string]*a2a.AgentCard, len(entries))

	for _, e := range entries {
		if e.ID == "" || e.Agent == nil {
			continue
		}
		invokeURL := fmt.Sprintf("%s%s%s", h.publicURL, PathPrefix, e.ID)

		card := &a2a.AgentCard{
			Name:               e.Name,
			Description:        e.Description,
			URL:                invokeURL,
			Version:            "1.0.0",
			ProtocolVersion:    protocolVersion,
			PreferredTransport: a2a.TransportProtocolJSONRPC,
			DefaultInputModes:  []string{"text/plain"},
			DefaultOutputModes: []string{"text/plain"},
			Capabilities: a2a.AgentCapabilities{
				Streaming: true,
			},
			Skills: adka2a.BuildAgentSkills(e.Agent),
		}
		h.applySecurity(card)
		cards[e.ID] = card

		reqHandler := BuildRequestHandler(e, sessionSvc, memorySvc)
		handlers[e.ID] = a2asrv.NewJSONRPCHandler(reqHandler)
	}

	h.mu.Lock()
	h.handlers = handlers
	h.cards = cards
	h.mu.Unlock()
}

// applySecurity annotates card with the bearer security scheme when the
// server enforces auth. The card advertises only the scheme descriptor
// (type http, scheme bearer) so a client knows to send
// Authorization: Bearer; the token value is never written into the card.
// When auth is off both fields stay nil and omitempty drops them.
func (h *Handler) applySecurity(card *a2a.AgentCard) {
	if !h.requireAuth {
		return
	}
	card.SecuritySchemes = a2a.NamedSecuritySchemes{
		"bearer": a2a.HTTPAuthSecurityScheme{Scheme: "bearer"},
	}
	card.Security = []a2a.SecurityRequirements{
		{"bearer": a2a.SecuritySchemeScopes{}},
	}
}

// AgentIDs returns the slugs currently registered. Useful for the
// server's health endpoint and for tests.
func (h *Handler) AgentIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.handlers))
	for id := range h.handlers {
		out = append(out, id)
	}
	return out
}

// ServeHTTP routes an incoming request based on its path: well-known
// discovery vs JSON-RPC invocation. Plug this into the parent mux as
// Handle("/api/a2a/", h).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Global agent-card discovery: /api/a2a/.well-known/agent-card.json
	if path == PathPrefix+".well-known/agent-card.json" {
		h.serveGlobalCards(w, r)
		return
	}

	// Per-agent agent-card discovery:
	//   /api/a2a/<id>/.well-known/agent-card.json
	if strings.HasSuffix(path, wellKnownSuffix) {
		h.servePerAgentCard(w, r)
		return
	}

	h.serveJSONRPC(w, r)
}

// serveGlobalCards either returns every card as a JSON array or, when
// ?agent=ID is set, the single matching card.
func (h *Handler) serveGlobalCards(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent")

	h.mu.RLock()
	cards := h.cards
	h.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if agentID != "" {
		card, ok := cards[agentID]
		if !ok {
			http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(card)
		return
	}

	out := make([]*a2a.AgentCard, 0, len(cards))
	for _, c := range cards {
		out = append(out, c)
	}
	_ = json.NewEncoder(w).Encode(out)
}

// servePerAgentCard returns one card matched by the path segment.
func (h *Handler) servePerAgentCard(w http.ResponseWriter, r *http.Request) {
	id := extractAgentID(r.URL.Path, PathPrefix, wellKnownSuffix)

	h.mu.RLock()
	card, ok := h.cards[id]
	h.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !ok {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(card)
}

// serveJSONRPC dispatches the request to the matching agent's JSON-RPC
// handler. Returns 404 when the agent is unknown.
func (h *Handler) serveJSONRPC(w http.ResponseWriter, r *http.Request) {
	id := extractAgentID(r.URL.Path, PathPrefix, "")

	h.mu.RLock()
	handler, ok := h.handlers[id]
	h.mu.RUnlock()

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	ctx := context.WithValue(r.Context(), agentIDKey, id)
	handler.ServeHTTP(w, r.WithContext(ctx))
}

// contextKey scopes context values stamped on the request so handlers
// downstream can read the active agent id without re-parsing the URL.
type contextKey string

const agentIDKey contextKey = "a2a-agent-id"

// AgentIDFromContext returns the agent id the handler stamped on the
// request context, or empty when called outside an A2A request flow.
func AgentIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(agentIDKey).(string); ok {
		return v
	}
	return ""
}

// extractAgentID strips the prefix (and optional suffix) from a path
// and keeps the first segment after the prefix. Examples:
//
//	"/api/a2a/root", "/api/a2a/", ""                    -> "root"
//	"/api/a2a/code-reviewer/.well-known/agent-card.json",
//	  "/api/a2a/", "/.well-known/agent-card.json"       -> "code-reviewer"
//	"/api/a2a/dyn_abc/extra/path", "/api/a2a/", ""      -> "dyn_abc"
func extractAgentID(path, prefix, suffix string) string {
	rest := strings.TrimPrefix(path, prefix)
	if suffix != "" {
		rest = strings.TrimSuffix(rest, suffix)
	}
	rest = strings.TrimSuffix(rest, "/")
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}

// Compile-time check: Handler is an http.Handler.
var _ http.Handler = (*Handler)(nil)

// errMisuse is returned when callers ask for something the Handler
// cannot currently do. Kept here to avoid a fmt.Errorf race when the
// only error in the file is wrapping.
var errMisuse = errors.New("a2a handler misuse")
