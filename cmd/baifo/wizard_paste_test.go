// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestSanitisePastedLine checks that pasting into a single-line wizard
// field strips control characters (the common case: an API key copied
// with a trailing newline) while preserving every printable rune.
func TestSanitisePastedLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"trailing newline", "sk-ant-abc123\n", "sk-ant-abc123"},
		{"crlf", "key\r\n", "key"},
		{"embedded newlines", "a\nb\nc", "abc"},
		{"tabs", "a\tb", "ab"},
		{"clean key untouched", "AIza-Key_123.456", "AIza-Key_123.456"},
		{"url with slashes", "http://localhost:11434/v1", "http://localhost:11434/v1"},
		{"unicode preserved", "clé-éàü", "clé-éàü"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitisePastedLine(c.in); got != c.want {
				t.Errorf("sanitisePastedLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
