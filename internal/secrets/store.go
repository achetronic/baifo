// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package secrets implements the encrypted-at-rest secret store described
// in .agents/SECRETS.md.
//
// Storage: one YAML file (`secrets.yaml`) containing a version tag and a
// map of name → entry. Each entry's value is serialised as
//
//	AES256GCM:v1:<base64 nonce>:<base64 ciphertext>
//
// Key derivation: PBKDF2(passphrase, salt="baifo-secrets-v1",
// iter=200000, len=32). The passphrase comes from `baifo.encryption_key`
// (see internal/config).
//
// When `baifo.encryption_key` is empty the store runs in plaintext mode:
// the file is still well-formed YAML but values are serialised verbatim
// with the `plain:v1:<base64>` prefix. This mode is meant for local
// development; the `encrypted: false` flag at the top of the file makes
// the mode explicit so accidents (key configured later) surface as a
// loud error instead of "can't decrypt".
//
// Moving between modes is explicit and supported via Encode / Decode
// (exposed in the TUI as `/secret encode` and `/secret decode`).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/pbkdf2"
	yaml "gopkg.in/yaml.v3"
)

const (
	// fileName is the on-disk name of the secrets file inside .baifo/.
	fileName = "secrets.yaml"

	// filePerm is the strict permission mode enforced on every write
	// (owner read+write only — see threat model in SECRETS.md).
	filePerm os.FileMode = 0o600

	// fileVersion is the schema version embedded at the top of the YAML.
	fileVersion = 1

	// valuePrefix tags the serialised ciphertext format so we can rev it.
	valuePrefix = "AES256GCM:v1"

	// plainPrefix tags base64-encoded plaintext values when the store
	// runs without an encryption key. The base64 wrapper keeps the YAML
	// shape identical to the encrypted mode (single line, no surprises
	// for downstream parsers) and avoids accidental shell mangling.
	plainPrefix = "plain:v1"

	// kdfSalt is the fixed v1 salt (justified in SECRETS.md: single-user,
	// "encrypt at rest"). Bump to v2 when introducing per-install salts.
	kdfSalt = "baifo-secrets-v1"

	// kdfIterations matches the spec in SECRETS.md.
	kdfIterations = 200_000

	// kdfKeyLength is the AES-256 key length in bytes.
	kdfKeyLength = 32
)

// ErrNotFound is returned by Get and Delete when the requested secret
// does not exist.
var ErrNotFound = errors.New("secret not found")

// Entry is the public representation of a secret as listed by the CLI.
// The raw value is never embedded here.
type Entry struct {
	Name        string
	Description string
	CreatedAt   time.Time
	RotatedAt   time.Time
}

// fileEntry is the on-disk serialisation of a single secret.
type fileEntry struct {
	Description string    `yaml:"description,omitempty"`
	CreatedAt   time.Time `yaml:"created_at"`
	RotatedAt   time.Time `yaml:"rotated_at"`
	Value       string    `yaml:"value"`
}

// fileSchema is the root of secrets.yaml.
//
// Encrypted records the mode the file was last written in. It defaults
// to true when missing (legacy files written before the flag existed
// were always encrypted), so old installations keep working without
// migration.
type fileSchema struct {
	Version   int                  `yaml:"version"`
	Encrypted *bool                `yaml:"encrypted,omitempty"`
	Secrets   map[string]fileEntry `yaml:"secrets"`
}

// Store is the entry point. When aead is nil the store runs in
// plaintext mode; otherwise every value is AES-256-GCM sealed before
// hitting disk.
type Store struct {
	path string
	aead cipher.AEAD
	// gen is incremented atomically on every successful write so
	// external caches (e.g. LogRedactor) can detect staleness
	// without decrypting every entry on each check.
	gen atomic.Uint64
}

// Encrypted reports whether the store will write new values encrypted.
// Mirrors the on-disk flag so callers can render a badge.
func (s *Store) Encrypted() bool { return s.aead != nil }

// Generation returns the current mutation counter. It increments
// atomically on every successful write (Set, Delete, Encode, Decode)
// so external caches can detect stale snapshots without decrypting
// every entry on each check. The value starts at zero and wraps on
// overflow (2^64 iterations of baifo are not expected in a session).
func (s *Store) Generation() uint64 { return s.gen.Load() }

