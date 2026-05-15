// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package filesystem

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name      string
		s         string
		max       int
		hint      string
		wantTrunc bool
		wantHas   string // substring that must be present in output when truncated
	}{
		{
			name:      "no-op: max <= 0 unlimited",
			s:         "hello world",
			max:       0,
			wantTrunc: false,
		},
		{
			name:      "no-op: max negative unlimited",
			s:         "hello world",
			max:       -5,
			wantTrunc: false,
		},
		{
			name:      "no-op: len exactly at max",
			s:         "hello",
			max:       5,
			wantTrunc: false,
		},
		{
			name:      "no-op: len below max",
			s:         "hi",
			max:       100,
			wantTrunc: false,
		},
		{
			name:      "truncation with marker and hint",
			s:         "abcdef",
			max:       3,
			hint:      "; do something",
			wantTrunc: true,
			wantHas:   "[truncated: showing first 3 of 6 chars]",
		},
		{
			name:      "hint is appended after marker",
			s:         "abcdef",
			max:       3,
			hint:      "; my hint",
			wantTrunc: true,
			wantHas:   "; my hint",
		},
		{
			name:      "truncation keeps only first max bytes",
			s:         "abcdefgh",
			max:       4,
			wantTrunc: true,
			wantHas:   "[truncated: showing first 4 of 8 chars]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := truncateString(tc.s, tc.max, tc.hint)
			if truncated != tc.wantTrunc {
				t.Errorf("truncated = %v, want %v", truncated, tc.wantTrunc)
			}
			if !truncated && got != tc.s {
				t.Errorf("no-op case changed string: got %q, want %q", got, tc.s)
			}
			if truncated && tc.wantHas != "" && !strings.Contains(got, tc.wantHas) {
				t.Errorf("output %q missing %q", got, tc.wantHas)
			}
			// Result must always be valid UTF-8.
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
		})
	}
}

func TestTruncateStringRuneBoundary(t *testing.T) {
	// Build a string that contains multi-byte runes (é = 0xC3 0xA9).
	// "café" is 5 bytes (c=1, a=1, f=1, é=2).  If we cap at 4 bytes we must
	// NOT split the 'é' rune — the cut should land at byte 3 ("caf").
	s := "café"
	got, truncated := truncateString(s, 4, "")
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("result is not valid UTF-8: %q", got)
	}
	// The kept prefix must be "caf" (3 bytes), not "caf\xc3" (4 bytes).
	if !strings.HasPrefix(got, "caf") {
		t.Errorf("prefix: got %q, want prefix %q", got, "caf")
	}
	if strings.HasPrefix(got, "café") {
		t.Error("unexpectedly kept the full string")
	}
}

func TestTruncateStringEmptyHint(t *testing.T) {
	s := "hello world"
	got, truncated := truncateString(s, 5, "")
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(got, "[truncated:") {
		t.Errorf("missing truncated marker: %q", got)
	}
}
