// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"context"
	"fmt"
	"regexp"

	"github.com/achetronic/baifo/internal/facade"
)

// secretRefRE matches the ${secret:NAME} placeholder syntax shared by
// every config field that can reference the secrets store. Kept local
// to the App so the expansion entry point lives next to its consumer;
// the providers package keeps its own copy for the same reason
// (avoiding a cross-package dependency on a shared regexp var).
var secretRefRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9_\-]+)\}`)

// SetSecret implements facade.Facade. Writes the (name, value) pair to the
// secrets store under secrets.yaml. description is the human-readable
// note that the secrets list surfaces — never the value itself. Both
// name and value are required.
//
// The store runs encrypted when baifo.encryption_key is set, and in
// plaintext mode otherwise (intended for local development).
//
// We do NOT trigger ReloadFromDisk here: secrets are read on demand
// by the secrets store, not snapshotted into the running App state.
// A facade.ReloadEvent is emitted so any open Settings overlay refreshes
// its "(none / N entries)" badge.
func (a *App) SetSecret(ctx context.Context, name, value, description string) error {
	if a.secrets == nil {
		return fmt.Errorf("secrets store not initialised")
	}
	if name == "" {
		return fmt.Errorf("missing name")
	}
	if value == "" {
		return fmt.Errorf("missing value")
	}
	if err := a.secrets.Set(name, value, description); err != nil {
		return fmt.Errorf("set secret: %w", err)
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{}:
	default:
	}
	return nil
}

// DeleteSecret implements facade.Facade. Removes the named secret from the
// store. Idempotent: missing names are not an error because the
// user's mental model is "make sure it's gone", and the Delete
// prompt already confirmed.
func (a *App) DeleteSecret(ctx context.Context, name string) error {
	if a.secrets == nil {
		return fmt.Errorf("secrets store not initialised")
	}
	if err := a.secrets.Delete(name); err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{}:
	default:
	}
	return nil
}

// SecretsEncrypted reports whether the secrets store is currently
// running in encrypted mode. The TUI surfaces this as a badge in the
// Settings overlay so the user knows at a glance whether their secrets
// are at-rest encrypted.
func (a *App) SecretsEncrypted() bool {
	if a.secrets == nil {
		return false
	}
	return a.secrets.Encrypted()
}

// EncodeSecrets re-seals every plaintext entry into the encrypted
// format. Requires baifo.encryption_key to be configured. Returns the
// number of entries converted (zero is fine, the call is idempotent).
func (a *App) EncodeSecrets(ctx context.Context) (int, error) {
	if a.secrets == nil {
		return 0, fmt.Errorf("secrets store not initialised")
	}
	n, err := a.secrets.Encode()
	if err != nil {
		return n, fmt.Errorf("encode secrets: %w", err)
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{}:
	default:
	}
	return n, nil
}

// DecodeSecrets unwraps every encrypted entry into the plaintext
// format and flips the on-disk flag. Requires baifo.encryption_key to
// be configured (we need the AEAD to decrypt). DESTRUCTIVE of
// confidentiality — callers MUST confirm with the user first.
// Returns the number of entries converted.
func (a *App) DecodeSecrets(ctx context.Context) (int, error) {
	if a.secrets == nil {
		return 0, fmt.Errorf("secrets store not initialised")
	}
	n, err := a.secrets.Decode()
	if err != nil {
		return n, fmt.Errorf("decode secrets: %w", err)
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{}:
	default:
	}
	return n, nil
}

// ExpandSecretString resolves every ${secret:NAME} placeholder in s
// against the secrets store and returns the expanded string. A literal
// string with no placeholder is returned unchanged. When the store is
// not configured the placeholder is left intact and no error is
// raised, matching the rest of baifo's "secrets are optional in dev"
// posture (callers that require a real value validate it themselves).
//
// Used by the boot path to resolve config fields that may reference a
// secret — e.g. the A2A bearer token (a2a.credentials.token) — so the
// real value never has to live in baifo.yaml in plaintext.
func (a *App) ExpandSecretString(s string) (string, error) {
	if !secretRefRE.MatchString(s) {
		return s, nil
	}
	if a.secrets == nil {
		return s, nil
	}
	var firstErr error
	out := secretRefRE.ReplaceAllStringFunc(s, func(match string) string {
		name := secretRefRE.FindStringSubmatch(match)[1]
		value, err := a.secrets.Get(name)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("expand secret %q: %w", name, err)
			}
			return match
		}
		return value
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}
