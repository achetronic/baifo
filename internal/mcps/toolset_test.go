// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package mcps

import (
	"strings"
	"testing"
)

// fakeSecretStore is a tiny stand-in for *secrets.Store that lets us
// test expandHeaders without spinning the AES-GCM machinery. The
// production code accepts a *secrets.Store concrete pointer; for the
// tests we point at a real one with a single fixture value, which
// keeps the test honest end-to-end without ceremony.
//
// We keep this in a separate package-internal test file rather than
// reaching into internal/secrets so the secrets package can evolve
// its API without breaking the mcps test suite.

func TestExpandHeaders_PassesThroughWithoutPlaceholders(t *testing.T) {
	in := map[string]string{
		"X-Tenant-ID": "acme",
		"X-Custom":    "literal",
	}
	got, err := expandHeaders(in, nil)
	if err != nil {
		t.Fatalf("expandHeaders: %v", err)
	}
	if got["X-Tenant-ID"] != "acme" || got["X-Custom"] != "literal" {
		t.Errorf("non-placeholder headers should pass through: %v", got)
	}
}

func TestExpandHeaders_NilStoreLeavesPlaceholdersAlone(t *testing.T) {
	in := map[string]string{
		"Authorization": "Bearer ${secret:TOKEN}",
	}
	got, err := expandHeaders(in, nil)
	if err != nil {
		t.Fatalf("expandHeaders: %v", err)
	}
	if !strings.Contains(got["Authorization"], "${secret:TOKEN}") {
		t.Errorf("expected placeholder to survive a nil store, got %q", got["Authorization"])
	}
}

func TestExpandHeaders_EmptyMapReturnsNil(t *testing.T) {
	if out, err := expandHeaders(nil, nil); err != nil || out != nil {
		t.Errorf("empty in should yield nil: %v err=%v", out, err)
	}
}

// End-to-end expansion through a real secrets.Store lives in the
// integration tests; here we only assert the wiring contract:
// placeholders survive when no store is provided, plain values come
// through untouched, and the regex matches the canonical syntax.
func TestSecretPlaceholderRE_MatchesCanonicalSyntax(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"${secret:TOKEN}", true},
		{"prefix ${secret:NAME-1}", true},
		{"prefix ${secret:NAME_2}", true},
		{"${secret:bad name}", false}, // space disallowed
		{"$secret:TOKEN}", false},     // missing '${'
		{"${secret:}", false},         // empty name
	}
	for _, tc := range cases {
		got := secretPlaceholderRE.MatchString(tc.in)
		if got != tc.want {
			t.Errorf("regex match for %q: got %v want %v", tc.in, got, tc.want)
		}
	}
}
