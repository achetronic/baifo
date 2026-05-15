// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/config"
)

// runFirstRunWizard starts a Bubble Tea application that asks the user
// whether to initialise a fresh .baifo directory. If confirmed, it creates
// it under $HOME with a minimal baifo.yaml. Returns the absolute path
// of the new directory.
func runFirstRunWizard(flagDir string) (string, error) {
	dir := flagDir
	if dir == "" {
		if envDir := os.Getenv("BAIFO_HOME"); envDir != "" {
			dir = envDir
		} else {
			var err error
			dir, err = config.DefaultDir()
			if err != nil {
				return "", err
			}
		}
	}

	p := tea.NewProgram(newWizardModel(dir))
	m, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("wizard error: %w", err)
	}

	wm, ok := m.(wizardModel)
	if !ok {
		return "", fmt.Errorf("unexpected model type")
	}

	if wm.aborted {
		return "", fmt.Errorf("aborted by user")
	}

	if err := scaffoldConfigDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

type wizardModel struct {
	dir      string
	aborted  bool
	selected int // 0 = Yes, 1 = No
	width    int
	height   int
}

func newWizardModel(dir string) wizardModel {
	return wizardModel{
		dir:      dir,
		selected: 0,
	}
}

func (m wizardModel) Init() tea.Cmd {
	return nil
}

func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.aborted = true
			return m, tea.Quit
		case "left", "h", "tab":
			if m.selected > 0 {
				m.selected--
			} else {
				m.selected = 1
			}
		case "right", "l", "shift+tab":
			if m.selected < 1 {
				m.selected++
			} else {
				m.selected = 0
			}
		case "enter", " ":
			if m.selected == 0 {
				m.aborted = false
			} else {
				m.aborted = true
			}
			return m, tea.Quit
		case "y", "Y":
			m.aborted = false
			return m, tea.Quit
		case "n", "N":
			m.aborted = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m wizardModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("")
	}

	// Styles
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F2922B")).MarginBottom(1)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E8DDCB")).MarginBottom(2)

	btnStyle := lipgloss.NewStyle().
		Padding(0, 3).
		Margin(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#5E3119"))

	activeBtnStyle := btnStyle.
		BorderForeground(lipgloss.Color("#F2922B")).
		Foreground(lipgloss.Color("#F2922B")).
		Bold(true)

	// Baifo Logo
	logo0 := "█▄  ▄▀█ █ █▀▀ █▀█"
	logo1 := "█▄█ █▀█ █ █▀  █▄█"
	logoBlock := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#F2922B")).
		MarginBottom(2).
		Render(logo0 + "\n" + logo1)

	// Buttons
	yesBtn := btnStyle.Render("Yes")
	noBtn := btnStyle.Render("No")
	if m.selected == 0 {
		yesBtn = activeBtnStyle.Render("Yes")
	} else {
		noBtn = activeBtnStyle.Render("No")
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Center, yesBtn, noBtn)

	// Content
	title := titleStyle.Render("Initialise baifo")
	desc := textStyle.Render(fmt.Sprintf("No .baifo directory was found.\nCreate a new one at %s?", m.dir))

	content := lipgloss.JoinVertical(lipgloss.Center, logoBlock, title, desc, buttons)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#5E3119")).
		Padding(1, 4).
		Render(content)

	// Center everything
	v := tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box))
	v.AltScreen = true
	return v
}

// scaffoldConfigDir creates dir and the file layout baifo needs to run:
// a fully-populated baifo.yaml (every section the loader understands,
// with sensible defaults pre-written so the file is self-documenting;
// providers / mcps / secrets start as empty arrays the user fills in),
// an agents.yaml seeded with a single root agent (complete except for
// the model, which the user chooses via /agent edit), and a data/
// subdir for the SQLite database. Existing files are left untouched.
func scaffoldConfigDir(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	configPath := filepath.Join(dir, config.FileName)
	if _, err := os.Stat(configPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", config.FileName, err)
		}
		if err := os.WriteFile(configPath, []byte(defaultBaifoYAML), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", config.FileName, err)
		}
	}

	agentsPath := filepath.Join(dir, config.AgentsFileName)
	if _, err := os.Stat(agentsPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", config.AgentsFileName, err)
		}
		if err := os.WriteFile(agentsPath, []byte(defaultAgentsYAML), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", config.AgentsFileName, err)
		}
	}

	secretsPath := filepath.Join(dir, "secrets.yaml")
	if _, err := os.Stat(secretsPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", "secrets.yaml", err)
		}
		if err := os.WriteFile(secretsPath, []byte(defaultSecretsYAML), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", "secrets.yaml", err)
		}
	}
	return nil
}
