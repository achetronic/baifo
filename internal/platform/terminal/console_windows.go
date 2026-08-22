// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package terminal

import (
	"golang.org/x/sys/windows"
)

// cpUTF8 is the Windows code page identifier for UTF-8 (65001). x/sys
// does not export the constant, so we spell it out here — it has been
// stable since Windows NT and is documented in Microsoft's
// "Code Page Identifiers" reference.
const cpUTF8 uint32 = 65001

// prepareUTF8 switches the Windows console code pages to UTF-8 so the
// UTF-8 output baifo (and every Charm library under it) emits is
// decoded correctly. Bubble Tea v2 enables virtual-terminal processing
// on the console but deliberately does NOT touch the code page: a
// console still in CP1252/CP850 mis-decodes every multi-byte sequence
// (box drawing, the "▌" selection rail, emoji), which is exactly the
// mojibake / wrong-width rendering reported in issue #22.
//
// The previous code pages are captured and returned in a restore
// closure the caller must run on shutdown (consoles are shared
// resources; leaving a user's shell in UTF-8 would surprise the next
// program). Errors are tolerated: on a genuinely old console the call
// may fail, and in that case SupportsBoxDrawing reports false so the
// TUI degrades to ASCII anyway.
func prepareUTF8() (restore func(), err error) {
	prevIn, _ := windows.GetConsoleCP()
	prevOut, _ := windows.GetConsoleOutputCP()

	if err := windows.SetConsoleCP(cpUTF8); err != nil {
		return func() {}, err
	}
	if err := windows.SetConsoleOutputCP(cpUTF8); err != nil {
		// Roll back the input page so we don't leave a half-applied
		// state behind.
		_ = windows.SetConsoleCP(prevIn)
		return func() {}, err
	}

	return func() {
		_ = windows.SetConsoleCP(prevIn)
		_ = windows.SetConsoleOutputCP(prevOut)
	}, nil
}
