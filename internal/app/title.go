// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/sessions"
)

// title.go implements the auto-titler: a small background process
// that gives untitled sessions a meaningful name after the second
// turn. The goal is "the user never sees (untitled) for a session
// they've actually used", with two levels of resilience:
//
//   1. Primary path: ask the same LLM the chat uses to summarise
//      the first few turns into a 3–7-word title. Retried on every
//      AppendEvent (and on SwitchSession) until success or until
//      we've burned through titleMaxAttempts attempts.
//
//   2. Fallback (when the LLM keeps failing): build a title from
//      the first user message, trimmed and ellipsised. Honest, no
//      magic, always available.
//
// Everything happens off the runner goroutine. The titler owns one
// goroutine per session, deduplicated; the runner's AppendEvent
// returns immediately regardless of whether a titling pass is
// running, queued or skipped.

// titleMinMsgCount is the AppendEvent threshold at which the
// titler starts trying. The runner appends events one by one
// (user, assistant, user, assistant, ...), so 4 events = two full
// turns, which is enough context for a useful title.
const titleMinMsgCount = 4

// titleMaxAttempts caps how many times we'll bother the LLM for a
// single session before giving up and using the fallback. The
// counter increments every time the titler actually fires (i.e.
// MsgCount crossed a fresh threshold), not every AppendEvent.
const titleMaxAttempts = 5

// titleAttemptCooldown enforces a minimum interval between LLM
// attempts for the same session. Without it, a chatty session
// would fire the titler on every event past the threshold; with
// it, we wait at least one extra turn between retries so the
// model sees fresh context.
const titleAttemptCooldown = 2 // events between attempts

// titleLLMTimeout caps how long a single LLM call may take before
// we give up on it. Generous because some providers respond in
// 20s on cold start; small enough that even repeated failures
// won't pile up unbounded goroutines.
const titleLLMTimeout = 30 * time.Second

// titleMaxLength is the hard cap on the title's visible length,
// after cleanup. Both LLM output and the fallback are trimmed to
// this width.
const titleMaxLength = 60

// titleFallbackEllipsis is appended when the fallback title was
// cut by the length cap. The character is the single-rune ellipsis
// so the trim doesn't break wide-char rendering downstream.
const titleFallbackEllipsis = "…"

// sessionTitler is the per-App orchestrator. One instance is shared
// across all sessions; the inFlight map serialises concurrent
// invocations for the same session.
type sessionTitler struct {
	app *App

	mu       sync.Mutex
	attempts map[string]*titleAttempt
}

// titleAttempt is the per-session bookkeeping. Lives in memory
// only — if baifo restarts, untitled sessions get re-evaluated the
// next time they receive an event or are switched into.
type titleAttempt struct {
	// tries is the number of LLM attempts we've made so far.
	tries int

	// lastTriedAt is the MsgCount we last fired at. We use it to
	// enforce titleAttemptCooldown.
	lastTriedAt int

	// inFlight signals there's already a goroutine running for
	// this session. Used to deduplicate so an event burst doesn't
	// spawn N parallel titlers.
	inFlight bool

	// exhausted is set true after we apply the fallback title or
	// after the user explicitly renames the session. Either way
	// we stop fighting and leave the title alone.
	exhausted bool
}

// newSessionTitler constructs a titler bound to the given App.
// The App is expected to wire titler.onAppend into the sessions
// service via SetAppendHook.
func newSessionTitler(a *App) *sessionTitler {
	return &sessionTitler{
		app:      a,
		attempts: make(map[string]*titleAttempt),
	}
}

// onAppend is the AppendHookFunc the titler registers with the
// sessions service. Runs on the runner goroutine — must return
// fast. Real work happens in a fired-and-forgotten goroutine.
func (t *sessionTitler) onAppend(entry sessions.IndexEntry) {
	if entry.ID == "" {
		return
	}
	// Already titled (by us or by the user)? Done.
	if strings.TrimSpace(entry.Title) != "" {
		t.markExhausted(entry.ID)
		return
	}
	// Not enough context yet.
	if entry.MsgCount < titleMinMsgCount {
		return
	}
	t.maybeFire(entry)
}

// onSessionResumed is the hook called when SwitchSession activates
// a session. Mirrors onAppend but for the "old, never-titled
// session is back" case: when the user revisits a session that
// already has enough events but no title, we kick the titler too.
func (t *sessionTitler) onSessionResumed(entry sessions.IndexEntry) {
	if entry.ID == "" {
		return
	}
	if strings.TrimSpace(entry.Title) != "" {
		t.markExhausted(entry.ID)
		return
	}
	if entry.MsgCount < titleMinMsgCount {
		return
	}
	t.maybeFire(entry)
}

