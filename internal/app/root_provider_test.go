// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// geminiRootNoProviderYAML declares a complete, flagged root agent
// whose provider is not declared in baifo.yaml: issue #9's shape.
const geminiRootNoProviderYAML = `
version: 1
agents:
  - name: baifo-coordinator
    root: true
    prompt: |
      you are baifo
    llm:
      provider: gemini
      model: gemini-1.5-flash
`

// TestApp_RootProviderNotDeclared_ReportsReason reproduces issue #9:
// the user flags a complete root entry whose provider baifo.yaml does
// not declare. baifo must report the provider gap, not a false "no
// root agent configured", which is what contradicted the agent index
// in the report: the chat said there was no root while /agent
// set-root insisted one existed.
func TestApp_RootProviderNotDeclared_ReportsReason(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalYAML)
	writeAgents(t, dir, geminiRootNoProviderYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// The root entry exists and is flagged: the index and the
	// set-root guard see it. The old code reported ErrNoRoot here
	// instead, which is exactly the contradiction of issue #9.
	if got := a.RootName(); got != "baifo-coordinator" {
		t.Fatalf("RootName = %q, want baifo-coordinator", got)
	}
	if a.RootAgent() != nil {
		t.Fatalf("RootAgent should be nil while its provider is missing")
	}

	buildErr := a.RootBuildError()
	if buildErr == nil {
		t.Fatal("RootBuildError is nil: the undeclared provider went silent (issue #9)")
	}
	msg := buildErr.Error()
	if !strings.Contains(msg, "baifo-coordinator") || !strings.Contains(msg, "gemini") ||
		!strings.Contains(msg, "not declared in baifo.yaml") {
		t.Errorf("error should name the agent, the provider and baifo.yaml, got: %s", msg)
	}
}

// TestApp_RootProviderNotDeclared_ClearsAfterFix covers the recovery
// path: declaring the provider and reloading clears the stashed error
// and yields a usable root. The reload itself must report success:
// the file on disk was always valid, only the root was degraded.
func TestApp_RootProviderNotDeclared_ClearsAfterFix(t *testing.T) {
	dir := t.TempDir()
	writeAgents(t, dir, geminiRootNoProviderYAML)
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte(`version: 1
encrypted: false
secrets:
  GEMINI_API_KEY:
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-01T00:00:00Z
    value: plain:v1:bm90LWEtcmVhbC1rZXk=
`), 0o600); err != nil {
		t.Fatalf("write secrets.yaml: %v", err)
	}
	writeConfig(t, dir, minimalYAML)

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if a.RootBuildError() == nil {
		t.Fatal("expected a stashed provider error before the fix")
	}

	// The user declares the provider in baifo.yaml. The api_key goes
	// through the ${secret:NAME} placeholder, resolved by the secrets
	// store pre-seeded above (boot only needs the key to be present,
	// not valid).
	writeConfig(t, dir, `version: 1
providers:
  - name: gemini
    type: gemini
    api_key: ${secret:GEMINI_API_KEY}
theme:
  nerd_fonts: false
`)
	if err := a.ReloadFromDisk(context.Background()); err != nil {
		t.Fatalf("reload after declaring the provider: %v", err)
	}
	if err := a.RootBuildError(); err != nil {
		t.Errorf("RootBuildError after fix: %v", err)
	}
	if a.RootAgent() == nil {
		t.Error("RootAgent is still nil after declaring the provider")
	}
}
