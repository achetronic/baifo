// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package terminal holds the OS-level console adaptations baifo needs
// before Bubble Tea takes over the screen. It exists so cmd/baifo and
// internal/tui share ONE source of truth for "what does this terminal
// actually render correctly", instead of sprinkling GOOS checks across
// the codebase.
//
// The package has three concerns:
//
//  1. UTF-8 on Windows (console_windows.go). Bubble Tea v2 enables
//     virtual-terminal processing but never switches the console code
//     page, so a console left in CP1252/CP850 mis-decodes every
//     multi-byte UTF-8 sequence baifo emits (box drawing, the selection
//     rail, emoji). PrepareUTF8 fixes the code page up front and
//     restores it on the way out.
//
//  2. Capability detection (capabilities.go). When the terminal is a
//     legacy Windows console (no VT processing, raster fonts), box
//     drawing and the half-block rail come out as tofu or with the
//     wrong cell width, which desyncs the chat's row bookkeeping
//     (issue #22). SupportsBoxDrawing reports whether the pretty
//     glyphs are safe; internal/tui degrades to ASCII when they are
//     not. The detection is environment-variable based so it is fully
//     testable.
//
//  3. Sanitising LLM text (sanitize.go). Models emit emoji with
//     variation selectors and ZWJ sequences whose rendered width
//     legitimately disagrees between x/ansi's wcwidth tables and the
//     terminal's own font fallback (worst offender: Windows Terminal
//     with emoji fallback fonts). StripVariationSelectors removes the
//     invisible codepoints that cause the disagreement while keeping
//     the visible emoji intact.
package terminal
