// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package mdhl provides a per-line Markdown highlighter for the
// embedded editor. Same shape as yamlhl: New() returns an
// editor.LineStyler that emits []StyledSpan per line, and the editor
// composes those with selection/cursor styles.
//
// We highlight the bits that matter for SKILL.md bodies: headers,
// code fences, inline code, bold/italic, links, list bullets and
// blockquotes. We deliberately do NOT track multi-line state between
// calls (e.g. detecting that a line is inside a code fence by looking
// at previous lines) — the editor calls the styler per line and is
// not set up to thread state. If that becomes a visible nuisance we
// can teach the editor to keep a parser context; for now per-line
// detection of code fences via the leading ``` is enough.
package mdhl

import (
	"regexp"

	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// Theme groups the lipgloss styles applied by Style. Exposed so the
// caller can re-skin without forking the package.
type Theme struct {
	Header     lipgloss.Style
	CodeFence  lipgloss.Style
	InlineCode lipgloss.Style
	Bold       lipgloss.Style
	Italic     lipgloss.Style
	Link       lipgloss.Style
	LinkURL    lipgloss.Style
	ListMarker lipgloss.Style
	Blockquote lipgloss.Style
}

// DefaultTheme returns neutral xterm-256 defaults. Callers that want
// a host-specific palette should build a Theme and inject it via New().
func DefaultTheme() Theme {
	return Theme{
		Header:     lipgloss.NewStyle().Foreground(lipgloss.Color("147")).Bold(true),
		CodeFence:  lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		InlineCode: lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Background(lipgloss.Color("237")),
		Bold:       lipgloss.NewStyle().Bold(true),
		Italic:     lipgloss.NewStyle().Italic(true),
		Link:       lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Underline(true),
		LinkURL:    lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		ListMarker: lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		Blockquote: lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true),
	}
}

// Pre-compiled regexes. Order in tokenize() matters because earlier
// matches claim bytes that later matches must skip.
var (
	// "# Header" through "###### Header" — at most 6 leading hashes.
	reHeader = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+.*$`)

	// "```" or "```lang" at the start of the line: code fence delim.
	reFence = regexp.MustCompile("^\\s{0,3}```.*$")

	// Inline code: "`text`". Greedy enough to handle a single span
	// per line correctly without dragging through unrelated runs.
	reInlineCode = regexp.MustCompile("`[^`]+`")

	// **bold**
	reBold = regexp.MustCompile(`\*\*[^*]+\*\*`)

	// *italic* or _italic_ — we keep the patterns simple. False
	// positives for math (a*b) are acceptable in a SKILL.md body.
	reItalic = regexp.MustCompile(`(?:\*[^*\s][^*]*\*|_[^_\s][^_]*_)`)

	// [text](url) — the canonical Markdown link form.
	reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

	// List marker at line start: "- ", "* ", "+ " or "1. " style.
	reListMarker = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+`)

	// Blockquote: line starts with ">".
	reBlockquote = regexp.MustCompile(`^\s{0,3}>.*$`)
)

// New returns a Styler bound to theme. The lineNum argument is unused
// today but kept in the signature so future state-aware highlighting
// (multi-line code fences, for example) can be threaded without
// breaking the contract.
func New(theme Theme) editor.LineStyler {
	return func(_ int, content string) []editor.StyledSpan {
		return tokenize(content, theme)
	}
}

// hlSpan is a private span we accumulate before flattening to the
// editor's StyledSpan type. Same shape; lets us sort and merge
// without touching the public type.
type hlSpan struct {
	from, to int
	style    lipgloss.Style
}

// tokenize walks content and emits a sorted list of styled spans.
// Precedence (highest wins on overlap): code fence > header >
// blockquote > inline code > link > bold > italic > list marker.
//
// Once a byte index is "claimed" by an earlier pattern, later
// patterns skip over it. This is the same precedence-via-claim
// strategy yamlhl uses; keeping the trick consistent across both
// packages means a future refactor can extract it to a helper.
func tokenize(content string, t Theme) []editor.StyledSpan {
	if content == "" {
		return nil
	}

	var spans []hlSpan
	claimed := make([]bool, len(content))

	add := func(from, to int, style lipgloss.Style) {
		if from < 0 || to > len(content) || from >= to {
			return
		}
		for i := from; i < to; i++ {
			if claimed[i] {
				return
			}
		}
		for i := from; i < to; i++ {
			claimed[i] = true
		}
		spans = append(spans, hlSpan{from, to, style})
	}

	// 1. Code fence claims the entire line.
	if m := reFence.FindStringIndex(content); m != nil {
		add(m[0], m[1], t.CodeFence)
	}

	// 2. Header claims the entire line.
	if m := reHeader.FindStringIndex(content); m != nil {
		add(m[0], m[1], t.Header)
	}

	// 3. Blockquote claims the entire line.
	if m := reBlockquote.FindStringIndex(content); m != nil {
		add(m[0], m[1], t.Blockquote)
	}

	// 4. Inline code spans.
	for _, m := range reInlineCode.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.InlineCode)
	}

	// 5. Links. Group 1 is the visible text, group 2 the URL; we
	// style the brackets+text in one colour and the URL in another
	// so the user can still see exactly what they're linking to.
	for _, m := range reLink.FindAllStringSubmatchIndex(content, -1) {
		// m: [full_start, full_end, text_start, text_end, url_start, url_end]
		add(m[0], m[3]+1, t.Link)      // [text]
		add(m[4]-1, m[5]+1, t.LinkURL) // (url)
	}

	// 6. Bold (greedy double asterisks).
	for _, m := range reBold.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.Bold)
	}

	// 7. Italic.
	for _, m := range reItalic.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.Italic)
	}

	// 8. List marker at the start of the line.
	if m := reListMarker.FindStringSubmatchIndex(content); m != nil {
		// m[2..3] is the leading whitespace; we don't style that.
		// m[4..5] is the bullet/number itself.
		add(m[4], m[5], t.ListMarker)
	}

	if len(spans) == 0 {
		return nil
	}
	sortSpansByStart(spans)

	out := make([]editor.StyledSpan, len(spans))
	for i, s := range spans {
		out[i] = editor.StyledSpan{From: s.from, To: s.to, Style: s.style}
	}
	return out
}

// sortSpansByStart sorts spans in place by their starting byte index.
// Insertion sort because the slice is tiny in practice.
func sortSpansByStart(spans []hlSpan) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j-1].from > spans[j].from; j-- {
			spans[j-1], spans[j] = spans[j], spans[j-1]
		}
	}
}
