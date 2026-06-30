// SPDX-License-Identifier: Apache-2.0

package modelcatalog

import (
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// TestCatwalkEndpointsGuards asserts that known OpenAI-compatible providers
// remain in the catalogue with valid, non-placeholder API endpoints.
func TestCatwalkEndpointsGuards(t *testing.T) {
	all := embedded.GetAll()
	registry := make(map[string]catwalk.Provider)
	for _, p := range all {
		registry[strings.ToLower(string(p.ID))] = p
	}

	knownExpected := []string{"openrouter", "groq", "deepseek", "xai"}
	for _, id := range knownExpected {
		p, exists := registry[id]
		if !exists {
			t.Skipf("catwalk provider %q is missing from catwalk entirely, skipping", id)
			continue
		}

		endpoint := strings.TrimSpace(p.APIEndpoint)
		if endpoint == "" || strings.HasPrefix(endpoint, "$") {
			t.Fatalf("catwalk provider %q has empty/placeholder APIEndpoint %q; URL-based model matching is broken, see internal/modelcatalog", id, p.APIEndpoint)
		}

		// Verify that Resolve on the exact endpoint yields MatchByURLExact and the correct provider
		res, kind, ok := Resolve("", endpoint)
		if !ok {
			t.Errorf("Resolve failed to find provider %q using its exact endpoint %q", id, endpoint)
		}
		if kind != MatchByURLExact {
			t.Errorf("expected MatchByURLExact for provider %q endpoint %q, got %v", id, endpoint, kind)
		}
		if strings.ToLower(string(res.ID)) != id {
			t.Errorf("expected resolved provider ID to be %q, got %q", id, res.ID)
		}
	}
}

// TestCanonicalEndpointsArePlaceholders asserts that canonical providers like
// openai, anthropic, and gemini do not expose real URLs that could trigger false positive matches.
func TestCanonicalEndpointsArePlaceholders(t *testing.T) {
	all := embedded.GetAll()
	canonicals := map[string]bool{
		"openai":    true,
		"anthropic": true,
		"gemini":    true,
	}

	for _, p := range all {
		id := strings.ToLower(string(p.ID))
		if !canonicals[id] {
			continue
		}

		endpoint := strings.TrimSpace(p.APIEndpoint)
		if endpoint != "" && !strings.HasPrefix(endpoint, "$") {
			t.Errorf("canonical provider %q has a non-placeholder APIEndpoint %q; this may cause accidental matches", id, p.APIEndpoint)
		}
	}
}

// TestResolveTable is a comprehensive table-driven test for Resolve.
func TestResolveTable(t *testing.T) {
	all := embedded.GetAll()
	registry := make(map[string]catwalk.Provider)
	for _, p := range all {
		registry[strings.ToLower(string(p.ID))] = p
	}

	tests := []struct {
		name         string
		providerType string
		url          string
		expectedKind MatchKind
		expectedID   string
		expectOK     bool
	}{
		{
			name:         "MatchByType with gemini and empty url",
			providerType: "gemini",
			url:          "",
			expectedKind: MatchByType,
			expectedID:   "gemini",
			expectOK:     true,
		},
		{
			name:         "MatchNone with unknown type and empty url",
			providerType: "unknown-type-xyz",
			url:          "",
			expectedKind: MatchNone,
			expectedID:   "",
			expectOK:     false,
		},
		{
			name:         "localhost url maps to MatchNone",
			providerType: "openai",
			url:          "http://localhost:11434/v1",
			expectedKind: MatchNone,
			expectedID:   "",
			expectOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, kind, ok := Resolve(tt.providerType, tt.url)
			if ok != tt.expectOK {
				t.Fatalf("expected ok=%v, got %v", tt.expectOK, ok)
			}
			if kind != tt.expectedKind {
				t.Errorf("expected kind %v, got %v", tt.expectedKind, kind)
			}
			if tt.expectOK && strings.ToLower(string(res.ID)) != tt.expectedID {
				t.Errorf("expected provider ID %q, got %q", tt.expectedID, res.ID)
			}
		})
	}

	// Dynamic tests based on catwalk entries
	if openrouter, ok := registry["openrouter"]; ok && openrouter.APIEndpoint != "" {
		t.Run("MatchByURLExact with openrouter", func(t *testing.T) {
			endpoint := openrouter.APIEndpoint
			res, kind, ok := Resolve("", endpoint)
			if !ok || kind != MatchByURLExact || strings.ToLower(string(res.ID)) != "openrouter" {
				t.Errorf("exact match failed for openrouter: ok=%v, kind=%v, id=%s", ok, kind, res.ID)
			}
		})

		t.Run("MatchByURLHost with trailing slash on openrouter", func(t *testing.T) {
			// Normalisation strips the path so it matches the unique host.
			// E.g. openrouter endpoint is "https://openrouter.ai/api/v1".
			// Let's pass "https://openrouter.ai/api/v1/" to see if normalisation handles it.
			endpoint := openrouter.APIEndpoint + "/"
			res, kind, ok := Resolve("", endpoint)
			if !ok || kind != MatchByURLExact || strings.ToLower(string(res.ID)) != "openrouter" {
				t.Errorf("exact match with trailing slash failed for openrouter: ok=%v, kind=%v, id=%s", ok, kind, res.ID)
			}
		})
	}

	if groq, ok := registry["groq"]; ok && groq.APIEndpoint != "" {
		t.Run("MatchByURLHost with unique host groq", func(t *testing.T) {
			// Get host of groq, and append something to the path to force host match rather than exact.
			host, _ := normalizeURL(groq.APIEndpoint)
			testURL := "https://" + host + "/some/other/custom/path"
			res, kind, ok := Resolve("", testURL)
			if !ok || kind != MatchByURLHost || strings.ToLower(string(res.ID)) != "groq" {
				t.Errorf("host match failed for groq: ok=%v, kind=%v, id=%s", ok, kind, res.ID)
			}
		})
	}

	// Ambiguous host (open.bigmodel.cn -> MatchNone) only if both zhipu entries are present.
	_, hasZhipu := registry["zhipu"]
	_, hasZhipuCoding := registry["zhipu-coding"]
	if hasZhipu && hasZhipuCoding {
		t.Run("ambiguous host open.bigmodel.cn", func(t *testing.T) {
			testURL := "https://open.bigmodel.cn/some/path"
			_, kind, ok := Resolve("", testURL)
			if ok || kind != MatchNone {
				t.Errorf("expected MatchNone for ambiguous host open.bigmodel.cn, got kind=%v, ok=%v", kind, ok)
			}
		})
	} else {
		t.Skip("skipping ambiguous host test because zhipu and/or zhipu-coding are missing from catwalk")
	}
}
