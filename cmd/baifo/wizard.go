// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/secrets"
)

// firstRunResult is what runFirstRunWizard hands back to main. It carries
// the chosen config directory plus what to do next: launch the TUI, or
// (for an OAuth provider) stop and tell the user to log in first.
type firstRunResult struct {
	// dir is the config directory that was created.
	dir string

	// launchTUI is true when baifo should proceed into the interactive
	// TUI. It is false only on the OAuth path, where the provider has no
	// usable credentials until the user runs `baifo provider auth`.
	launchTUI bool

	// oauthName is the provider name to log into; non-empty only when
	// launchTUI is false.
	oauthName string
}

// runFirstRunWizard walks the user through creating a fresh .baifo
// directory and (optionally) configuring their first LLM provider, so a
// brand-new install lands in a working conversation instead of a degraded
// boot. Returns the created directory and what to do next.
func runFirstRunWizard(flagDir string) (firstRunResult, error) {
	dir := flagDir
	if dir == "" {
		if envDir := os.Getenv("BAIFO_HOME"); envDir != "" {
			dir = envDir
		} else {
			var err error
			dir, err = config.DefaultDir()
			if err != nil {
				return firstRunResult{}, err
			}
		}
	}

	p := tea.NewProgram(newWizardModel(dir))
	m, err := p.Run()
	if err != nil {
		return firstRunResult{}, fmt.Errorf("wizard error: %w", err)
	}

	wm, ok := m.(wizardModel)
	if !ok {
		return firstRunResult{}, fmt.Errorf("unexpected model type")
	}

	if wm.aborted {
		return firstRunResult{}, fmt.Errorf("aborted by user")
	}

	if !wm.configured {
		// User skipped provider setup: write the static templates as
		// before. baifo will boot degraded until they finish in the TUI.
		if err := scaffoldConfigDir(dir); err != nil {
			return firstRunResult{}, err
		}
		return firstRunResult{dir: dir, launchTUI: true}, nil
	}

	c := wm.choice()
	if err := scaffoldConfigDirWithProvider(dir, c); err != nil {
		return firstRunResult{}, err
	}
	if c.oauth {
		return firstRunResult{dir: dir, launchTUI: false, oauthName: c.name}, nil
	}
	return firstRunResult{dir: dir, launchTUI: true}, nil
}

// wizardStep is the wizard's position in its linear flow.
type wizardStep int

const (
	stepConfirm       wizardStep = iota // create the .baifo dir? Yes/No
	stepAskProvider                     // configure a provider now? Yes/Skip
	stepType                            // anthropic | openai | gemini
	stepAnthropicAuth                   // oauth | api key
	stepOpenAIMode                      // official | compatible
	stepURL                             // text input: endpoint URL
	stepAPIKey                          // text input: API key (masked)
	stepModel                           // text input: model id
)

type wizardModel struct {
	dir    string
	width  int
	height int

	aborted bool

	step wizardStep
	sel  int // selected option index on a choice step

	// Accumulated choices.
	provType   string // anthropic | openai | gemini
	useOAuth   bool
	compatible bool // OpenAI-compatible endpoint
	url        string
	apiKey     string
	model      string

	// input is the live buffer for the current text-input step.
	input string

	// configured is true once the user completed provider setup (i.e. did
	// not skip). Drives whether runFirstRunWizard writes a provider.
	configured bool
}

func newWizardModel(dir string) wizardModel {
	return wizardModel{dir: dir, step: stepConfirm, sel: 0}
}

func (m wizardModel) Init() tea.Cmd { return nil }

// isInputStep reports whether the current step captures typed text rather
// than a fixed set of choices.
func (m wizardModel) isInputStep() bool {
	return m.step == stepURL || m.step == stepAPIKey || m.step == stepModel
}

// numOptions returns how many selectable options the current choice step
// has (0 for input steps).
func (m wizardModel) numOptions() int {
	switch m.step {
	case stepConfirm, stepAskProvider, stepAnthropicAuth, stepOpenAIMode:
		return 2
	case stepType:
		return 3
	default:
		return 0
	}
}

