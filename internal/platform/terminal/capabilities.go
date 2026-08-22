// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"os"
	"runtime"
)

// supportsBoxDrawing decides whether the terminal can render box
// drawing characters and the half-block selection rail with correct
// cell widths. When it reports false, internal/tui swaps the pretty
// set (▌ rail, rounded borders, │ quote bars, ─ rules) for a pure
// ASCII look so the chat's row bookkeeping stays in sync with what the
// terminal actually paints (issue #22).
//
// The decision is environment-based — there is no reliable ioctl to
// ask "is your font broken" — and mirrors the heuristics Charm's own
// colorprofile package applies:
//
//   - POSIX terminals (linux, macOS, *bsd): true. Modern libvte /
//     libtsm stacks render box drawing correctly; this has been true
//     for decades.
//   - Windows Terminal (WT_SESSION), ConEmu (ConEmuANSI=ON), ANSICON:
//     true — all of these provide a VT path with sane fonts.
//   - Anything else on Windows (legacy conhost, unknown wrappers):
//     false. conhost with raster fonts is exactly the environment the
//     screenshot in issue #22 comes from.
//
// The variable form (rather than a func) lets the TUI tests pin the
// capability without env manipulation gymnastics.
var supportsBoxDrawing = detectBoxDrawing(os.Getenv)

// detectBoxDrawing is the pure, testable core of supportsBoxDrawing:
// same contract, but the environment arrives as a lookup function.
func detectBoxDrawing(getenv func(string) string) bool {
	if runtime.GOOS == "windows" {
		if getenv("WT_SESSION") != "" {
			return true
		}
		if getenv("ConEmuANSI") == "ON" {
			return true
		}
		if getenv("ANSICON") != "" {
			return true
		}
		// MSYS2 / Git Bash / Cygwin ptys set TERM like a POSIX
		// terminal; they render Unicode correctly.
		if term := getenv("TERM"); term != "" && term != "dumb" {
			return true
		}
		return false
	}
	return true
}

// SupportsBoxDrawing reports whether the pretty glyph set is safe on
// this terminal. It is a function (not a variable) so tests in other
// packages can stub via the variable above.
func SupportsBoxDrawing() bool { return supportsBoxDrawing }

// PrepareUTF8 switches the console to UTF-8 where the OS requires it
// (Windows) and is a no-op elsewhere. It returns a restore function
// the caller must run on shutdown to hand the console back to the next
// program in the state it was found.
func PrepareUTF8() (restore func(), err error) { return prepareUTF8() }
