// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/sessions"
)

// TestCleanTitle covers the small cleanup routine that takes
// whatever the LLM produced and turns it into a plain one-line
// title without quoting / punctuation noise / "Title:" prefixes.
func TestCleanTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"a normal title", "a normal title"},
		{"Title: research htmx", "research htmx"},
		{"title: research htmx", "research htmx"},
		{`"quoted thing"`, "quoted thing"},
		{"'single quotes'", "single quotes"},
		{"«guillemets»", "guillemets"},
		{"trailing period.", "trailing period"},
		{"trailing exclam!", "trailing exclam!"}, // we only strip dots, not exclamation
		{"multi\nline\nstuff", "multi"},
		{strings.Repeat("a", titleMaxLength+10), strings.Repeat("a", titleMaxLength-1) + "…"},
	}
	for _, c := range cases {
		got := cleanTitle(c.in)
		if got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTitlerMaybeFireDedup verifies the per-session bookkeeping
// stops a second concurrent attempt from being launched while the
// first one is still in flight.
//
// We don't exercise the LLM call here — onAppend's contract is "be
// fast". We inspect the state map directly: after the first
// maybeFire call, inFlight must be true and tries must be 1; the
// second call must be a no-op (tries stays at 1).
func TestTitlerMaybeFireDedup(t *testing.T) {
	titler := &sessionTitler{attempts: make(map[string]*titleAttempt)}

	// First call: maybeFire will mark inFlight and try to spawn a
	// goroutine. The goroutine will panic on the nil app, but
	// that runs concurrently and doesn't affect the state we
	// inspect immediately afterwards. To avoid that flakiness in
	// CI we pre-seed state to "just spawned" instead of letting
	// maybeFire run for real.
	titler.attempts["s1"] = &titleAttempt{
		tries:       1,
		lastTriedAt: 4,
		inFlight:    true,
	}

	// Second call mid-flight should be dropped: tries must not
	// increment, inFlight must stay true.
	titler.maybeFire(sessions.IndexEntry{ID: "s1", MsgCount: 5})

	titler.mu.Lock()
	st := titler.attempts["s1"]
	titler.mu.Unlock()
	if st.tries != 1 {
		t.Errorf("tries = %d, want 1 (in-flight call must be deduplicated)", st.tries)
	}
	if !st.inFlight {
		t.Errorf("inFlight = false, want true after dedup hit")
	}
}

// TestTitlerCooldownEnforced ensures we don't bang the LLM on
// every event past the threshold. Once an attempt is registered,
// a second call within titleAttemptCooldown events must be
// dropped.
func TestTitlerCooldownEnforced(t *testing.T) {
	titler := &sessionTitler{attempts: make(map[string]*titleAttempt)}
	titler.attempts["s4"] = &titleAttempt{
		tries:       1,
		lastTriedAt: 4,
		inFlight:    false, // previous attempt finished (failed)
	}
	// Cooldown is titleAttemptCooldown=2 events; MsgCount=5 is
	// one short of being allowed to retry.
	titler.maybeFire(sessions.IndexEntry{ID: "s4", MsgCount: 5})
	titler.mu.Lock()
	st := titler.attempts["s4"]
	titler.mu.Unlock()
	if st.tries != 1 {
		t.Errorf("tries = %d, want 1 (cooldown not yet elapsed)", st.tries)
	}
}

// TestTitlerSkipsAlreadyTitled checks that maybeFire is a no-op
// when the entry already has a Title — the auto-titler must never
// overwrite a title the user (or a previous successful pass) set.
func TestTitlerSkipsAlreadyTitled(t *testing.T) {
	titler := &sessionTitler{attempts: make(map[string]*titleAttempt)}
	entry := sessions.IndexEntry{ID: "s2", MsgCount: 10, Title: "user named me"}
	titler.onAppend(entry)
	titler.mu.Lock()
	st := titler.attempts["s2"]
	titler.mu.Unlock()
	if st == nil || !st.exhausted {
		t.Fatalf("expected exhausted=true for already-titled session, got %+v", st)
	}
}

// TestTitlerSkipsBelowThreshold covers the "not enough context"
// guard: the first three events must not trigger anything.
func TestTitlerSkipsBelowThreshold(t *testing.T) {
	titler := &sessionTitler{attempts: make(map[string]*titleAttempt)}
	for n := 1; n < titleMinMsgCount; n++ {
		titler.onAppend(sessions.IndexEntry{ID: "s3", MsgCount: n})
	}
	titler.mu.Lock()
	st := titler.attempts["s3"]
	titler.mu.Unlock()
	if st != nil {
		t.Errorf("expected no state for below-threshold session, got %+v", st)
	}
}