// maybeFire is the gatekeeper. Reads/updates the per-session
// state under the lock and decides whether to launch a goroutine.
func (t *sessionTitler) maybeFire(entry sessions.IndexEntry) {
	t.mu.Lock()
	st, ok := t.attempts[entry.ID]
	if !ok {
		st = &titleAttempt{}
		t.attempts[entry.ID] = st
	}
	switch {
	case st.exhausted:
		t.mu.Unlock()
		return
	case st.inFlight:
		// Another goroutine is already on it; let it finish.
		t.mu.Unlock()
		return
	case st.tries >= titleMaxAttempts:
		// We've tried enough. Drop the fallback and stop.
		st.inFlight = true
		t.mu.Unlock()
		go t.applyFallback(entry)
		return
	case entry.MsgCount < st.lastTriedAt+titleAttemptCooldown && st.tries > 0:
		// Too soon after the last retry; wait a turn.
		t.mu.Unlock()
		return
	}
	st.inFlight = true
	st.tries++
	st.lastTriedAt = entry.MsgCount
	t.mu.Unlock()

	go t.runOnce(entry)
}

// markExhausted is called when we learn the session already has a
// title (either we just set one, the user renamed it manually, or
// we discovered it via onAppend with a non-empty Title). Prevents
// any further attempts.
func (t *sessionTitler) markExhausted(id string) {
	t.mu.Lock()
	st, ok := t.attempts[id]
	if !ok {
		st = &titleAttempt{}
		t.attempts[id] = st
	}
	st.exhausted = true
	st.inFlight = false
	t.mu.Unlock()
}

// release flips inFlight off without changing the exhausted state.
// Used at the end of a failed LLM attempt so the next event can
// retry.
func (t *sessionTitler) release(id string) {
	t.mu.Lock()
	if st, ok := t.attempts[id]; ok {
		st.inFlight = false
	}
	t.mu.Unlock()
}

// runOnce is one LLM attempt. Reads the session events, builds the
// prompt, calls the model with a timeout, cleans the response and
// stores the result via Service.SetTitle. On any failure we
// release the in-flight flag so the next AppendEvent re-tries
// (until tries >= titleMaxAttempts, when we'll fall back).
func (t *sessionTitler) runOnce(entry sessions.IndexEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), titleLLMTimeout)
	defer cancel()

	title, err := t.generateTitle(ctx, entry)
	if err != nil || title == "" {
		slog.Debug("session title: llm attempt failed",
			"session", entry.ID, "tries", entry.MsgCount, "err", err)
		t.release(entry.ID)
		return
	}
	if err := t.commit(ctx, entry.ID, title); err != nil {
		slog.Debug("session title: set failed", "session", entry.ID, "err", err)
		t.release(entry.ID)
		return
	}
	t.markExhausted(entry.ID)
}

// applyFallback runs when titleMaxAttempts has been spent without
// a successful LLM response. We build a deterministic title from
// the first user message and store it; that locks the title in
// (exhausted=true) so we don't keep poking the LLM.
func (t *sessionTitler) applyFallback(entry sessions.IndexEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	title := t.fallbackTitle(ctx, entry.ID)
	if title == "" {
		// No first user message available either — give up
		// gracefully; the overlay will keep showing "(untitled)".
		t.markExhausted(entry.ID)
		return
	}
	if err := t.commit(ctx, entry.ID, title); err != nil {
		slog.Debug("session title fallback: set failed", "session", entry.ID, "err", err)
	}
	t.markExhausted(entry.ID)
}

// commit writes the chosen title to SQLite via the sessions Service.
func (t *sessionTitler) commit(ctx context.Context, sessionID, title string) error {
	return t.app.sessions.SetTitle(ctx, appName, t.app.userID, sessionID, title)
}

// generateTitle runs the LLM step. Returns ("", nil) when the model
// produced no usable text; returns ("", err) on transport-level
// errors so the caller knows to count it as a failed attempt.
func (t *sessionTitler) generateTitle(ctx context.Context, entry sessions.IndexEntry) (string, error) {
	// Resolve the utility LLM (the agents.yaml entry flagged
	// utility: true), falling back to the chat's root model when no
	// utility agent is configured. Titling is exactly the kind of
	// chore the utility agent exists for: a 3-7 word summary does
	// not need the expensive coordinator model.
	t.app.mu.RLock()
	provider, modelID, ok := t.app.utilityLLMRef()
	t.app.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no LLM configured for titling")
	}
	llm, err := t.app.providers.Model(ctx, provider, modelID)
	if err != nil {
		return "", fmt.Errorf("resolve model: %w", err)
	}

	prompt, err := t.buildPrompt(ctx, entry.ID)
	if err != nil {
		return "", err
	}

	// Single user turn, no system message. ADK's GenerateContent
	// returns an iterator; we want the final non-partial chunk's
	// text. Stream=false keeps it simple.
	req := &model.LLMRequest{
		Contents: []*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{Text: prompt}},
		}},
	}
	var collected strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		if resp.Partial {
			continue
		}
		for _, p := range resp.Content.Parts {
			if p != nil && p.Text != "" {
				collected.WriteString(p.Text)
			}
		}
	}
	raw := strings.TrimSpace(collected.String())
	return cleanTitle(raw), nil
}

