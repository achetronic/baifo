// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package anthropic

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ─── injectBillingHeader ──────────────────────────────────────────────────────

// parseSystemArray unmarshals the "system" key of the given JSON body into a
// slice of generic objects. Fails the test immediately if the body or the
// system field are not valid JSON arrays.
func parseSystemArray(t *testing.T, body []byte) []map[string]interface{} {
	t.Helper()
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	var sys []map[string]interface{}
	if err := json.Unmarshal(msg["system"], &sys); err != nil {
		t.Fatalf("system field is not a JSON array: %v (raw: %s)", err, msg["system"])
	}
	return sys
}

// findBillingBlock returns the first element of sys whose "text" starts with
// billingHeaderPrefix, or nil if none is found.
func findBillingBlock(sys []map[string]interface{}) map[string]interface{} {
	for _, block := range sys {
		if text, ok := block["text"].(string); ok && strings.HasPrefix(text, billingHeaderPrefix) {
			return block
		}
	}
	return nil
}

func TestInjectBillingHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		check func(t *testing.T, in, out []byte)
	}{
		{
			// (a) Body without "messages" key must be returned unchanged.
			name:  "no_messages_key_returns_unchanged",
			input: `{"model":"claude-3-5-sonnet","max_tokens":100}`,
			check: func(t *testing.T, in, out []byte) {
				if string(in) != string(out) {
					t.Errorf("expected unchanged body, got %s", out)
				}
			},
		},
		{
			// (b) Body with "messages" and no "system" → system becomes a
			// single-element array containing only the billing block.
			name:  "messages_no_system_creates_billing_array",
			input: `{"messages":[{"role":"user","content":"hi"}]}`,
			check: func(t *testing.T, _, out []byte) {
				sys := parseSystemArray(t, out)
				if len(sys) != 1 {
					t.Fatalf("expected 1 system block, got %d", len(sys))
				}
				if findBillingBlock(sys) == nil {
					t.Error("billing entry not found in system array")
				}
			},
		},
		{
			// (c) system is a JSON STRING → must be converted to an array
			// [billingBlock, {type:"text", text:<orig>, cache_control:...}].
			name:  "system_string_converted_to_array",
			input: `{"messages":[{"role":"user","content":"hi"}],"system":"be helpful"}`,
			check: func(t *testing.T, _, out []byte) {
				sys := parseSystemArray(t, out)
				if len(sys) != 2 {
					t.Fatalf("expected 2 system blocks, got %d", len(sys))
				}
				if findBillingBlock(sys) == nil {
					t.Error("billing entry not found")
				}
				// Original text must be preserved in the second block.
				origText, ok := sys[1]["text"].(string)
				if !ok {
					t.Fatal("system[1].text is not a string")
				}
				if origText != "be helpful" {
					t.Errorf("original system text not preserved: got %q", origText)
				}
				// cache_control must be present on the original block.
				if sys[1]["cache_control"] == nil {
					t.Error("cache_control missing on original system block")
				}
			},
		},
		{
			// (d) system is already a JSON ARRAY → billing block prepended,
			// original element preserved at index 1.
			name:  "system_array_billing_prepended",
			input: `{"messages":[{"role":"user","content":"hi"}],"system":[{"type":"text","text":"existing"}]}`,
			check: func(t *testing.T, _, out []byte) {
				sys := parseSystemArray(t, out)
				if len(sys) != 2 {
					t.Fatalf("expected 2 system blocks, got %d", len(sys))
				}
				// First block must be the billing header.
				firstText, ok := sys[0]["text"].(string)
				if !ok || !strings.HasPrefix(firstText, billingHeaderPrefix) {
					t.Errorf("first block is not the billing header, got %q", firstText)
				}
				// Original block preserved at position 1.
				origText, ok := sys[1]["text"].(string)
				if !ok {
					t.Fatal("system[1].text is not a string")
				}
				if origText != "existing" {
					t.Errorf("original text not preserved: got %q", origText)
				}
			},
		},
		{
			// (e) Body already contains the billing prefix → returned unchanged
			// (idempotent).
			name:  "already_has_billing_header_is_idempotent",
			input: `{"messages":[{"role":"user","content":"hi"}],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.77; cc_entrypoint=cli; cch=00000;"}]}`,
			check: func(t *testing.T, in, out []byte) {
				if string(in) != string(out) {
					t.Errorf("expected unchanged body (idempotent), got %s", out)
				}
			},
		},
		{
			// (f) Invalid JSON → returned unchanged.
			name:  "invalid_json_returns_unchanged",
			input: `not valid json {`,
			check: func(t *testing.T, in, out []byte) {
				if string(in) != string(out) {
					t.Errorf("expected unchanged body for invalid JSON, got %s", out)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := []byte(tc.input)
			out := injectBillingHeader(in)
			tc.check(t, in, out)
		})
	}
}

// ─── generatePKCE ─────────────────────────────────────────────────────────────

func TestGeneratePKCE(t *testing.T) {
	t.Parallel()

	t.Run("verifier_length_in_rfc7636_range", func(t *testing.T) {
		t.Parallel()
		v, _, err := generatePKCE()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(v) < 43 || len(v) > 128 {
			t.Errorf("verifier length %d not in [43, 128] (RFC 7636)", len(v))
		}
	})

	t.Run("challenge_is_s256_of_verifier", func(t *testing.T) {
		t.Parallel()
		v, ch, err := generatePKCE()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		h := sha256.Sum256([]byte(v))
		expected := base64.RawURLEncoding.EncodeToString(h[:])
		if ch != expected {
			t.Errorf("challenge mismatch:\n got  %q\n want %q", ch, expected)
		}
	})

	t.Run("two_calls_produce_different_verifiers", func(t *testing.T) {
		t.Parallel()
		v1, _, err1 := generatePKCE()
		v2, _, err2 := generatePKCE()
		if err1 != nil || err2 != nil {
			t.Fatalf("unexpected errors: %v, %v", err1, err2)
		}
		if v1 == v2 {
			t.Error("two consecutive generatePKCE calls returned identical verifiers")
		}
	})
}

// ─── TokenSet.IsExpired ───────────────────────────────────────────────────────

func TestTokenSetIsExpired(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "past_token_is_expired",
			expiresAt: time.Now().Add(-1 * time.Hour),
			want:      true,
		},
		{
			name:      "far_future_token_is_not_expired",
			expiresAt: time.Now().Add(1 * time.Hour),
			want:      false,
		},
		{
			// Within the 5-minute buffer the token is considered expired so
			// that refresh happens proactively before the actual expiry.
			name:      "within_5min_buffer_is_expired",
			expiresAt: time.Now().Add(3 * time.Minute),
			want:      true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := &TokenSet{ExpiresAt: tc.expiresAt}
			got := ts.IsExpired()
			if got != tc.want {
				t.Errorf("IsExpired() = %v, want %v (ExpiresAt=%v, now=%v)",
					got, tc.want, tc.expiresAt, time.Now())
			}
		})
	}
}
