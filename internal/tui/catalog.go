// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/facade"
)

// catalog.go owns the generic "catalogue" overlay: a single navigable,
// mostly read-only list that backs the bare/`list` form of every
// config-entity command (/agent, /provider, /mcp, /secret, /skill).
//
// Before this, those verbs dumped a multi-line system message into the
// chat while sessions/workers/facts got a proper floating modal. The
// catalogue overlay closes that gap without spawning five bespoke
// overlays: each entity just builds a catalogView (title + rows + the
// per-row entry name + footer) and the shared handler/renderer do the
// rest. The optional Enter→edit action reuses the exact editor flow the
// `/<entity> edit NAME` verbs already use.

// catalogKind identifies which entity a catalogView holds. It drives
// the Enter→edit routing and the rebuild-after-reload path.
type catalogKind int

const (
	catalogNone catalogKind = iota
	catalogAgents
	catalogProviders
	catalogMCPs
	catalogSecrets
	catalogSkills
)

// catalogView is the immutable description of one catalogue listing:
// the modal title, the rendered rows, the entry name behind each row
// (parallel to Items; "" means the row has no Enter→edit action), the
// footer hint, and the kind for routing.
type catalogView struct {
	Title  string
	Kind   catalogKind
	Items  []listItem
	Names  []string
	Footer string
}

// editFooter / browseFooter are the two footer variants: entities with
// an editor get an Enter→edit hint, secrets (whose values are never
// shown) get a browse-only footer.
const (
	catalogEditFooter   = keyNav + " select · " + keyEnter + " edit · [esc] close"
	catalogBrowseFooter = keyNav + " select · [esc] close"
)

// buildAgentCatalog renders the static-agent templates. The root and
// utility agents are annotated by AgentDetails via their Description
// prefix, so we surface that as-is.
func buildAgentCatalog(f facade.Facade) catalogView {
	details := f.AgentDetails()
	items := make([]listItem, 0, len(details))
	names := make([]string, 0, len(details))
	for _, d := range details {
		suffix := ""
		if d.Provider != "" || d.Model != "" {
			suffix = d.Provider + "/" + d.Model
		}
		items = append(items, listItem{
			Label:      d.Name,
			EntityKind: "static",
			Suffix:     suffix,
			MetaLines:  metaIfSet(d.Description),
		})
		names = append(names, d.Name)
	}
	return catalogView{
		Title:  "Agents",
		Kind:   catalogAgents,
		Items:  items,
		Names:  names,
		Footer: catalogEditFooter,
	}
}

// buildProviderCatalog renders the LLM providers. The API key is shown
// only as a presence flag, never as the value.
func buildProviderCatalog(f facade.Facade) catalogView {
	details := f.ProviderDetails()
	items := make([]listItem, 0, len(details))
	names := make([]string, 0, len(details))
	for _, d := range details {
		key := "no key"
		if d.HasKey {
			key = "key set"
		}
		url := d.URL
		if url == "" {
			url = "(default endpoint)"
		}
		items = append(items, listItem{
			Label:      d.Name,
			EntityKind: "static",
			Suffix:     d.Type,
			MetaLines:  []string{url + " · " + key},
		})
		names = append(names, d.Name)
	}
	return catalogView{
		Title:  "Providers",
		Kind:   catalogProviders,
		Items:  items,
		Names:  names,
		Footer: catalogEditFooter,
	}
}

// buildMCPCatalog renders the configured MCP servers.
func buildMCPCatalog(f facade.Facade) catalogView {
	details := f.MCPDetails()
	items := make([]listItem, 0, len(details))
	names := make([]string, 0, len(details))
	for _, d := range details {
		endpoint := d.Endpoint
		if endpoint == "" {
			endpoint = "(none)"
		}
		meta := endpoint
		if d.HasAuth {
			meta += " · auth=" + d.AuthKind
		}
		items = append(items, listItem{
			Label:      d.Name,
			EntityKind: "static",
			Suffix:     d.Type,
			MetaLines:  []string{meta},
		})
		names = append(names, d.Name)
	}
	return catalogView{
		Title:  "MCPs",
		Kind:   catalogMCPs,
		Items:  items,
		Names:  names,
		Footer: catalogEditFooter,
	}
}

// buildSecretCatalog renders the secret NAMES only — values never leave
// the encrypted store, so this listing is browse-only (no Enter→edit).
func buildSecretCatalog(f facade.Facade) catalogView {
	names := f.ListSecretNames()
	items := make([]listItem, 0, len(names))
	for _, n := range names {
		items = append(items, listItem{Label: n, EntityKind: "static"})
	}
	return catalogView{
		Title:  "Secrets",
		Kind:   catalogSecrets,
		Items:  items,
		Names:  make([]string, len(names)), // all empty: no Enter action
		Footer: catalogBrowseFooter,
	}
}

// buildSkillCatalog renders the installed skills.
func buildSkillCatalog(f facade.Facade) catalogView {
	details := f.SkillDetails()
	items := make([]listItem, 0, len(details))
	names := make([]string, 0, len(details))
	for _, d := range details {
		items = append(items, listItem{
			Label:      d.Name,
			EntityKind: "skill",
			MetaLines:  metaIfSet(d.Description),
		})
		names = append(names, d.Name)
	}
	return catalogView{
		Title:  "Skills",
		Kind:   catalogSkills,
		Items:  items,
		Names:  names,
		Footer: catalogEditFooter,
	}
}

