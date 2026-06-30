// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"log/slog"
	"testing"
)

// newPlaintextTestStore creates a plaintext (no encryption key) store
// in a temp dir for use in LogRedactor tests. Plaintext mode is fast
// (no PBKDF2) and correct for unit testing the redactor logic.
func newPlaintextTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore plaintext: %v", err)
	}
	return s
}

func TestLogRedactor_BasicRedaction(t *testing.T) {
	store := newPlaintextTestStore(t)
	if err := store.Set("github_token", "ghp_supersecretvalue1234", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	r := NewLogRedactor(store, true, 8)

	attr := slog.String("url", "https://api.github.com?token=ghp_supersecretvalue1234")
	got := r.Redact(attr)

	want := "https://api.github.com?token=${secret:github_token}"
	if got.Value.String() != want {
		t.Errorf("Redact: got %q, want %q", got.Value.String(), want)
	}
	if got.Key != "url" {
		t.Errorf("key changed: got %q, want %q", got.Key, "url")
	}
}

func TestLogRedactor_ShortSecretSkipped(t *testing.T) {
	store := newPlaintextTestStore(t)
	// Value "pin" is only 3 bytes, below the default minLen of 8.
	if err := store.Set("pin", "pin1234", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	r := NewLogRedactor(store, true, 8)

	attr := slog.String("msg", "code is pin1234 for the door")
	got := r.Redact(attr)

	// Value len("pin1234")=7 < 8, so it must NOT be redacted.
	if got.Value.String() != attr.Value.String() {
		t.Errorf("short secret was redacted: got %q, want unchanged %q", got.Value.String(), attr.Value.String())
	}
}

func TestLogRedactor_DisabledDoesNothing(t *testing.T) {
	store := newPlaintextTestStore(t)
	if err := store.Set("github_token", "ghp_supersecretvalue1234", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	r := NewLogRedactor(store, false, 8)

	attr := slog.String("url", "https://api.github.com?token=ghp_supersecretvalue1234")
	got := r.Redact(attr)

	if got.Value.String() != attr.Value.String() {
		t.Errorf("disabled redactor mutated attr: got %q, want %q", got.Value.String(), attr.Value.String())
	}
}

func TestLogRedactor_NilStoreDoesNothing(t *testing.T) {
	r := NewLogRedactor(nil, true, 8)

	attr := slog.String("key", "some-value-that-should-not-be-redacted")
	got := r.Redact(attr)

	if got.Value.String() != attr.Value.String() {
		t.Errorf("nil-store redactor mutated attr: got %q, want %q", got.Value.String(), attr.Value.String())
	}
}

func TestLogRedactor_CacheRefreshOnMutation(t *testing.T) {
	store := newPlaintextTestStore(t)

	r := NewLogRedactor(store, true, 8)

	// First redact call with empty store: nothing changes.
	attr1 := slog.String("hdr", "Authorization: Bearer sk_newtoken_abcdefgh")
	got1 := r.Redact(attr1)
	if got1.Value.String() != attr1.Value.String() {
		t.Errorf("before adding secret: attr mutated unexpectedly: %q", got1.Value.String())
	}

	// Now add a secret to the store. Generation bumps.
	genBefore := store.Generation()
	if err := store.Set("api_key", "sk_newtoken_abcdefgh", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	genAfter := store.Generation()
	if genAfter <= genBefore {
		t.Errorf("generation did not increase after Set: before=%d after=%d", genBefore, genAfter)
	}

	// Next Redact call must pick up the new secret.
	attr2 := slog.String("hdr", "Authorization: Bearer sk_newtoken_abcdefgh")
	got2 := r.Redact(attr2)
	want := "Authorization: Bearer ${secret:api_key}"
	if got2.Value.String() != want {
		t.Errorf("after mutation: got %q, want %q", got2.Value.String(), want)
	}
}

func TestLogRedactor_SetStore(t *testing.T) {
	store1 := newPlaintextTestStore(t)
	if err := store1.Set("tok", "supersecretA12345", ""); err != nil {
		t.Fatalf("Set store1: %v", err)
	}
	store2 := newPlaintextTestStore(t)
	if err := store2.Set("tok2", "supersecretB67890", ""); err != nil {
		t.Fatalf("Set store2: %v", err)
	}

	r := NewLogRedactor(store1, true, 8)

	attr := slog.String("v", "supersecretA12345")
	if got := r.Redact(attr); got.Value.String() != "${secret:tok}" {
		t.Errorf("before SetStore: got %q, want ${secret:tok}", got.Value.String())
	}

	r.SetStore(store2)

	attr2 := slog.String("v", "supersecretB67890")
	if got := r.Redact(attr2); got.Value.String() != "${secret:tok2}" {
		t.Errorf("after SetStore: got %q, want ${secret:tok2}", got.Value.String())
	}

	// Old secret must no longer match.
	attrOld := slog.String("v", "supersecretA12345")
	if got := r.Redact(attrOld); got.Value.String() != "supersecretA12345" {
		t.Errorf("old secret still redacted after SetStore: got %q", got.Value.String())
	}
}

func TestLogRedactor_SetConfig(t *testing.T) {
	store := newPlaintextTestStore(t)
	if err := store.Set("tok", "supersecretX99999", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Start enabled.
	r := NewLogRedactor(store, true, 8)
	attr := slog.String("v", "supersecretX99999")
	if got := r.Redact(attr); got.Value.String() != "${secret:tok}" {
		t.Errorf("enabled: got %q, want ${secret:tok}", got.Value.String())
	}

	// Disable via SetConfig.
	r.SetConfig(false, 8)
	if got := r.Redact(attr); got.Value.String() != "supersecretX99999" {
		t.Errorf("after disable: got %q, want original", got.Value.String())
	}

	// Re-enable.
	r.SetConfig(true, 8)
	if got := r.Redact(attr); got.Value.String() != "${secret:tok}" {
		t.Errorf("re-enabled: got %q, want ${secret:tok}", got.Value.String())
	}
}

func TestLogRedactor_GroupAttr(t *testing.T) {
	store := newPlaintextTestStore(t)
	if err := store.Set("github_token", "ghp_supersecretvalue1234", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	r := NewLogRedactor(store, true, 8)

	// Build a group attribute containing a secret.
	group := slog.Group("request",
		slog.String("url", "https://host?tok=ghp_supersecretvalue1234"),
		slog.Int("status", 200),
	)
	got := r.Redact(group)

	if got.Value.Kind() != slog.KindGroup {
		t.Fatalf("expected group, got kind %v", got.Value.Kind())
	}
	members := got.Value.Group()
	if len(members) != 2 {
		t.Fatalf("expected 2 group members, got %d", len(members))
	}
	want := "https://host?tok=${secret:github_token}"
	if members[0].Value.String() != want {
		t.Errorf("group member[0]: got %q, want %q", members[0].Value.String(), want)
	}
	// Non-string member (int) must be unchanged.
	if members[1].Value.Int64() != 200 {
		t.Errorf("group member[1] changed: got %v", members[1].Value.Int64())
	}
}

func TestLogRedactor_GenerationBumpsOnDelete(t *testing.T) {
	store := newPlaintextTestStore(t)
	if err := store.Set("tok", "supersecretDEL12345", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	genAfterSet := store.Generation()

	if err := store.Delete("tok"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	genAfterDelete := store.Generation()
	if genAfterDelete <= genAfterSet {
		t.Errorf("generation did not increase after Delete: %d -> %d", genAfterSet, genAfterDelete)
	}

	// The redactor should no longer redact the value after deletion.
	r := NewLogRedactor(store, true, 8)
	attr := slog.String("v", "supersecretDEL12345")
	got := r.Redact(attr)
	if got.Value.String() != "supersecretDEL12345" {
		t.Errorf("deleted secret still redacted: got %q", got.Value.String())
	}
}