// NewStore opens (or initialises) the secret store living in <dir>.
// The passphrase is the value of baifo.encryption_key from baifo.yaml.
// An empty passphrase puts the store in plaintext mode (intended for
// local development); a non-empty passphrase enables AES-256-GCM.
//
// If the file already exists, its `encrypted` flag must match the
// configured mode — otherwise NewStore returns a clear error explaining
// how to reconcile (use `/secret encode` or `/secret decode`). This
// guards against the silent footgun of "key was unset, secrets now
// unreadable" or "key was set, secrets still plaintext on disk".
func NewStore(dir, passphrase string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ensure secrets directory: %w", err)
	}

	s := &Store{
		path: filepath.Join(dir, fileName),
	}
	if passphrase != "" {
		aead, err := deriveAEAD(passphrase)
		if err != nil {
			return nil, err
		}
		s.aead = aead
	}

	// First-run: create an empty file in the configured mode.
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		if err := s.write(fileSchema{
			Version:   fileVersion,
			Encrypted: boolPtr(s.aead != nil),
			Secrets:   map[string]fileEntry{},
		}); err != nil {
			return nil, err
		}
		return s, nil
	}

	// File exists: check mode consistency before returning the handle.
	schema, err := s.read()
	if err != nil {
		return nil, err
	}
	onDiskEncrypted := schema.Encrypted == nil || *schema.Encrypted
	wantEncrypted := s.aead != nil
	if onDiskEncrypted != wantEncrypted {
		if wantEncrypted {
			return nil, fmt.Errorf(
				"secrets file is unencrypted but encryption_key is configured; " +
					"run `/secret encode` to encrypt existing entries, or remove encryption_key from baifo.yaml")
		}
		return nil, fmt.Errorf(
			"secrets file is encrypted but encryption_key is not configured; " +
				"set encryption_key in baifo.yaml (and run `/secret decode` later if you want plaintext)")
	}
	return s, nil
}

func boolPtr(b bool) *bool { return &b }

// deriveAEAD turns a passphrase into a ready-to-use AES-256-GCM AEAD.
func deriveAEAD(passphrase string) (cipher.AEAD, error) {
	key := pbkdf2.Key([]byte(passphrase), []byte(kdfSalt), kdfIterations, kdfKeyLength, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init GCM: %w", err)
	}
	return aead, nil
}

// Set encrypts and writes (or replaces) the secret named name.
// If the secret already exists, its CreatedAt is preserved and only
// RotatedAt is bumped.
func (s *Store) Set(name, value, description string) error {
	if name == "" {
		return errors.New("secret name cannot be empty")
	}

	serialised, err := s.seal(value)
	if err != nil {
		return err
	}

	schema, err := s.read()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	entry := schema.Secrets[name]
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.RotatedAt = now
	entry.Value = serialised
	if description != "" {
		entry.Description = description
	}
	schema.Secrets[name] = entry
	schema.Encrypted = boolPtr(s.aead != nil)

	return s.write(schema)
}

// Encode re-seals every plaintext entry into the encrypted format.
// Requires the store to be in encrypted mode (i.e. encryption_key is
// configured). It is a no-op when there is nothing to convert, so it
// is safe to call repeatedly. Returns the number of entries converted.
func (s *Store) Encode() (int, error) {
	if s.aead == nil {
		return 0, errors.New("encryption_key is not configured; cannot encode")
	}
	schema, err := s.read()
	if err != nil {
		return 0, err
	}
	converted := 0
	for name, entry := range schema.Secrets {
		if !strings.HasPrefix(entry.Value, plainPrefix+":") {
			continue
		}
		encoded := strings.TrimPrefix(entry.Value, plainPrefix+":")
		pt, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return converted, fmt.Errorf("decode plaintext for %q: %w", name, err)
		}
		sealed, err := s.seal(string(pt))
		if err != nil {
			return converted, fmt.Errorf("seal %q: %w", name, err)
		}
		entry.Value = sealed
		schema.Secrets[name] = entry
		converted++
	}
	schema.Encrypted = boolPtr(true)
	if err := s.write(schema); err != nil {
		return converted, err
	}
	return converted, nil
}

// Decode unwraps every encrypted entry into the plaintext format.
// Requires the store to currently hold the AEAD (so it can decrypt),
// but flips the on-disk flag to false so future Sets stay plaintext
// and `baifo.encryption_key` can be removed from the config.
//
// IMPORTANT: this is intentionally destructive of confidentiality.
// Callers (the TUI) MUST present a confirmation prompt before invoking.
// Returns the number of entries converted.
func (s *Store) Decode() (int, error) {
	if s.aead == nil {
		return 0, errors.New("encryption_key is not configured; cannot decode (no key to read the ciphertext with)")
	}
	schema, err := s.read()
	if err != nil {
		return 0, err
	}
	converted := 0
	for name, entry := range schema.Secrets {
		if !strings.HasPrefix(entry.Value, valuePrefix+":") {
			continue
		}
		pt, err := s.open(entry.Value)
		if err != nil {
			return converted, fmt.Errorf("open %q: %w", name, err)
		}
		entry.Value = plainPrefix + ":" + base64.StdEncoding.EncodeToString([]byte(pt))
		schema.Secrets[name] = entry
		converted++
	}
	schema.Encrypted = boolPtr(false)
	// After Decode the on-disk file is plaintext, but the in-memory
	// Store still holds the AEAD because the caller is mid-session and
	// may still call Get. The next time baifo boots without the key it
	// will run in plaintext mode and read the file fine.
	if err := s.write(schema); err != nil {
		return converted, err
	}
	return converted, nil
}

// Get decrypts and returns the value of the secret named name.
// Returns ErrNotFound when the name is unknown.
func (s *Store) Get(name string) (string, error) {
	schema, err := s.read()
	if err != nil {
		return "", err
	}
	entry, ok := schema.Secrets[name]
	if !ok {
		return "", ErrNotFound
	}
	return s.open(entry.Value)
}