// metaIfSet returns a one-element meta slice when s is non-empty, or
// nil so the row collapses to a single line. renderList truncates long
// values, so we pass the full description through untouched.
func metaIfSet(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return []string{s}
}

// rebuildCatalog re-derives the open catalogue from its kind after a
// config reload, so an open overlay reflects new/edited/deleted entries
// without the user closing and reopening it. The selection is clamped
// to the new length.
func (m *Model) rebuildCatalog() {
	if m.facade == nil || !m.catalogOpen {
		return
	}
	switch m.catalogView.Kind {
	case catalogAgents:
		m.catalogView = buildAgentCatalog(m.facade)
	case catalogProviders:
		m.catalogView = buildProviderCatalog(m.facade)
	case catalogMCPs:
		m.catalogView = buildMCPCatalog(m.facade)
	case catalogSecrets:
		m.catalogView = buildSecretCatalog(m.facade)
	case catalogSkills:
		m.catalogView = buildSkillCatalog(m.facade)
	default:
		return
	}
	if m.catalogSel >= len(m.catalogView.Items) {
		m.catalogSel = len(m.catalogView.Items) - 1
	}
	if m.catalogSel < 0 {
		m.catalogSel = 0
	}
}

// handleCatalogOverlayKey owns keystrokes while the catalogue overlay
// is open. Navigation mirrors every other list overlay; Enter opens the
// focused entry in the embedded editor via the same flow as the
// `/<entity> edit NAME` verbs (a no-op for secrets, which have no
// editable body).
func (m Model) handleCatalogOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.catalogView.Items
	switch msg.String() {
	case "esc":
		m.catalogOpen = false
		return m, nil
	case "up":
		if m.catalogSel > 0 {
			m.catalogSel--
		}
		return m, nil
	case "down":
		if m.catalogSel < len(items)-1 {
			m.catalogSel++
		}
		return m, nil
	case "pgup":
		m.catalogSel -= overlayPageStep
		if m.catalogSel < 0 {
			m.catalogSel = 0
		}
		return m, nil
	case "pgdown":
		m.catalogSel += overlayPageStep
		if m.catalogSel > len(items)-1 {
			m.catalogSel = len(items) - 1
		}
		if m.catalogSel < 0 {
			m.catalogSel = 0
		}
		return m, nil
	case "home":
		m.catalogSel = 0
		return m, nil
	case "end":
		m.catalogSel = len(items) - 1
		return m, nil
	case "enter", "e", "E":
		return m.editCatalogEntry()
	}
	return m, nil
}

// editCatalogEntry opens the focused catalogue row in the embedded
// editor. Returns unchanged when the row has no editable target (e.g.
// secrets) or the entry can't be loaded; in the latter case it appends
// an error row so the keystroke isn't silently swallowed.
func (m Model) editCatalogEntry() (tea.Model, tea.Cmd) {
	if m.catalogSel < 0 || m.catalogSel >= len(m.catalogView.Names) {
		return m, nil
	}
	name := m.catalogView.Names[m.catalogSel]
	if name == "" || m.facade == nil {
		return m, nil
	}

	var (
		title   string
		initial string
		kind    editorKind
		err     error
	)
	switch m.catalogView.Kind {
	case catalogAgents:
		title, kind = "Edit agent: "+name, editorKindAgentUpsert
		initial, err = m.facade.AgentYAML(name)
	case catalogProviders:
		title, kind = "Edit provider: "+name, editorKindProviderUpsert
		initial, err = m.facade.ProviderYAML(name)
	case catalogMCPs:
		title, kind = "Edit MCP: "+name, editorKindMCPUpsert
		initial, err = m.facade.MCPYAML(name)
	case catalogSkills:
		title, kind = "Edit skill: "+name, editorKindSkillUpsert
		initial, err = m.facade.SkillContent(name)
	default:
		return m, nil
	}
	if err != nil {
		m.messages = append(m.messages, Message{
			Kind: MessageError,
			Time: time.Now(),
			Text: fmt.Sprintf("edit %s: %v", name, err),
		})
		m.chat.SetMessages(m.messages)
		return m, nil
	}

	m.catalogOpen = false
	return m.openEmbeddedEditor(openEditorRequest{
		Title:        title,
		InitialValue: initial,
		Kind:         kind,
	})
}

// renderCatalog paints the catalogue overlay as a centred modal over
// `back`, reusing the shared list chrome so it looks identical to the
// sessions/workers/facts overlays.
func renderCatalog(theme Theme, cv catalogView, selected int, back string, width, height int) string {
	empty := "nothing here yet"
	content := renderList(theme, cv.Items, selected, empty,
		listOverlayMinRows, listOverlayContentWidth)
	return renderModal(theme, overlayOpts{
		Title:    cv.Title,
		Content:  content,
		Footer:   cv.Footer,
		MinWidth: listOverlayMinWidth,
		MaxWidth: listOverlayMaxWidth,
	}, back, width, height)
}
