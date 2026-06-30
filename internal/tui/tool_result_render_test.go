// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
)

// TestStringifyExpandedPrettyPrintsMaps checks that a nested map is
// rendered as indented JSON (multi-line) instead of the compact,
// single-line stringifyAny form.
func TestStringifyExpandedPrettyPrintsMaps(t *testing.T) {
	v := map[string]any{
		"path":        "/tmp/foo.go",
		"total_lines": float64(42),
		"fragments":   []any{map[string]any{"start": float64(1)}},
	}
	out := stringifyExpanded(v, 80)
	if !strings.Contains(out, "\n") {
		t.Fatalf("expected multi-line JSON, got single line: %q", out)
	}
	if !strings.Contains(out, "\"path\": \"/tmp/foo.go\"") {
		t.Fatalf("expected indented JSON with path field, got:\n%s", out)
	}
	if strings.Contains(out, "{path:") {
		t.Fatalf("output fell back to compact stringifyAny form:\n%s", out)
	}
}

// TestStringifyExpandedScalarsVerbatim checks that scalars stay
// untouched so paths, URLs and prose read naturally.
func TestStringifyExpandedScalarsVerbatim(t *testing.T) {
	cases := map[any]string{
		"https://example.com/a/b": "https://example.com/a/b",
		true:                      "true",
		false:                     "false",
		nil:                       "null",
	}
	for in, want := range cases {
		if got := stringifyExpanded(in, 80); got != want {
			t.Errorf("stringifyExpanded(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestPrettyJSONString re-indents a string that is itself JSON and
// leaves plain strings alone.
func TestPrettyJSONString(t *testing.T) {
	if _, ok := prettyJSONString("/just/a/path"); ok {
		t.Errorf("plain path should not be treated as JSON")
	}
	if _, ok := prettyJSONString("hello world"); ok {
		t.Errorf("prose should not be treated as JSON")
	}
	out, ok := prettyJSONString(`{"a":1,"b":[2,3]}`)
	if !ok {
		t.Fatalf("valid JSON object not recognised")
	}
	if !strings.Contains(out, "\n") || !strings.Contains(out, "\"a\": 1") {
		t.Fatalf("expected re-indented JSON, got:\n%s", out)
	}
}

// TestUnwrapResultEnvelope peels the single-key ADK functiontool
// wrapper only when the inner value is a non-empty map.
func TestUnwrapResultEnvelope(t *testing.T) {
	inner := map[string]any{"path": "/x", "lines": float64(3)}

	got := unwrapResultEnvelope(map[string]any{"result": inner})
	if len(got) != 2 || got["path"] != "/x" {
		t.Errorf("expected inner map promoted, got %v", got)
	}

	// Scalar payload: keep the wrapper so the key still labels it.
	scalar := map[string]any{"result": "done"}
	if got := unwrapResultEnvelope(scalar); len(got) != 1 || got["result"] != "done" {
		t.Errorf("scalar payload should be left wrapped, got %v", got)
	}

	// Multi-key result: never unwrap.
	multi := map[string]any{"a": 1, "b": 2}
	if got := unwrapResultEnvelope(multi); len(got) != 2 {
		t.Errorf("multi-key result should be untouched, got %v", got)
	}

	// Unknown single key: never unwrap.
	other := map[string]any{"payload": inner}
	if got := unwrapResultEnvelope(other); len(got) != 1 {
		t.Errorf("unknown wrapper key should be untouched, got %v", got)
	}
}

// TestSplitValueLinesWordBoundary verifies that overflow wraps on a
// space rather than mid-word, and that very long tokens still hard-cut.
func TestSplitValueLinesWordBoundary(t *testing.T) {
	lines := splitValueLines("the quick brown fox jumps", 10)
	for _, l := range lines {
		if len(l) > 10 {
			t.Fatalf("line exceeds wrap width: %q", l)
		}
	}
	// No line should end mid-word with a trailing partial token when a
	// space break was available.
	joined := strings.Join(lines, "|")
	if strings.Contains(joined, "qui|ck") || strings.Contains(joined, "bro|wn") {
		t.Fatalf("wrapped mid-word despite available spaces: %v", lines)
	}

	// A single token longer than the window must hard-cut.
	long := splitValueLines(strings.Repeat("x", 25), 10)
	if len(long) < 3 {
		t.Fatalf("expected long token to hard-cut into >=3 lines, got %v", long)
	}
}

// TestSplitValueLinesPreservesIndent checks that continuation rows of a
// wrapped JSON value keep the source line's leading indentation.
func TestSplitValueLinesPreservesIndent(t *testing.T) {
	line := "    key value-that-is-really-quite-long-and-must-wrap here"
	lines := splitValueLines(line, 20)
	if len(lines) < 2 {
		t.Fatalf("expected the long indented line to wrap, got %v", lines)
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("continuation line %d lost indentation: %q", i+1, l)
		}
	}
}
