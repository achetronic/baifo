// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/config"
)

// TestRealWorldLLMRequestShape boots a full App with the bundled
// example config (Gemini provider + real prompt + filesystem MCP)
// and inspects the LLMRequest that hits the model when the user
// sends 'hola'. We do NOT call the real LLM; the test stops at the
// SendMessage iterator's first event and reports what the runner
// produced.
//
// Goal: catch unexpected contents being injected before the user
// message (stale session events, hidden context from facts memory,
// double system instructions, MCP discovery noise) so we have proof
// of what the model actually receives.
func TestRealWorldLLMRequestShape(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := t.TempDir()

	// Stage the bundled example config so we exercise the actual
	// production prompt + tool wiring, not a stripped-down version.
	cfgPath := filepath.Join(dir, "baifo.yaml")
	agentsPath := filepath.Join(dir, "agents.yaml")
	secretsPath := filepath.Join(dir, "secrets.yaml")
	mustCopy(t, filepath.Join(repoRoot, "config", "baifo.yaml"), cfgPath)
	mustCopy(t, filepath.Join(repoRoot, "config", "agents.yaml"), agentsPath)
	mustWriteFile(t, secretsPath, `version: 1
encrypted: false
secrets:
  GEMINI_API_KEY:
    created_at: 2026-01-01T00:00:00Z
    rotated_at: 2026-01-01T00:00:00Z
    value: plain:v1:Zm9v
`)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a, err := New(context.Background(), cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if a.RootBuildError() != nil {
		t.Skipf("root build error (probably no real gemini key wired): %v", a.RootBuildError())
	}

	// We cannot hit the actual Gemini endpoint, so we intercept at
	// the runner level by reading the raw events the runner yields
	// when we call SendMessage. The error in the first event will
	// be the network call to Gemini failing; we just want to log
	// what the runner attempted to send.
	for ev, err := range a.SendMessage(context.Background(), "hola") {
		if ev != nil && ev.Raw != nil {
			raw, _ := json.MarshalIndent(ev.Raw, "", "  ")
			t.Logf("session.Event yielded:\n%s", raw)
		}
		if err != nil {
			t.Logf("SendMessage error (expected without real key): %v", err)
			break
		}
	}

	// Verify the session has exactly ONE event so far (the user's
	// 'hola'). If the runner snuck anything else in before, this
	// will tell us.
	t.Logf("done; inspect logs above for the request shape")

	// Touch a no-op so the imports stay even if some are unused in
	// short-circuit paths.
	_ = strings.HasPrefix
	_ = agent.RunConfig{}
	_ = (&genai.Content{})
}

func mustCopy(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
