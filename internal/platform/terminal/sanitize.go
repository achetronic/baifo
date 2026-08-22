// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package terminal

import "strings"

// StripVariationSelectors removes U+FE0F (variation selector-16) and
// U+FE0E (variation selector-15) from s. These codepoints are
// invisible by definition, but they flip the base character between
// "text" and "emoji" presentation — and each presentation has a
// DIFFERENT cell width in the terminal than x/ansi's wcwidth tables
// assume. The disagreement is worst on Windows Terminal with emoji
// fallback fonts, where a VS16-tagged grapheme can paint wider than
// measured, producing extra wrapped rows the chat's row bookkeeping
// never counted (issue #22).
//
// Stripping keeps the visible character: "✅️" renders as the plain
// glyph "✅" at width the tables actually report. ZWJ sequences are
// left intact — decomposing them changes meaning (family emoji would
// become several people) — and their width is at least consistently
// reported as 2.
func StripVariationSelectors(s string) string {
	// Fast path: the selectors are three-byte UTF-8 sequences; a plain
	// Contains check avoids an allocation on the overwhelmingly common
	// emoji-free input.
	if !strings.ContainsRune(s, '\uFE0F') && !strings.ContainsRune(s, '\uFE0E') {
		return s
	}
	return strings.NewReplacer("\uFE0F", "", "\uFE0E", "").Replace(s)
}