func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.isInputStep() {
			return m.updateInput(msg)
		}
		return m.updateChoice(msg)
	case tea.PasteMsg:
		// Bracketed paste (the terminal's own paste gesture) arrives as
		// one message, not as individual key presses. Only the text-input
		// steps care: pasting an API key or endpoint URL must work, or
		// the user is stuck typing a 100-char secret by hand. Newlines
		// are stripped so a trailing newline in the clipboard doesn't
		// submit or corrupt a single-line field.
		if m.isInputStep() {
			m.input += sanitisePastedLine(msg.String())
		}
		return m, nil
	case tea.ClipboardMsg:
		// Reply to the ReadClipboard() issued by Ctrl+V on an input step.
		if m.isInputStep() {
			m.input += sanitisePastedLine(msg.String())
		}
		return m, nil
	}
	return m, nil
}

// sanitisePastedLine flattens pasted text into something safe for a
// single-line field: newlines and tabs collapse away and other control
// characters are dropped, so an API key copied with a trailing newline
// (or out of a wrapped terminal) lands clean.
func sanitisePastedLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		// Drop ASCII control characters (newlines, tabs, etc.) and DEL;
		// keep everything printable so the field stays single-line.
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// updateChoice handles navigation and selection on a choice step.
func (m wizardModel) updateChoice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := m.numOptions()
	switch msg.String() {
	case "ctrl+c", "esc":
		m.aborted = true
		return m, tea.Quit
	case "left", "h", "up", "k":
		m.sel = (m.sel - 1 + n) % n
		return m, nil
	case "right", "l", "down", "j", "tab":
		m.sel = (m.sel + 1) % n
		return m, nil
	case "enter", " ":
		return m.commitChoice()
	}
	// Confirm step keeps the historical y/n shortcuts.
	if m.step == stepConfirm {
		switch msg.String() {
		case "y", "Y":
			m.sel = 0
			return m.commitChoice()
		case "n", "N", "q":
			m.aborted = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// commitChoice advances the wizard based on the selected option.
func (m wizardModel) commitChoice() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepConfirm:
		if m.sel == 1 { // No
			m.aborted = true
			return m, tea.Quit
		}
		m.step = stepAskProvider
		m.sel = 0

	case stepAskProvider:
		if m.sel == 1 { // Skip
			m.configured = false
			return m, tea.Quit
		}
		m.step = stepType
		m.sel = 0

	case stepType:
		switch m.sel {
		case 0:
			m.provType = "anthropic"
			m.step = stepAnthropicAuth
		case 1:
			m.provType = "openai"
			m.step = stepOpenAIMode
		case 2:
			m.provType = "gemini"
			m.step = stepAPIKey
		}
		m.sel = 0

	case stepAnthropicAuth:
		if m.sel == 0 { // OAuth
			m.useOAuth = true
			m.step = stepModel
		} else { // API key
			m.useOAuth = false
			m.step = stepAPIKey
		}
		m.input = ""

	case stepOpenAIMode:
		if m.sel == 1 { // Compatible
			m.compatible = true
			m.step = stepURL
		} else { // Official
			m.compatible = false
			m.step = stepAPIKey
		}
		m.input = ""
	}
	return m, nil
}

// updateInput handles typing on a text-input step.
func (m wizardModel) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.aborted = true
		return m, tea.Quit
	case "backspace":
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	case "enter":
		return m.commitInput()
	case "ctrl+v":
		// Explicitly support Ctrl+V: ask the terminal for its clipboard.
		// The reply arrives as tea.ClipboardMsg, handled in Update. This
		// complements bracketed paste (tea.PasteMsg) so the advertised
		// [ctrl+v] shortcut genuinely works, not just the terminal's own
		// paste gesture.
		return m, func() tea.Msg { return tea.ReadClipboard() }
	}
	// Printable rune: in bubbletea v2 the typed text lives on Key().Text
	// and is empty for non-printables (Enter, Esc, arrows, ...), so this
	// never inserts a control key's byte form.
	if text := msg.Key().Text; text != "" {
		m.input += text
	}
	return m, nil
}

// commitInput validates and stores the current input, then advances. An
// empty value keeps the wizard on the same step (no silent acceptance).
func (m wizardModel) commitInput() (tea.Model, tea.Cmd) {
	val := strings.TrimSpace(m.input)
	if val == "" {
		return m, nil
	}
	switch m.step {
	case stepURL:
		m.url = val
		m.step = stepAPIKey
		m.input = ""
	case stepAPIKey:
		m.apiKey = val
		m.step = stepModel
		m.input = ""
	case stepModel:
		m.model = val
		m.configured = true
		return m, tea.Quit
	}
	return m, nil
}

