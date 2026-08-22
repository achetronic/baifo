// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/platform/terminal"
)

// Shared chrome for the editor pop-ups, gated by the same terminal
// capability the chat uses (issue #22): the pretty set on capable
// terminals, pure ASCII on legacy Windows consoles.
var (
	// selectionRail is the marker painted on the focused row of the
	// completer; mirrors the chat's selection marker.
	selectionRail = "▌ "
	// frameBorders is the border set of every editor modal/popup.
	frameBorders = lipgloss.RoundedBorder()

	// asciiFrameBorders is the pure-ASCII fallback for legacy
	// terminals (lipgloss.NormalBorder is still Unicode box-drawing).
	asciiFrameBorders = lipgloss.Border{
		Top:         "-",
		Bottom:      "-",
		Left:        "|",
		Right:       "|",
		TopLeft:     "+",
		TopRight:    "+",
		BottomLeft:  "+",
		BottomRight: "+",
	}
)

func init() {
	if !terminal.SupportsBoxDrawing() {
		selectionRail = "| "
		frameBorders = asciiFrameBorders
	}
}
