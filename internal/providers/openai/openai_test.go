// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package openai

import (
	"testing"
)

// dialectFor routes known hosts to their dialect and falls back to nil,
// the OpenAI-pure shape, for everything else: local servers, private
// gateways and hosts nobody has probed.
func TestDialectFor(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		wantNil  bool
		wantName string
	}{
		{"openrouter", "https://openrouter.ai/api/v1", false, "openrouter"},
		{"deepseek", "https://api.deepseek.com/v1", false, "deepseek"},
		{"hyper", "https://hyper.charm.land/v1", false, "text"},
		{"local ollama falls back to nil", "http://localhost:11434/v1", true, ""},
		{"private gateway falls back to nil", "https://gateway.internal/v1", true, ""},
		{"opencode zen falls back to nil", "https://opencode.ai/zen/go/v1", true, ""},
		{"empty url falls back to nil", "", true, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dialectFor(c.url, "test")
			if c.wantNil {
				if got != nil {
					t.Errorf("dialect = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("dialect = nil, want %q", c.wantName)
			}
			if got.Name() != c.wantName {
				t.Errorf("dialect = %q, want %q", got.Name(), c.wantName)
			}
		})
	}
}
