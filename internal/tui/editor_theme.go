// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/tui/components/editor"
	"github.com/achetronic/baifo/internal/tui/components/editor/mdhl"
	"github.com/achetronic/baifo/internal/tui/components/editor/yamlhl"
)

// canariasEditorStyles maps the Canarias palette onto editor.Styles.
// The host modal chrome (renderOverlay in overlay_chrome.go) uses
// PanelBorderFocused (Accent.Primary) for the border, Accent.Subtle for
// the title band background, and Accent.Primary for the title text;
// the modal colors below mirror that exactly for visual consistency.
func canariasEditorStyles() *editor.Styles {
	st := editor.DefaultStyles()

	st.Header = lipgloss.NewStyle().
		Background(colorBGHover).
		Foreground(colorText).
		Padding(0, 1)

	st.Gutter = lipgloss.NewStyle().
		Foreground(colorTextFaint).
		Padding(0, 1, 0, 0)

	st.ErrorLine = lipgloss.NewStyle().
		Foreground(colorError).
		Padding(0, 1)

	st.Selection = lipgloss.NewStyle().
		Background(colorBGFocus).
		Foreground(colorText)

	st.Cursor = lipgloss.NewStyle().Reverse(true)

	st.SearchMatch = lipgloss.NewStyle().
		Background(colorInfo).
		Foreground(colorBG)

	st.SearchCurrentMatch = lipgloss.NewStyle().
		Background(canariasAccent.Primary).
		Foreground(colorBG).
		Bold(true)

	// Mirror the host modal: PanelBorderFocused uses Accent.Primary;
	// title band uses Accent.Subtle bg with Accent.Primary fg.
	st.ModalBorder = canariasAccent.Primary
	st.ModalTitleBg = canariasAccent.Subtle
	st.ModalTitleFg = canariasAccent.Primary
	st.ModalText = colorText
	st.ModalDim = colorTextDim

	st.CompleterBg = colorBGAlt
	st.CompleterSelBg = colorBGFocus
	st.CompleterFg = colorText
	st.CompleterDim = colorTextDim
	st.CompleterAccent = canariasAccent.Primary
	st.CompleterBorder = colorBorder

	return &st
}

// canariasYAMLTheme returns a yamlhl.Theme tuned for the Canarias palette.
func canariasYAMLTheme() yamlhl.Theme {
	return yamlhl.Theme{
		Comment: lipgloss.NewStyle().Foreground(colorTextFaint).Italic(true),
		Key:     lipgloss.NewStyle().Foreground(canariasAccent.Primary).Bold(true),
		String:  lipgloss.NewStyle().Foreground(colorSuccess),
		Number:  lipgloss.NewStyle().Foreground(colorInfo),
		Bool:    lipgloss.NewStyle().Foreground(colorProvider),
		Anchor:  lipgloss.NewStyle().Foreground(colorMCP),
		Punct:   lipgloss.NewStyle().Foreground(colorTextDim),
	}
}

// canariasMDTheme returns an mdhl.Theme tuned for the Canarias palette.
func canariasMDTheme() mdhl.Theme {
	return mdhl.Theme{
		Header:     lipgloss.NewStyle().Foreground(canariasAccent.Primary).Bold(true),
		CodeFence:  lipgloss.NewStyle().Foreground(colorTextDim),
		InlineCode: lipgloss.NewStyle().Foreground(colorSuccess).Background(colorBGAlt),
		Bold:       lipgloss.NewStyle().Bold(true),
		Italic:     lipgloss.NewStyle().Italic(true),
		Link:       lipgloss.NewStyle().Foreground(colorMCP).Underline(true),
		LinkURL:    lipgloss.NewStyle().Foreground(colorTextFaint),
		ListMarker: lipgloss.NewStyle().Foreground(colorInfo),
		Blockquote: lipgloss.NewStyle().Foreground(colorTextDim).Italic(true),
	}
}
