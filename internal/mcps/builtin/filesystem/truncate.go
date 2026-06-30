// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package filesystem

import (
	"fmt"
	"unicode/utf8"
)

// truncateString returns s unchanged when max <= 0 or len(s) <= max.
// Otherwise it keeps the first max bytes (cutting back to a clean UTF-8
// rune boundary so the result is always valid text) and appends a marker:
//
//	"\n[truncated: showing first N of M chars]"
//
// followed by the caller-supplied hint (e.g. instructions on how to read
// the rest). Returns the truncated string and true when truncation
// occurred, or the original string and false when it did not.
func truncateString(s string, max int, hint string) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	// Walk back from max until we land on the start byte of a rune so
	// we never produce an incomplete multi-byte sequence.
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	marker := fmt.Sprintf("\n[truncated: showing first %d of %d chars]%s", cut, len(s), hint)
	return s[:cut] + marker, true
}