// buildPrompt builds the summariser prompt from the first few
// events of the session. Tools, tool results and system events
// are skipped — we only feed the user/assistant turns since the
// "title" should reflect the user's intent, not the agent's tool
// dance.
func (t *sessionTitler) buildPrompt(ctx context.Context, sessionID string) (string, error) {
	resp, err := t.app.sessions.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: t.app.userID, SessionID: sessionID,
	})
	if err != nil || resp == nil || resp.Session == nil {
		return "", fmt.Errorf("load session events: %w", err)
	}

	type turn struct {
		role string
		text string
	}
	var turns []turn
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil {
			continue
		}
		var t strings.Builder
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			// Skip tool plumbing — the title should describe
			// the conversation, not the agent's machinery.
			if p.FunctionCall != nil || p.FunctionResponse != nil {
				continue
			}
			if p.Text != "" {
				t.WriteString(p.Text)
			}
		}
		text := strings.TrimSpace(t.String())
		if text == "" {
			continue
		}
		role := ev.Content.Role
		if role == "" {
			role = "model"
		}
		turns = append(turns, turn{role: role, text: text})
		if len(turns) >= 6 {
			break
		}
	}
	if len(turns) == 0 {
		return "", fmt.Errorf("no usable events for prompt")
	}

	var b strings.Builder
	b.WriteString("Write a short title (3–7 words) for the following conversation.\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Same language the user is using.\n")
	b.WriteString("- No quotes, no trailing punctuation.\n")
	b.WriteString("- Plain text, single line.\n")
	b.WriteString("- Just the title, nothing else.\n\n")
	b.WriteString("Conversation:\n")
	for _, tn := range turns {
		role := "user"
		if tn.role == "model" || tn.role == "assistant" {
			role = "assistant"
		}
		// Truncate each turn so a single huge message doesn't
		// blow the token budget. 400 chars is enough context
		// for a title and keeps the call cheap.
		text := tn.text
		if len(text) > 400 {
			text = text[:400] + " …"
		}
		fmt.Fprintf(&b, "%s: %s\n", role, text)
	}
	b.WriteString("\nTitle:")
	return b.String(), nil
}

// cleanTitle strips the noise LLMs sometimes prepend ("Title: ...",
// quotes, trailing periods) and clamps the length so the overlay
// never has to truncate.
func cleanTitle(raw string) string {
	s := strings.TrimSpace(raw)
	// Drop "Title:" prefix if present.
	if low := strings.ToLower(s); strings.HasPrefix(low, "title:") {
		s = strings.TrimSpace(s[len("title:"):])
	}
	// Collapse multi-line to first line.
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// Strip surrounding quotes.
	s = strings.Trim(s, "\"'`«»“”")
	// Drop a single trailing period (LLMs love them).
	s = strings.TrimRight(s, ".")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Clamp to titleMaxLength runes.
	r := []rune(s)
	if len(r) > titleMaxLength {
		r = r[:titleMaxLength-1]
		s = string(r) + titleFallbackEllipsis
	}
	return s
}

// fallbackTitle builds the deterministic last-resort title from the
// first user message of the session. Returns "" when there's no
// such message (rare — only happens when MsgCount counted only
// system events, which shouldn't happen with the current runner).
func (t *sessionTitler) fallbackTitle(ctx context.Context, sessionID string) string {
	resp, err := t.app.sessions.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: t.app.userID, SessionID: sessionID,
	})
	if err != nil || resp == nil || resp.Session == nil {
		return ""
	}
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil {
			continue
		}
		if ev.Content.Role != "user" {
			continue
		}
		var b strings.Builder
		for _, p := range ev.Content.Parts {
			if p != nil && p.FunctionCall == nil && p.FunctionResponse == nil {
				b.WriteString(p.Text)
			}
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}
		// First line, trimmed to titleMaxLength runes.
		if i := strings.IndexAny(text, "\r\n"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		r := []rune(text)
		if len(r) > titleMaxLength {
			r = r[:titleMaxLength-1]
			return string(r) + titleFallbackEllipsis
		}
		return text
	}
	return ""
}