// choice assembles the providerChoice from the accumulated answers.
func (m wizardModel) choice() providerChoice {
	c := providerChoice{typ: m.provType, model: m.model}
	switch m.provType {
	case "anthropic":
		c.name = "anthropic"
		if m.useOAuth {
			c.oauth = true
		} else {
			c.apiKey = m.apiKey
			c.secretName = "ANTHROPIC_API_KEY"
		}
	case "openai":
		if m.compatible {
			c.name = "openai-compatible"
			c.url = m.url
			c.secretName = "OPENAI_COMPATIBLE_API_KEY"
		} else {
			c.name = "openai"
			c.secretName = "OPENAI_API_KEY"
		}
		c.apiKey = m.apiKey
	case "gemini":
		c.name = "gemini"
		c.apiKey = m.apiKey
		c.secretName = "GEMINI_API_KEY"
	}
	return c
}

// --- View ---

var (
	wizardAccent = lipgloss.Color("#F2922B")
	wizardText   = lipgloss.Color("#E8DDCB")
	wizardClay   = lipgloss.Color("#5E3119")
	wizardFaint  = lipgloss.Color("#8A7560") // muted taupe for secondary chrome
)

// docsURL points users at the full configuration guide. Shown on every
// wizard screen so a lost user always has somewhere to go.
const docsURL = "https://github.com/achetronic/baifo/blob/master/docs/CONFIGURATION.md"

func (m wizardModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("")
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(wizardAccent).MarginBottom(1)
	textStyle := lipgloss.NewStyle().Foreground(wizardText).MarginBottom(2)
	hintStyle := lipgloss.NewStyle().Foreground(wizardClay).MarginTop(1).Italic(true)

	logoBlock := lipgloss.NewStyle().
		Bold(true).
		Foreground(wizardAccent).
		MarginBottom(2).
		Render("█▄  ▄▀█ █ █▀▀ █▀█\n█▄█ █▀█ █ █▀  █▄█")

	title, desc, body, hint := m.viewParts()

	parts := []string{logoBlock}
	if title != "" {
		parts = append(parts, titleStyle.Render(title))
	}
	if desc != "" {
		parts = append(parts, textStyle.Render(desc))
	}
	parts = append(parts, body)
	if hint != "" {
		parts = append(parts, hintStyle.Render(hint))
	}
	// Persistent docs pointer: faint + italic so it reads as the quietest
	// possible footnote (a terminal can't shrink the glyphs, so "smaller"
	// means dimmer and shorter).
	docsStyle := lipgloss.NewStyle().Foreground(wizardFaint).Faint(true).Italic(true).MarginTop(1)
	parts = append(parts, docsStyle.Render(docsURL))

	content := lipgloss.JoinVertical(lipgloss.Center, parts...)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(wizardClay).
		Padding(1, 4).
		Render(content)

	v := tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box))
	v.AltScreen = true
	return v
}

// viewParts returns the title, description, body and hint for the active
// step. The body is either a row of choice buttons or a text-input field.
func (m wizardModel) viewParts() (title, desc, body, hint string) {
	switch m.step {
	case stepConfirm:
		return "Initialise baifo",
			fmt.Sprintf("No .baifo directory was found.\nCreate a new one at %s?", m.dir),
			m.renderChoices([]string{"Yes", "No"}),
			"[left/right] choose · [enter] confirm · [esc] cancel"

	case stepAskProvider:
		return "Connect a model",
			"baifo needs one LLM provider before it can talk.\nSet one up now, or skip and do it later in the TUI.",
			m.renderChoices([]string{"Configure now", "Skip for now"}),
			"[left/right] choose · [enter] confirm · [esc] cancel"

	case stepType:
		return "Choose a provider",
			"Which LLM provider do you want to connect?",
			m.renderChoices([]string{"Anthropic", "OpenAI", "Gemini"}),
			"[left/right] choose · [enter] confirm · [esc] cancel"

	case stepAnthropicAuth:
		return "Anthropic authentication",
			"Use your Claude subscription (OAuth) or an API key?",
			m.renderChoices([]string{"OAuth (Claude sub)", "API key"}),
			"[left/right] choose · [enter] confirm · [esc] cancel"

	case stepOpenAIMode:
		return "OpenAI endpoint",
			"Official OpenAI, or an OpenAI-compatible endpoint\n(Ollama, OpenRouter, vLLM, ...)?",
			m.renderChoices([]string{"OpenAI official", "Compatible endpoint"}),
			"[left/right] choose · [enter] confirm · [esc] cancel"

	case stepURL:
		return "Endpoint URL",
			"Paste the base URL of your OpenAI-compatible endpoint.",
			m.renderInput(false),
			"e.g. http://localhost:11434/v1 · [ctrl+v] paste · [enter] confirm · [esc] cancel"

	case stepAPIKey:
		return "API key",
			"Paste your API key. It is stored in secrets.yaml and\nreferenced as ${secret:...}; baifo never shows it to the model.",
			m.renderInput(true),
			"[ctrl+v] paste · [enter] confirm · [esc] cancel"

	case stepModel:
		return "Model", "Which model should the main agent use?",
			m.renderInput(false),
			m.modelHint()
	}
	return "", "", "", ""
}

