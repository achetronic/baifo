// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package yamlhl provides a single-line YAML syntax highlighter for
// the embedded editor.
//
// Why "line-at-a-time": the editor calls LineStyler once per visible
// row on every render. A full-document YAML parser would be both
// overkill (we only need visual cues, not semantic correctness) and
// painful to keep in sync with a mid-edit, often-invalid buffer. A
// per-line tokenizer is good enough: in YAML, the relevant tokens
// (comments, keys, scalars, lists, anchors) are recognisable from
// the line alone except for multiline strings, which we accept as a
// known limitation \u2014 they render as plain text, which is acceptable.
//
// The package returns a Style function that the editor plugs into
// Options.LineStyler. The styling is conservative: we colour the
// most useful tokens and leave everything else untouched, so a
// future redesign of the colour palette only needs to tweak the
// constants below.
package yamlhl

import (
	"regexp"

	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// Theme groups the four lipgloss styles applied by Style. Exposed so
// callers can inject their own palette without forking the package.
type Theme struct {
	Comment lipgloss.Style
	Key     lipgloss.Style
	String  lipgloss.Style
	Number  lipgloss.Style
	Bool    lipgloss.Style
	Anchor  lipgloss.Style
	Punct   lipgloss.Style
}

// DefaultTheme returns neutral xterm-256 defaults. Callers that want
// a host-specific palette should build a Theme and inject it via New().
func DefaultTheme() Theme {
	return Theme{
		Comment: lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true),
		Key:     lipgloss.NewStyle().Foreground(lipgloss.Color("147")).Bold(true), // light violet
		String:  lipgloss.NewStyle().Foreground(lipgloss.Color("114")),            // soft green
		Number:  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),            // amber
		Bool:    lipgloss.NewStyle().Foreground(lipgloss.Color("173")),            // peach
		Anchor:  lipgloss.NewStyle().Foreground(lipgloss.Color("110")),            // cyan
		Punct:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")),            // grey
	}
}

// Pre-compiled regexes. Order in tokenize() matters: the first match
// wins on overlap, so the patterns sit roughly from most specific to
// most general.
//
// We anchor with ^ where the token can only appear at the start of a
// (whitespace-stripped) line, e.g. comments and bare list dashes.
var (
	// "# comment to end-of-line"
	reComment = regexp.MustCompile(`#.*$`)

	// "key:" or "key: value", capturing the key and the colon.
	// Greedy match before ':' to handle quoted keys and dotted keys.
	reKey = regexp.MustCompile(`^(\s*-?\s*)([^"\s][^:#]*?|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\\\)*')(:)(\s|$)`)

	// "&anchor" or "*alias" \u2014 YAML anchors.
	reAnchor = regexp.MustCompile(`[&*][A-Za-z0-9_-]+`)

	// Quoted strings, single or double.
	reDoubleStr = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	reSingleStr = regexp.MustCompile(`'(?:[^'\\]|\\\\)*'`)

	// Numbers: integer or decimal, optionally signed, not preceded
	// by a letter (so we don't paint version suffixes like "v2").
	reNumber = regexp.MustCompile(`(?:^|[\s:\-\[,])(-?\d+(?:\.\d+)?)`)

	// Booleans and null \u2014 YAML 1.1 spellings; YAML 1.2 narrows
	// these, but most users still type lowercase forms.
	reBool = regexp.MustCompile(`\b(?:true|false|null|yes|no|on|off)\b`)
)

// Styler is the signature expected by editor.Options.LineStyler. We
// implement it as a closure that captures the Theme so the editor
// stays decoupled from the highlighting palette.
type Styler = editor.LineStyler

// New returns a Styler bound to theme. lineNum is unused today but
// kept in the signature so future per-line state (e.g. multiline
// folding) can be plumbed without changing the API.
func New(theme Theme) Styler {
	return func(_ int, content string) []editor.StyledSpan {
		return tokenize(content, theme)
	}
}

// hlSpan is one styled byte range inside a line. Named at package
// scope so helpers can use it without redeclaring the anonymous form.
type hlSpan struct {
	from, to int
	style    lipgloss.Style
}

// tokenize walks content and returns a slice of byte-ranged spans
// the editor should paint. Overlap is resolved by precedence:
// comment > key > anchor > string > number > bool. Bytes claimed by
// an earlier pattern are skipped by later ones.
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

	// 1. Comment claims the rest of the line. We must respect quoted
	// strings: '#' inside a quoted scalar is data, not a comment.
	// Cheap heuristic: find the first '#' that is not inside quotes.
	if i := firstUnquotedHash(content); i >= 0 {
		add(i, len(content), t.Comment)
	}

	// 2. Key (key: ...). Only the key portion + the colon.
	if m := reKey.FindStringSubmatchIndex(content); m != nil {
		// m has groups: [0:full] [1:lead] [2:key] [3:colon] [4:space]
		keyFrom, keyTo := m[4], m[5]
		colonFrom, colonTo := m[6], m[7]
		add(keyFrom, keyTo, t.Key)
		add(colonFrom, colonTo, t.Punct)
	}

	// 3. Anchors and aliases.
	for _, m := range reAnchor.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.Anchor)
	}

	// 4. Quoted strings.
	for _, m := range reDoubleStr.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.String)
	}
	for _, m := range reSingleStr.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.String)
	}

	// 5. Numbers \u2014 capturing group 1 is the digits.
	for _, m := range reNumber.FindAllStringSubmatchIndex(content, -1) {
		add(m[2], m[3], t.Number)
	}

	// 6. Booleans.
	for _, m := range reBool.FindAllStringIndex(content, -1) {
		add(m[0], m[1], t.Bool)
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

// firstUnquotedHash returns the byte index of the first '#' that is
// not inside a single- or double-quoted string, or -1 if none. We
// walk the string once tracking quote state; backslash escapes only
// matter inside double quotes (YAML single-quoted strings escape
// quotes by doubling them, not backslashes).
func firstUnquotedHash(s string) int {
	inDouble := false
	inSingle := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escape:
			escape = false
		case inDouble:
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inDouble = false
			}
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		default:
			if c == '"' {
				inDouble = true
			} else if c == '\'' {
				inSingle = true
			} else if c == '#' {
				// '#' starts a comment only when it stands at line
				// start or is preceded by whitespace. Inline "value#x"
				// is a literal '#' in YAML.
				if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
					return i
				}
			}
		}
	}
	return -1
}

// sortSpansByStart is a tiny insertion sort. The slice is typically
// small (<10 spans per line), so stdlib sort would be overkill.
func sortSpansByStart(spans []hlSpan) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j-1].from > spans[j].from; j-- {
			spans[j-1], spans[j] = spans[j], spans[j-1]
		}
	}
}
