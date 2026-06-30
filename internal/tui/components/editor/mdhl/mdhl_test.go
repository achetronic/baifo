// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package mdhl

import (
	"testing"

	"github.com/achetronic/baifo/internal/tui/components/editor"
)

func hasSpanCovering(spans []editor.StyledSpan, from, to int) bool {
	for _, s := range spans {
		if s.From <= from && s.To >= to {
			return true
		}
	}
	return false
}

func TestTokenize_HeaderClaimsLine(t *testing.T) {
	st := New(DefaultTheme())
	line := "# Hello world"
	spans := st(0, line)
	if !hasSpanCovering(spans, 0, len(line)) {
		t.Errorf("expected header span over the whole line, got %v", spans)
	}
}

func TestTokenize_CodeFenceClaimsLine(t *testing.T) {
	st := New(DefaultTheme())
	line := "```go"
	spans := st(0, line)
	if !hasSpanCovering(spans, 0, len(line)) {
		t.Errorf("expected fence span over the whole line, got %v", spans)
	}
}

func TestTokenize_BlockquoteClaimsLine(t *testing.T) {
	st := New(DefaultTheme())
	line := "> quoted text"
	spans := st(0, line)
	if !hasSpanCovering(spans, 0, len(line)) {
		t.Errorf("expected blockquote span over the whole line, got %v", spans)
	}
}

func TestTokenize_InlineCodeSpan(t *testing.T) {
	st := New(DefaultTheme())
	line := "use `go build` to compile"
	spans := st(0, line)
	if !hasSpanCovering(spans, 4, 14) {
		t.Errorf("expected inline-code span over `go build`, got %v", spans)
	}
}

func TestTokenize_BoldAndItalic(t *testing.T) {
	st := New(DefaultTheme())
	line := "this is **bold** and *italic*"
	spans := st(0, line)
	if !hasSpanCovering(spans, 8, 16) {
		t.Errorf("expected bold span over **bold**, got %v", spans)
	}
	if !hasSpanCovering(spans, 21, 29) {
		t.Errorf("expected italic span over *italic*, got %v", spans)
	}
}

func TestTokenize_Link(t *testing.T) {
	st := New(DefaultTheme())
	line := "see [docs](https://example.com) for details"
	spans := st(0, line)
	// "[docs]" runs from 4 to 10. Our impl styles "[docs](" together,
	// then "(url)" separately. Verify at least the link region.
	if !hasSpanCovering(spans, 4, 10) {
		t.Errorf("expected link text styling for [docs], got %v", spans)
	}
}

func TestTokenize_ListMarker(t *testing.T) {
	st := New(DefaultTheme())
	line := "  - first item"
	spans := st(0, line)
	// The bullet character is at index 2.
	if !hasSpanCovering(spans, 2, 3) {
		t.Errorf("expected list-marker span at index 2, got %v", spans)
	}
}

func TestTokenize_NumberedListMarker(t *testing.T) {
	st := New(DefaultTheme())
	line := "1. first item"
	spans := st(0, line)
	// "1." is at indices 0..2.
	if !hasSpanCovering(spans, 0, 2) {
		t.Errorf("expected list-marker span over '1.', got %v", spans)
	}
}

func TestTokenize_EmptyLineYieldsNoSpans(t *testing.T) {
	st := New(DefaultTheme())
	if spans := st(0, ""); len(spans) != 0 {
		t.Errorf("empty line should yield no spans, got %v", spans)
	}
}

func TestTokenize_HeaderWinsOverInlineCode(t *testing.T) {
	// A header line that happens to contain backticks should be one
	// big header span; the precedence rules forbid inline code from
	// stealing part of a header.
	st := New(DefaultTheme())
	line := "# title with `code`"
	spans := st(0, line)
	if !hasSpanCovering(spans, 0, len(line)) {
		t.Errorf("expected header span to cover whole line, got %v", spans)
	}
}