// Delete removes a secret. Returns ErrNotFound if it was not stored.
func (s *Store) Delete(name string) error {
	schema, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := schema.Secrets[name]; !ok {
		return ErrNotFound
	}
	delete(schema.Secrets, name)
	schema.Encrypted = boolPtr(s.aead != nil)
	return s.write(schema)
}

// List returns the public metadata of every stored secret, sorted by
// name. Values are never returned by this method.
func (s *Store) List() ([]Entry, error) {
	schema, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(schema.Secrets))
	for name, entry := range schema.Secrets {
		out = append(out, Entry{
			Name:        name,
			Description: entry.Description,
			CreatedAt:   entry.CreatedAt,
			RotatedAt:   entry.RotatedAt,
		})
	}
	// Stable, alphabetical order — useful for CLI output and tests.
	sortByName(out)
	return out, nil
}

// Snapshot returns every secret's name → plaintext value in one
// shot. Unlike List, this method DOES decrypt every entry, so the
// returned map carries raw values. Callers that handle the result
// MUST keep it in memory only, never log it, and let the map go out
// of scope as soon as possible.
//
// The intended consumer is the AfterToolCallback redactor's
// comprehensive-scrub pass (see SECRETS.md), which needs to scan a
// tool result for every stored value at once instead of just the
// values the BeforeToolCallback substituted. Decrypting once per
// tool call (vs. N times via Get) avoids thrashing AES-GCM.
//
// An entry whose ciphertext is malformed is silently skipped — the
// redactor has no recourse to recover from a bad seal and we prefer
// degraded redaction over a hard failure that would block every
// tool call. The CLI's `baifo secrets list` will still surface the
// broken entry separately.
func (s *Store) Snapshot() (map[string]string, error) {
	schema, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(schema.Secrets))
	for name, entry := range schema.Secrets {
		val, err := s.open(entry.Value)
		if err != nil {
			continue
		}
		out[name] = val
	}
	return out, nil
}

// seal serialises plaintext according to the current store mode.
// Encrypted mode produces AES256GCM:v1:nonce:ct; plaintext mode
// produces plain:v1:<base64>.
func (s *Store) seal(plaintext string) (string, error) {
	if s.aead == nil {
		return plainPrefix + ":" + base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	ct := s.aead.Seal(nil, nonce, []byte(plaintext), nil)

	return strings.Join([]string{
		valuePrefix,
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ct),
	}, ":"), nil
}

// open is the inverse of seal. Detects both formats by prefix and
// rejects payloads whose mode does not match the store state (e.g.
// encrypted entry in a plaintext store — should not happen because
// NewStore catches it earlier, but defence in depth never hurts).
func (s *Store) open(serialised string) (string, error) {
	switch {
	case strings.HasPrefix(serialised, plainPrefix+":"):
		encoded := strings.TrimPrefix(serialised, plainPrefix+":")
		pt, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("decode plaintext payload: %w", err)
		}
		return string(pt), nil
	case strings.HasPrefix(serialised, valuePrefix+":"):
		if s.aead == nil {
			return "", errors.New("encrypted entry found but store is in plaintext mode")
		}
		parts := strings.SplitN(serialised, ":", 4)
		if len(parts) != 4 {
			return "", errors.New("malformed secret payload")
		}
		nonce, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			return "", fmt.Errorf("decode nonce: %w", err)
		}
		ct, err := base64.StdEncoding.DecodeString(parts[3])
		if err != nil {
			return "", fmt.Errorf("decode ciphertext: %w", err)
		}
		pt, err := s.aead.Open(nil, nonce, ct, nil)
		if err != nil {
			return "", fmt.Errorf("decrypt: %w", err)
		}
		return string(pt), nil
	default:
		return "", fmt.Errorf("unsupported secret format")
	}
}

// read loads and parses secrets.yaml. An empty or missing Secrets map is
// normalised to an empty (non-nil) one so callers can write to it safely.
func (s *Store) read() (fileSchema, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return fileSchema{}, fmt.Errorf("read secrets file: %w", err)
	}
	var schema fileSchema
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &schema); err != nil {
			return fileSchema{}, fmt.Errorf("parse secrets file: %w", err)
		}
	}
	if schema.Version == 0 {
		schema.Version = fileVersion
	}
	if schema.Secrets == nil {
		schema.Secrets = map[string]fileEntry{}
	}
	return schema, nil
}

// write serialises and atomically replaces secrets.yaml with 0600 perms.
// On success it increments the generation counter so external caches
// (e.g. LogRedactor) know the on-disk state changed.
func (s *Store) write(schema fileSchema) error {
	data, err := yaml.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal secrets file: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return fmt.Errorf("write temp secrets file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace secrets file: %w", err)
	}
	// Belt and braces: enforce 0600 even if umask was weird at create time.
	if err := os.Chmod(s.path, filePerm); err != nil {
		return err
	}
	s.gen.Add(1)
	return nil
}
