// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package overlays

import (
	"testing"

	"github.com/achetronic/baifo/internal/tui/components/editor/mdhl"
	"github.com/achetronic/baifo/internal/tui/components/editor/yamlhl"
)

func TestClassifyLines_NoFrontmatter(t *testing.T) {
	kinds := classifyLines("# Hello\nWorld")
	for i, k := range kinds {
		if k != lineKindBody {
			t.Errorf("line %d: got %d, want body", i, k)
		}
	}
}

func TestClassifyLines_WithFrontmatter(t *testing.T) {
	buf := "---\nname: my-skill\ndescription: Something\n---\n\n# Heading\nbody"
	kinds := classifyLines(buf)
	want := []lineKind{
		lineKindFrontmatterDelim, // ---
		lineKindFrontmatter,      // name:
		lineKindFrontmatter,      // description:
		lineKindFrontmatterDelim, // ---
		lineKindBody,             // (blank)
		lineKindBody,             // # Heading
		lineKindBody,             // body
	}
	if len(kinds) != len(want) {
		t.Fatalf("kinds len: got %d, want %d", len(kinds), len(want))
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("line %d: got %d, want %d", i, kinds[i], want[i])
		}
	}
}

func TestClassifyLines_UnclosedFrontmatterStaysFrontmatter(t *testing.T) {
	buf := "---\nname: oops\nbody but never closed"
	kinds := classifyLines(buf)
	if kinds[0] != lineKindFrontmatterDelim {
		t.Errorf("line 0 should be delim, got %d", kinds[0])
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i] != lineKindFrontmatter {
			t.Errorf("line %d should be frontmatter, got %d", i, kinds[i])
		}
	}
}

func TestSkillLineStyler_RoutesToYamlAndMd(t *testing.T) {
	buf := "---\nname: x\ndescription: y\n---\n\n# Heading"
	st := SkillLineStyler(buf, yamlhl.DefaultTheme(), mdhl.DefaultTheme())

	// Line 1 ("name: x") should produce yaml spans (key + colon).
	if spans := st(1, "name: x"); len(spans) == 0 {
		t.Errorf("frontmatter line: expected yaml spans, got none")
	}
	// Line 5 ("# Heading") should produce a markdown header span.
	if spans := st(5, "# Heading"); len(spans) == 0 {
		t.Errorf("markdown line: expected mdhl spans, got none")
	}
}