// modelHint suggests an example model id for the chosen provider.
func (m wizardModel) modelHint() string {
	switch m.provType {
	case "anthropic":
		return "e.g. claude-sonnet-4-5-20250929 · [ctrl+v] paste · [enter] confirm · [esc] cancel"
	case "openai":
		return "e.g. gpt-4o · [ctrl+v] paste · [enter] confirm · [esc] cancel"
	case "gemini":
		return "e.g. gemini-2.5-flash · [ctrl+v] paste · [enter] confirm · [esc] cancel"
	}
	return "[ctrl+v] paste · [enter] confirm · [esc] cancel"
}

// renderChoices lays out the option buttons horizontally, highlighting the
// selected one in the accent colour.
func (m wizardModel) renderChoices(labels []string) string {
	btn := lipgloss.NewStyle().
		Padding(0, 3).
		Margin(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(wizardClay)
	active := btn.
		BorderForeground(wizardAccent).
		Foreground(wizardAccent).
		Bold(true)

	rendered := make([]string, len(labels))
	for i, l := range labels {
		if i == m.sel {
			rendered[i] = active.Render(l)
		} else {
			rendered[i] = btn.Render(l)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, rendered...)
}

// renderInput draws the current text buffer with a trailing cursor. When
// masked, the value is shown as bullets so an API key never appears.
func (m wizardModel) renderInput(masked bool) string {
	shown := m.input
	if masked {
		shown = strings.Repeat("•", len([]rune(m.input)))
	}
	field := lipgloss.NewStyle().
		Foreground(wizardText).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(wizardClay).
		Padding(0, 2).
		Width(44)
	return field.Render(shown + "▏")
}

// --- Scaffolding ---

// scaffoldConfigDir creates dir and the file layout baifo needs to run,
// using the static templates untouched (the user skipped provider setup).
// Existing files are left in place.
func scaffoldConfigDir(dir string) error {
	return writeScaffold(dir, defaultBaifoYAML, defaultAgentsYAML, defaultSecretsYAML)
}

// scaffoldConfigDirWithProvider is like scaffoldConfigDir but injects the
// provider the wizard configured. The provider entry and the root agent's
// model land in baifo.yaml / agents.yaml via template replacement; the API
// key (when any) is written through the secrets Store so it gets the exact
// on-disk encoding the loader expects (a sealed fileEntry, never a bare
// string). secrets.yaml itself is written from the untouched template.
func scaffoldConfigDirWithProvider(dir string, c providerChoice) error {
	if err := writeScaffold(dir,
		applyProviderToBaifoYAML(defaultBaifoYAML, c),
		applyProviderToAgentsYAML(defaultAgentsYAML, c),
		defaultSecretsYAML,
	); err != nil {
		return err
	}
	if c.oauth || c.apiKey == "" || c.secretName == "" {
		return nil
	}
	// A fresh install always has an empty encryption_key (plaintext mode),
	// so the store opens with an empty passphrase. Set() seals and persists
	// the value in the canonical format.
	store, err := secrets.NewStore(dir, "")
	if err != nil {
		return fmt.Errorf("open secrets store: %w", err)
	}
	if err := store.Set(c.secretName, c.apiKey, "Added by the first-run wizard"); err != nil {
		return fmt.Errorf("store API key: %w", err)
	}
	return nil
}

// writeScaffold creates dir/data and writes the three config files with
// the given contents. Files that already exist are left untouched.
func writeScaffold(dir, baifoYAML, agentsYAML, secretsYAML string) error {
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	files := []struct {
		name    string
		content string
	}{
		{config.FileName, baifoYAML},
		{config.AgentsFileName, agentsYAML},
		{"secrets.yaml", secretsYAML},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", f.name, err)
			}
			if err := os.WriteFile(path, []byte(f.content), 0o600); err != nil {
				return fmt.Errorf("write %s: %w", f.name, err)
			}
		}
	}
	return nil
}
