// SPDX-License-Identifier: Apache-2.0

package mcps

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/achetronic/baifo/internal/storage"
)

// newTokenStore boots a fresh SQLite-backed TokenStore inside a temp
// dir. The storage package creates every bucket on open, so the
// oauth_tokens bucket is ready by the time we call Save/Load.
func newTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_ = filepath.Dir // silence unused import in case storage path stops mattering
	return NewTokenStore(db)
}

func TestTokenStore_SaveAndLoad(t *testing.T) {
	store := newTokenStore(t)
	tok := &oauth2.Token{
		AccessToken:  "abc",
		TokenType:    "Bearer",
		RefreshToken: "refresh-xyz",
		Expiry:       time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := store.Save("github", tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load("github")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatalf("Load returned nil")
	}
	if got.AccessToken != "abc" || got.RefreshToken != "refresh-xyz" || got.TokenType != "Bearer" {
		t.Errorf("token fields mismatch: %+v", got)
	}
	if !got.Expiry.Equal(tok.Expiry) {
		t.Errorf("expiry: got %v want %v", got.Expiry, tok.Expiry)
	}
}

func TestTokenStore_LoadMissingReturnsNil(t *testing.T) {
	store := newTokenStore(t)
	got, err := store.Load("nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing token, got %+v", got)
	}
}

func TestTokenStore_SaveNilDeletes(t *testing.T) {
	store := newTokenStore(t)
	if err := store.Save("gh", &oauth2.Token{AccessToken: "x"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.Save("gh", nil); err != nil {
		t.Fatalf("delete via nil save: %v", err)
	}
	got, err := store.Load("gh")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after nil-save, got %+v", got)
	}
}

func TestTokenStore_ExplicitDelete(t *testing.T) {
	store := newTokenStore(t)
	_ = store.Save("gh", &oauth2.Token{AccessToken: "x"})
	if err := store.Delete("gh"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := store.Load("gh")
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestTokenStore_NilStoreIsNoOp(t *testing.T) {
	// A nil receiver must not panic; baifo boots happily without a
	// token store configured (e.g. encryption_key not set).
	var s *TokenStore
	if err := s.Save("x", &oauth2.Token{}); err != nil {
		t.Errorf("nil Save: %v", err)
	}
	if got, err := s.Load("x"); err != nil || got != nil {
		t.Errorf("nil Load: got %v err %v", got, err)
	}
	if err := s.Delete("x"); err != nil {
		t.Errorf("nil Delete: %v", err)
	}
}
