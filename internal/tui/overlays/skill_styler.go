// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package overlays

import (
	"strings"

	"github.com/achetronic/baifo/internal/tui/components/editor"
	"github.com/achetronic/baifo/internal/tui/components/editor/mdhl"
	"github.com/achetronic/baifo/internal/tui/components/editor/yamlhl"
)

// SkillLineStyler returns an editor.LineStyler tailored for SKILL.md.
// It detects the YAML frontmatter block (the section between two
// lines containing exactly "---") and applies yamlhl there; for
// everything else it falls back to mdhl.
//
// The classifier is closed over a slice of "kinds" — one entry per
// line — built lazily on the first call. We rebuild whenever the
// line count changes; this is cheap enough (hundreds of lines tops)
// and avoids tracking buffer mutations.
//
// We could push this logic into the editor as "multi-styler support",
// but it's a one-off for SKILL.md and any future format (JSONL? RST?)
// would need its own composition anyway, so keeping it here at the
// TUI layer is the right granularity.
func SkillLineStyler(buffer string, yamlTheme yamlhl.Theme, mdTheme mdhl.Theme) editor.LineStyler {
	kinds := classifyLines(buffer)
	yamlStyler := yamlhl.New(yamlTheme)
	mdStyler := mdhl.New(mdTheme)
	return func(lineNum int, content string) []editor.StyledSpan {
		if lineNum < 0 || lineNum >= len(kinds) {
			// Lines beyond what we classified at construction-time
			// are treated as markdown body. Happens when the user
			// appended new lines after the styler was created; the
			// next View call will rebuild the classification.
			return mdStyler(lineNum, content)
		}
		if kinds[lineNum] == lineKindFrontmatter {
			return yamlStyler(lineNum, content)
		}
		return mdStyler(lineNum, content)
	}
}

// lineKind categorises a single line of a SKILL.md document.
type lineKind int

const (
	lineKindBody lineKind = iota
	lineKindFrontmatter
	lineKindFrontmatterDelim
)

// classifyLines splits buffer into lines and tags each with its
// lineKind. The frontmatter block lives at the very top, opens with
// "---" on the first line and closes with the next "---". Anything
// outside that block is body markdown. A buffer without a leading
// "---" is treated as pure body.
func classifyLines(buffer string) []lineKind {
	lines := strings.Split(buffer, "\n")
	out := make([]lineKind, len(lines))
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		// No frontmatter — every line is body.
		return out
	}
	out[0] = lineKindFrontmatterDelim
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			out[i] = lineKindFrontmatterDelim
			// Everything after the closing delimiter is body, which
			// is the zero value — we already left it as lineKindBody.
			return out
		}
		out[i] = lineKindFrontmatter
	}
	// Unclosed frontmatter: treat the rest of the buffer as
	// frontmatter so the user can see they need to add a closing
	// "---". Better than silently giving up on highlighting.
	return out
}
