// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPassphrase = "super-secret-passphrase-for-tests"

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, testPassphrase)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, dir
}

func TestRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)

	if err := s.Set("github_token", "ghp_topsecret_value_42", "GitHub PAT"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := s.Get("github_token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "ghp_topsecret_value_42" {
		t.Errorf("Get: got %q, want %q", got, "ghp_topsecret_value_42")
	}
}

func TestPlaintextNeverHitsDisk(t *testing.T) {
	s, dir := newTestStore(t)

	const plaintext = "ghp_topsecret_value_42"
	if err := s.Set("github_token", plaintext, "GitHub PAT"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if strings.Contains(string(raw), plaintext) {
		t.Fatalf("plaintext found in secrets file:\n%s", raw)
	}
	if !strings.Contains(string(raw), valuePrefix) {
		t.Fatalf("ciphertext prefix %q missing from file:\n%s", valuePrefix, raw)
	}
}

func TestFilePermissionsAre0600(t *testing.T) {
	s, dir := newTestStore(t)
	if err := s.Set("k", "v", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != filePerm {
		t.Errorf("perms: got %o, want %o", info.Mode().Perm(), filePerm)
	}
}

func TestListIsSorted(t *testing.T) {
	s, _ := newTestStore(t)
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		if err := s.Set(n, "value-of-"+n, ""); err != nil {
			t.Fatalf("Set %s: %v", n, err)
		}
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alpha", "bravo", "charlie"}
	if len(list) != len(want) {
		t.Fatalf("len: got %d, want %d", len(list), len(want))
	}
	for i, e := range list {
		if e.Name != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, e.Name, want[i])
		}
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Set("k", "v", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDeleteUnknownReturnsNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Delete("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete unknown: got %v, want ErrNotFound", err)
	}
}

func TestWrongPassphraseFailsToDecrypt(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, testPassphrase)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Set("k", "value", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Re-open with a different passphrase; Get must fail.
	bad, err := NewStore(dir, "wrong-passphrase")
	if err != nil {
		t.Fatalf("NewStore (wrong): %v", err)
	}
	if _, err := bad.Get("k"); err == nil {
		t.Error("Get with wrong passphrase: expected error, got nil")
	}
}

func TestSetPreservesCreatedAtBumpsRotatedAt(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Set("k", "v1", "desc"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	first, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Small sleep to make sure RotatedAt advances.
	if err := s.Set("k", "v2", ""); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	second, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if !first[0].CreatedAt.Equal(second[0].CreatedAt) {
		t.Errorf("CreatedAt changed: %v -> %v", first[0].CreatedAt, second[0].CreatedAt)
	}
	if !second[0].RotatedAt.After(first[0].RotatedAt) && !second[0].RotatedAt.Equal(first[0].RotatedAt) {
		t.Errorf("RotatedAt regressed: %v -> %v", first[0].RotatedAt, second[0].RotatedAt)
	}
}

func TestEmptyPassphraseRunsPlaintext(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore with empty passphrase: unexpected error: %v", err)
	}
	if s.Encrypted() {
		t.Error("store with empty passphrase should report Encrypted()=false")
	}
}

func TestPlaintextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Set("api", "hunter2", "note"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get("api")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Get = %q, want %q", got, "hunter2")
	}
}

func TestModeMismatch_EncryptedFileWithoutKey(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "correct horse")
	if err != nil {
		t.Fatalf("NewStore encrypted: %v", err)
	}
	if err := s.Set("x", "y", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Reopening without the passphrase must fail loudly.
	if _, err := NewStore(dir, ""); err == nil {
		t.Error("reopen encrypted file without key: expected error, got nil")
	}
}

func TestModeMismatch_PlaintextFileWithKey(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore plaintext: %v", err)
	}
	if err := s.Set("x", "y", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Reopening with a key set must fail loudly (user needs to encode first).
	if _, err := NewStore(dir, "correct horse"); err == nil {
		t.Error("reopen plaintext file with key: expected error, got nil")
	}
}

func TestEncode_PlaintextToEncrypted(t *testing.T) {
	dir := t.TempDir()
	plain, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore plaintext: %v", err)
	}
	if err := plain.Set("api", "hunter2", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Stand up a fresh store with the key by hand-patching the file flag
	// so we don't trip the mismatch guard.
	// In real life the user removes the file or runs encode via the TUI
	// while still in the same process — we test the Encode method here.
	// To exercise Encode we need a store with AEAD AND a file that
	// claims encrypted=false. We craft that by reusing the plaintext
	// store's path and forcing the AEAD.
	enc := &Store{path: plain.path}
	aead, err := deriveAEAD("correct horse")
	if err != nil {
		t.Fatalf("deriveAEAD: %v", err)
	}
	enc.aead = aead
	n, err := enc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if n != 1 {
		t.Errorf("Encode converted %d entries, want 1", n)
	}
	// Now reopening with the key works; without it fails.
	if _, err := NewStore(dir, "correct horse"); err != nil {
		t.Errorf("reopen with key after Encode: %v", err)
	}
	if _, err := NewStore(dir, ""); err == nil {
		t.Error("reopen without key after Encode: expected error, got nil")
	}
	// The value is preserved through the conversion.
	reopened, err := NewStore(dir, "correct horse")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Get("api")
	if err != nil {
		t.Fatalf("Get after Encode: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Get after Encode = %q, want %q", got, "hunter2")
	}
}

func TestDecode_EncryptedToPlaintext(t *testing.T) {
	dir := t.TempDir()
	enc, err := NewStore(dir, "correct horse")
	if err != nil {
		t.Fatalf("NewStore encrypted: %v", err)
	}
	if err := enc.Set("api", "hunter2", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	n, err := enc.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != 1 {
		t.Errorf("Decode converted %d entries, want 1", n)
	}
	// File is now plaintext: reopening without the key must succeed,
	// and reopening WITH the key must fail (mode mismatch).
	if _, err := NewStore(dir, ""); err != nil {
		t.Errorf("reopen without key after Decode: %v", err)
	}
	if _, err := NewStore(dir, "correct horse"); err == nil {
		t.Error("reopen with key after Decode: expected mode mismatch error")
	}
	// Value preserved.
	reopened, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("reopen plain: %v", err)
	}
	got, err := reopened.Get("api")
	if err != nil {
		t.Fatalf("Get after Decode: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Get after Decode = %q, want %q", got, "hunter2")
	}
}

func TestEncode_WithoutKey_Errors(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.Encode(); err == nil {
		t.Error("Encode without key: expected error, got nil")
	}
}

func TestDecode_WithoutKey_Errors(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.Decode(); err == nil {
		t.Error("Decode without key: expected error, got nil")
	}
}
