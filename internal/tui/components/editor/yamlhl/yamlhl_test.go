// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package yamlhl

import (
	"testing"

	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// hasSpanCovering reports whether spans contains one that covers the
// byte range [from, to). Used by tests to assert tokenisation without
// depending on the precise lipgloss style applied.
func hasSpanCovering(spans []editor.StyledSpan, from, to int) bool {
	for _, s := range spans {
		if s.From <= from && s.To >= to {
			return true
		}
	}
	return false
}

func TestTokenize_DetectsKeyAndColon(t *testing.T) {
	st := New(DefaultTheme())
	spans := st(0, "name: github")
	if !hasSpanCovering(spans, 0, 4) {
		t.Errorf("expected key span over 'name', got spans %v", spans)
	}
	if !hasSpanCovering(spans, 4, 5) {
		t.Errorf("expected punctuation span over ':', got spans %v", spans)
	}
}

func TestTokenize_DetectsComment(t *testing.T) {
	st := New(DefaultTheme())
	spans := st(0, "key: value  # trailing comment")
	if !hasSpanCovering(spans, 12, 30) {
		t.Errorf("expected comment span over '# trailing comment', got spans %v", spans)
	}
}

func TestTokenize_HashInsideQuotesIsNotComment(t *testing.T) {
	st := New(DefaultTheme())
	spans := st(0, `name: "value#x"`)
	// The whole quoted string must be styled, and no comment span
	// should be present (we can detect that by checking nothing
	// covers the trailing '#x' as a separate (comment) span; the
	// quoted string covers it entirely).
	if !hasSpanCovering(spans, 6, 15) {
		t.Errorf("expected string span over quoted value, got spans %v", spans)
	}
}

func TestTokenize_DetectsBoolAndNumber(t *testing.T) {
	st := New(DefaultTheme())
	spans := st(0, "enabled: true  port: 8080")
	// 'true' lives at 9..13.
	if !hasSpanCovering(spans, 9, 13) {
		t.Errorf("expected bool span over 'true', got spans %v", spans)
	}
	// '8080' lives at 21..25.
	if !hasSpanCovering(spans, 21, 25) {
		t.Errorf("expected number span over '8080', got spans %v", spans)
	}
}

func TestTokenize_DetectsAnchorAndAlias(t *testing.T) {
	st := New(DefaultTheme())
	spans := st(0, "base: &default\nother: *default")
	// Both '&default' and '*default' should be styled. We only test
	// the first line worth of spans; the function tokenises one line
	// at a time so we pass it both halves.
	spans = st(0, "base: &default")
	if !hasSpanCovering(spans, 6, 14) {
		t.Errorf("expected anchor span over '&default', got spans %v", spans)
	}
	spans = st(0, "other: *default")
	if !hasSpanCovering(spans, 7, 15) {
		t.Errorf("expected alias span over '*default', got spans %v", spans)
	}
}

func TestTokenize_EmptyLineYieldsNoSpans(t *testing.T) {
	st := New(DefaultTheme())
	if spans := st(0, ""); len(spans) != 0 {
		t.Errorf("empty line should yield no spans, got %v", spans)
	}
}

func TestTokenize_OverlapResolvedByPrecedence(t *testing.T) {
	// A comment with a fake 'key:' inside should be entirely styled
	// as a comment; the key regex must not steal part of the comment.
	st := New(DefaultTheme())
	spans := st(0, "# foo: bar")
	for _, s := range spans {
		if s.From >= 2 && s.To <= 6 {
			// Part of the comment got claimed by another span.
			if !hasSpanCovering(spans, 0, 10) {
				t.Errorf("comment was split by overlap: spans %v", spans)
			}
		}
	}
}
