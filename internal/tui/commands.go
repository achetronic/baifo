// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/embeddings"
	"github.com/achetronic/baifo/internal/facade"
)

// slashResult is what handleSlashCommand returns. The Model uses it to
// decide what feedback to show in the chat history. We avoid having
// the dispatcher mutate the Model directly so command handling stays
// testable.
type slashResult struct {
	// systemMessage, when non-empty, is appended to the chat history
	// as a MessageSystem row so the user sees the outcome of the
	// command.
	systemMessage string

	// injectMessages, when non-empty, are appended verbatim to the
	// chat history. Used by debug/diagnostic commands that want to
	// drop fully-formed rows (e.g. special notice / agent-error rows)
	// into the transcript without going through the LLM.
	injectMessages []Message

	// errorMessage, when non-empty, replaces systemMessage and is
	// rendered as MessageError.
	errorMessage string

	// resetChat, when true, drops the in-memory message history (used
	// by /new to start fresh visually).
	resetChat bool

	// openSessionsOverlay opens the sessions list as an overlay
	// (the same one that used to live in the "Sessions" tab). The
	// model refreshes the sessions list lazily on open. Triggered
	// by `/session` with no sub-verb.
	openSessionsOverlay bool

	// openWorkersOverlay opens the workers list as an overlay
	// (the former "Workers" tab). The model refreshes the
	// workers list lazily on open. Triggered by `/worker` with
	// no sub-verb.
	openWorkersOverlay bool

	// openFactsOverlay opens the long-term memory entries as a
	// navigable overlay. Triggered by `/fact` or `/fact list`.
	// The model refreshes the facts list lazily on open.
	openFactsOverlay bool

	// openCatalog, when non-nil, opens the generic catalogue overlay
	// with the given entity list. Triggered by the bare/`list` form of
	// /agent, /provider, /mcp, /secret and /skill.
	openCatalog *catalogView

	// toggleHelp tells the Model to flip the help overlay flag.
	toggleHelp bool

	// quit tells the Model to schedule tea.Quit.
	quit bool

	// openEditor, when non-nil, tells the Model to open the embedded
	// editor overlay. The Model wires SaveMsg to the file write and
	// CancelMsg to close. See openEditorRequest for the payload.
	openEditor *openEditorRequest

	// openSecretPrompt, when non-nil, tells the Model to open the
	// masked-input modal for setting a secret. The value never
	// touches a regular editor buffer.
	openSecretPrompt *secretPromptRequest

	// openFactEditor, when true, tells the Model to open the
	// embedded YAML editor with the fact scaffold. No payload
	// needed: facts have no seed identity. Mirrors the editor-
	// based add UX of every other Settings section.
	openFactEditor bool

	// asyncCmd, when non-nil, is a tea.Cmd the Model should run
	// alongside the rest of the slash result. We use it to keep
	// the TUI responsive while a slow operation (OAuth flow, file
	// download, ...) is in flight: the systemMessage already shows
	// "starting …", and the asyncCmd produces a follow-up tea.Msg
	// when the operation completes so the Model can surface the
	// outcome.
	//
	// Today only /mcp auth uses this — the OAuth flow can
	// take up to a minute while the user clicks "allow" in their
	// browser. Without the async hop the entire BubbleTea event
	// loop would freeze, blocking input and rendering.
	asyncCmd tea.Cmd

	// switchInterlocutorTo, when non-empty, asks the Model to swap
	// the chat's active interlocutor. Use rootInterlocutor ("root")
	// to go back to the root agent from a worker chat, or a
	// worker_id to jump into that worker's stream. The Model runs
	// the swap after appending systemMessage / errorMessage so the
	// user sees the confirmation in the chat they're leaving.
	switchInterlocutorTo string
}

// openEditorRequest carries everything the embedded editor needs to
// be opened from a slash command. Used by /settings config edit and
// /mcp add/edit and similar commands across every entity that has a
// YAML form (MCPs, skills, agents, providers, facts).
type openEditorRequest struct {
	// Title appears in the editor's header bar.
	Title string

	// InitialValue is the buffer text on open.
	InitialValue string

	// SavePath, when non-empty, is the absolute path the editor's
	// buffer should be persisted to on save. The Model handles the
	// actual write so callers don't have to thread filesystem
	// access through their slash handler. Ignored when Kind is set.
	SavePath string

	// Kind selects how the buffer is consumed on save:
	//   editorKindRawFile        → write SavePath verbatim (default).
	//   editorKindMCPUpsert      → parse + validate as a config.MCPEntry
	//                              and call Facade.UpsertMCPFromDisk.
	//   editorKindSkillUpsert    → parse + validate as a SKILL.md doc
	//                              and call Facade.UpsertSkill.
	//   editorKindAgentUpsert    → parse + validate as an AgentTemplate.
	//   editorKindProviderUpsert → parse + validate as a ProviderEntry.
	//   editorKindFactUpsert     → parse a {content, category} doc
	//                              and call Facade.AddFact.
	//   editorKindFactUpdate     → parse the same doc and call
	//                              Facade.UpdateFact on FactTargetID.
	//   editorKindSessionRename  → take the buffer as the new session
	//                              title and call Facade.RenameSession
	//                              on SessionTargetID.
	Kind editorKind

	// FactTargetID is the fact ID to update when Kind is
	// editorKindFactUpdate. Ignored otherwise.
	FactTargetID uint64

	// SessionTargetID is the session ID to rename when Kind is
	// editorKindSessionRename. Ignored otherwise.
	SessionTargetID string
}

// editorKind enumerates the save-time strategies the embedded editor
// supports. New kinds are added per CRUD (providers, agents, ...)
// without touching the rest of the wiring.
type editorKind int

// secretPromptRequest carries the initial state for the secret-set
// modal. The Model uses it to seed the name field (which can be
// empty when the user types '/secret set' without an argument).
type secretPromptRequest struct {
	Name string
}

const (
	editorKindRawFile editorKind = iota
	editorKindMCPUpsert
	editorKindSkillUpsert
	editorKindAgentUpsert
	editorKindProviderUpsert
	editorKindFactUpsert
	editorKindFactUpdate
	editorKindSessionRename
)

// handleSlashCommand parses the given line and executes the command,
// using m.facade as the backend. Returns a slashResult describing the
// outcome.
//
// Supported in Phase 4:
//
//	/new                  start a fresh session
//	/session             list all sessions (printed inline)
//	/session switch ID   activate session ID
//	/session rename ID new title   rename session ID

//	/session delete ID   delete session ID
//
// Unknown commands surface as errorMessage. The dispatcher does NOT
// touch the network or the LLM — every operation is local to the
// facade's session bookkeeping.
func (m *Model) handleSlashCommand(line string) slashResult {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return slashResult{errorMessage: "empty slash command"}
	}
	if m.facade == nil {
		return slashResult{errorMessage: "no facade available (running with --no-agent?)"}
	}

	ctx := context.Background()

	switch parts[0] {
	case "/session":
		return m.handleSessionsCommand(ctx, parts[1:])

	case "/worker":
		return m.handleWorkersCommand(ctx, parts[1:])

	case "/settings":
		return m.handleSettingsCommand(parts[1:])

	case "/help", "/?":
		return slashResult{toggleHelp: true}

	case "/quit":
		return slashResult{quit: true}

	case "/mcp":
		return m.handleMCPCommand(ctx, parts[1:])

	case "/skill":
		return m.handleSkillCommand(ctx, parts[1:])

	case "/agent":
		return m.handleAgentCommand(ctx, parts[1:])

	case "/provider":
		return m.handleProviderCommand(ctx, parts[1:])

	case "/secret":
		return m.handleSecretCommand(ctx, parts[1:])

	case "/fact":
		return m.handleFactCommand(ctx, parts[1:])

	case "/debug":
		return m.handleDebugCommand(parts[1:])

	case "/root":
		// Switch the chat back to the root agent. Useful when the
		// user is talking to a worker and wants to return to the
		// main conversation. No-op (with a friendly message) when
		// already on root.
		if m.activeInterlocutor == rootInterlocutor {
			return slashResult{systemMessage: "already talking to root"}
		}
		return slashResult{
			systemMessage:        "switched to root",
			switchInterlocutorTo: rootInterlocutor,
		}

	default:
		return slashResult{errorMessage: "unknown command: " + parts[0]}
	}
}

// handleSessionsCommand routes the /session sub-verbs.
//
// /session               → open overlay with the list
// /session new           → create a fresh session, switch to it
// /session switch <id>   → activate session id
// /session rename <id> <title>
// /session delete <id>
//
// The naked /session used to print an inline text listing; now it
// opens the overlay (richer presentation, identical to what used
// to live behind the Sessions tab).
func (m *Model) handleSessionsCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		return slashResult{openSessionsOverlay: true}
	}

	switch args[0] {
	case "new":
		id, err := m.facade.NewSession(ctx)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("new session: %v", err)}
		}
		return slashResult{
			systemMessage: "started new session " + id,
			resetChat:     true,
		}

	case "switch":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /session switch <id>"}
		}
		id := args[1]
		if err := m.facade.SwitchSession(ctx, id); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("switch: %v", err)}
		}
		return slashResult{
			systemMessage: "switched to session " + id,
			resetChat:     true,
		}

	case "rename":
		if len(args) < 3 {
			return slashResult{errorMessage: "usage: /session rename <id> <new title>"}
		}
		id := args[1]
		title := strings.Join(args[2:], " ")
		if err := m.facade.RenameSession(ctx, id, title); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("rename: %v", err)}
		}
		return slashResult{systemMessage: fmt.Sprintf("renamed %s to %q", id, title)}

	case "delete":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /session delete <id>"}
		}
		id := args[1]
		wasActive := id == m.facade.SessionID()
		newActive, err := m.facade.DeleteSession(ctx, id)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("delete: %v", err)}
		}
		msg := "deleted session " + id
		if wasActive {
			msg += " (new active: " + newActive + ")"
			return slashResult{systemMessage: msg, resetChat: true}
		}
		return slashResult{systemMessage: msg}

	default:
		return slashResult{errorMessage: "unknown /session verb: " + args[0]}
	}
}

// handleWorkersCommand routes the /worker sub-verbs.
//
// /worker                  → open overlay with the live list
// /worker talk <id|name>   → switch the chat to that worker
// /worker kill <id|name>   → cancel the worker
// /worker collect <id|name>→ harvest its output and remove it
//
// IDs accept either the opaque worker_id (e.g. "w_a3f9") or the
// friendly Spec.Name; resolveWorkerRef normalises both.
func (m *Model) handleWorkersCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		return slashResult{openWorkersOverlay: true}
	}

	switch args[0] {
	case "talk":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /worker talk <id|name>"}
		}
		id := m.resolveWorkerRef(args[1])
		if id == "" {
			return slashResult{errorMessage: "no live worker matches " + args[1]}
		}
		return slashResult{
			systemMessage:        "switched to worker " + id,
			switchInterlocutorTo: id,
		}

	case "kill":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /worker kill <id|name>"}
		}
		id := m.resolveWorkerRef(args[1])
		if id == "" {
			return slashResult{errorMessage: "no live worker matches " + args[1]}
		}
		const reason = "killed by user from TUI"
		if err := m.facade.KillWorker(id, reason); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("kill %s: %v", id, err)}
		}
		return slashResult{systemMessage: fmt.Sprintf("killed worker %s (%s)", id, reason)}

	case "collect":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /worker collect <id|name>"}
		}
		id := m.resolveWorkerRef(args[1])
		if id == "" {
			return slashResult{errorMessage: "no live worker matches " + args[1]}
		}
		// Short timeout: if the worker is still busy the user
		// can retry later; we don't want to block the TUI.
		cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		output, err := m.facade.CollectWorker(cctx, id)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("collect %s: %v", id, err)}
		}
		msg := "collected worker " + id
		if output != "" {
			msg += "\noutput: " + output
		}
		return slashResult{systemMessage: msg}

	default:
		return slashResult{errorMessage: "unknown /worker verb: " + args[0]}
	}
}

// formatSessionList renders a session list as a multi-line string
// suitable for a MessageSystem row.
func formatSessionList(list []facade.SessionInfo, activeID string) string {
	if len(list) == 0 {
		return "no sessions yet"
	}
	var b strings.Builder
	b.WriteString("sessions:\n")
	for _, s := range list {
		marker := " "
		if s.ID == activeID {
			marker = "*"
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, " %s %s · %s · %d msg · %s\n",
			marker, title, s.LastAt, s.MsgCount, s.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// handleMCPCommand handles the /mcp family.
//
//	/mcp                   list configured MCPs.
//	/mcp list              same as above.
//	/mcp add NAME          stub — the form lands in PR B.2.
//	/mcp edit NAME         stub — the form lands in PR B.2.
//	/mcp delete NAME       remove the entry from baifo.yaml (comment-preserving).
//	/mcp auth NAME stub — OAuth flows land in PR B.3+.
//
// Read-only verbs return their output inline as a system message;
// delete writes through to disk via yamledit and triggers a reload
// automatically via the file watcher.
func (m *Model) handleMCPCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		cv := buildMCPCatalog(m.facade)
		return slashResult{openCatalog: &cv}
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /mcp add NAME"}
		}
		name := args[1]
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        "Add MCP: " + name,
				InitialValue: m.facade.MCPScaffold(name),
				Kind:         editorKindMCPUpsert,
			},
			systemMessage: "editing scaffold for new MCP " + name + "…",
		}

	case "edit":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /mcp edit NAME"}
		}
		name := args[1]
		yamlText, err := m.facade.MCPYAML(name)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/mcp edit: %v", err)}
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        "Edit MCP: " + name,
				InitialValue: yamlText,
				Kind:         editorKindMCPUpsert,
			},
			systemMessage: "editing MCP " + name + "…",
		}

	case "delete", "rm":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /mcp delete NAME"}
		}
		name := args[1]
		if err := m.facade.DeleteMCPFromDisk(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/mcp delete: %v", err)}
		}
		return slashResult{systemMessage: "deleted MCP " + name}

	case "auth":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /mcp auth NAME [--force]"}
		}
		name := args[1]
		// Parse trailing flags. We accept --force (and its short
		// alias -f) anywhere after NAME so the user can type
		// `/mcp auth foo --force` or `/mcp auth --force foo`.
		force := false
		for _, a := range args[1:] {
			if a == "--force" || a == "-f" {
				force = true
			}
		}
		msg := "authenticating " + name + " — opening browser if needed…"
		if force {
			msg = "re-authenticating " + name + " (force) — discarding cached token…"
		}
		return slashResult{
			systemMessage: msg,
			asyncCmd:      authenticateMCPCmd(m.facade, name, force),
		}

	case "test":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /mcp test NAME"}
		}
		name := args[1]
		return slashResult{
			systemMessage: "testing connection to " + name + "…",
			asyncCmd:      testMCPConnectionCmd(m.facade, name),
		}

	case "logout":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /mcp logout NAME"}
		}
		name := args[1]
		ctx := context.Background()
		if err := m.facade.ClearMCPAuth(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/mcp logout: %v", err)}
		}
		return slashResult{
			systemMessage: "cleared cached credentials for " + name + " (token + DCR client). Run /mcp auth " + name + " to authenticate again.",
		}

	default:
		return slashResult{errorMessage: "unknown /mcp verb: " + args[0]}
	}
}

// handleSkillCommand handles the /skill family.
//
//	/skill                      list installed skills.
//	/skill list                 same as above.
//	/skill add NAME             open editor with the SKILL.md scaffold.
//	/skill edit NAME            open editor with the current SKILL.md.
//	/skill delete NAME          remove .baifo/skill/NAME/ (with confirmation).
//	/skill install URL          stub — lands in next iteration.
func (m *Model) handleSkillCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		cv := buildSkillCatalog(m.facade)
		return slashResult{openCatalog: &cv}
	}
	switch args[0] {
	case "add":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		title := "Add skill"
		if name != "" {
			title = "Add skill: " + name
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        title,
				InitialValue: m.facade.SkillScaffold(name),
				Kind:         editorKindSkillUpsert,
			},
			systemMessage: "editing scaffold for new skill…",
		}

	case "edit":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /skill edit NAME"}
		}
		name := args[1]
		content, err := m.facade.SkillContent(name)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/skill edit: %v", err)}
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        "Edit skill: " + name,
				InitialValue: content,
				Kind:         editorKindSkillUpsert,
			},
			systemMessage: "editing skill " + name + "…",
		}

	case "delete", "rm":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /skill delete NAME"}
		}
		name := args[1]
		if err := m.facade.DeleteSkill(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/skill delete: %v", err)}
		}
		return slashResult{systemMessage: "deleted skill " + name}

	case "install":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /skill install URL (.zip or .tar.gz)"}
		}
		name, err := m.facade.InstallSkill(ctx, args[1])
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/skill install: %v", err)}
		}
		return slashResult{systemMessage: "installed skill " + name}

	default:
		return slashResult{errorMessage: "unknown /skill verb: " + args[0]}
	}
}

// handleAgentCommand handles the /agent family.
//
//	/agent                  list configured static agents.
//	/agent list             same as above.
//	/agent add [NAME]       open editor with the agent scaffold.
//	/agent edit NAME        open editor with the current agent YAML.
//	/agent delete NAME      remove from agents.yaml (with confirmation).
//	/agent set-root NAME    make NAME the root agent (persists to agents.yaml).
func (m *Model) handleAgentCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		cv := buildAgentCatalog(m.facade)
		return slashResult{openCatalog: &cv}
	}
	switch args[0] {
	case "add":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		title := "Add agent"
		if name != "" {
			title = "Add agent: " + name
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        title,
				InitialValue: m.facade.AgentScaffold(name),
				Kind:         editorKindAgentUpsert,
			},
			systemMessage: "editing scaffold for new agent…",
		}

	case "edit":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /agent edit NAME"}
		}
		name := args[1]
		yamlText, err := m.facade.AgentYAML(name)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/agent edit: %v", err)}
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        "Edit agent: " + name,
				InitialValue: yamlText,
				Kind:         editorKindAgentUpsert,
			},
			systemMessage: "editing agent " + name + "…",
		}

	case "delete", "rm":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /agent delete NAME"}
		}
		name := args[1]
		if err := m.facade.DeleteAgent(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/agent delete: %v", err)}
		}
		return slashResult{systemMessage: "deleted agent " + name}

	case "set-root":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /agent set-root NAME"}
		}
		name := args[1]
		if err := m.facade.SetRootAgent(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/agent set-root: %v", err)}
		}
		return slashResult{systemMessage: name + " is now the root agent"}

	default:
		return slashResult{errorMessage: "unknown /agent verb: " + args[0]}
	}
}

// handleProviderCommand handles the /provider family.
//
//	/provider                  list configured providers.
//	/provider list             same as above.
//	/provider add [NAME]       open editor with the provider scaffold.
//	/provider edit NAME        open editor with the current provider YAML.
//	/provider delete NAME      remove from baifo.yaml (with confirmation).
func (m *Model) handleProviderCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		cv := buildProviderCatalog(m.facade)
		return slashResult{openCatalog: &cv}
	}
	switch args[0] {
	case "add":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		title := "Add provider"
		if name != "" {
			title = "Add provider: " + name
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        title,
				InitialValue: m.facade.ProviderScaffold(name),
				Kind:         editorKindProviderUpsert,
			},
			systemMessage: "editing scaffold for new provider…",
		}

	case "edit":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /provider edit NAME"}
		}
		name := args[1]
		yamlText, err := m.facade.ProviderYAML(name)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/provider edit: %v", err)}
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        "Edit provider: " + name,
				InitialValue: yamlText,
				Kind:         editorKindProviderUpsert,
			},
			systemMessage: "editing provider " + name + "…",
		}

	case "delete", "rm":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /provider delete NAME"}
		}
		name := args[1]
		if err := m.facade.DeleteProvider(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/provider delete: %v", err)}
		}
		return slashResult{systemMessage: "deleted provider " + name}

	default:
		return slashResult{errorMessage: "unknown /provider verb: " + args[0]}
	}
}

// handleSecretCommand handles the /secret family.
//
//	/secret                  list secret names (never values).
//	/secret list             same as above.
//	/secret set [NAME]       open the secret prompt to set a value.
//	/secret delete NAME      remove from secrets.yaml (with confirmation).
//	/secret encode           re-seal every plaintext entry (needs key).
//	/secret decode           unwrap every encrypted entry into plaintext.
//
// /secret set is intentionally different from the YAML-edit pattern:
// the value never goes into a buffer that could leak to disk or to
// the chat history. It's collected by a masked-input modal and
// handed straight to the encrypted store.
func (m *Model) handleSecretCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		cv := buildSecretCatalog(m.facade)
		res := slashResult{openCatalog: &cv}
		if !m.facade.SecretsEncrypted() {
			// Surface the plaintext warning as a chat row alongside the
			// overlay; the overlay itself only lists names.
			res.systemMessage = "⚠ secrets are stored in PLAINTEXT (no encryption_key in baifo.yaml).\n" +
				"   Set encryption_key and run `/secret encode` to encrypt them at rest."
		}
		return res
	}
	switch args[0] {
	case "set":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		return slashResult{openSecretPrompt: &secretPromptRequest{Name: name}}

	case "delete", "rm":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /secret delete NAME"}
		}
		name := args[1]
		if err := m.facade.DeleteSecret(ctx, name); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/secret delete: %v", err)}
		}
		return slashResult{systemMessage: "deleted secret " + name}

	case "encode":
		n, err := m.facade.EncodeSecrets(ctx)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/secret encode: %v", err)}
		}
		if n == 0 {
			return slashResult{systemMessage: "nothing to encode (all entries already encrypted)"}
		}
		return slashResult{systemMessage: fmt.Sprintf("encoded %d secret(s) into the encrypted format", n)}

	case "decode":
		n, err := m.facade.DecodeSecrets(ctx)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/secret decode: %v", err)}
		}
		if n == 0 {
			return slashResult{systemMessage: "nothing to decode (all entries already plaintext)"}
		}
		return slashResult{systemMessage: fmt.Sprintf("decoded %d secret(s) into plaintext on disk", n)}

	default:
		return slashResult{errorMessage: "unknown /secret verb: " + args[0]}
	}
}

// handleFactCommand handles the /fact family. Facts are the agent's
// long-term memory. The user can list, add, edit and delete entries.
// Edit exists so the user can correct small mistakes the IA captured
// (typos, outdated values) without losing the entry's identity.
//
//	/fact                       open the facts overlay (navigable list).
//	/fact list                  same as above.
//	/fact add "content" [cat]   insert a new manual fact.
//	/fact add                   open the editor to type a new fact.
//	/fact edit ID               open the editor seeded with the entry.
//	/fact delete ID             remove the entry with that numeric ID.
func (m *Model) handleFactCommand(ctx context.Context, args []string) slashResult {
	if len(args) == 0 || args[0] == "list" {
		return slashResult{openFactsOverlay: true}
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			// No content provided: open the editor so the user can
			// type it interactively. Keeps the slash UX consistent
			// with the Settings overlay 'n' shortcut.
			return slashResult{openFactEditor: true}
		}
		content := strings.Join(args[1:], " ")
		id, err := m.facade.AddFact(ctx, content, "")
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/fact add: %v", err)}
		}
		return slashResult{systemMessage: fmt.Sprintf("stored fact #%d", id)}

	case "delete", "rm":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /fact delete ID"}
		}
		id, perr := strconv.ParseUint(args[1], 10, 64)
		if perr != nil {
			return slashResult{errorMessage: fmt.Sprintf("/fact delete: invalid id %q", args[1])}
		}
		if err := m.facade.DeleteFact(ctx, id); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/fact delete: %v", err)}
		}
		return slashResult{systemMessage: fmt.Sprintf("deleted fact #%d", id)}

	case "edit":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /fact edit ID"}
		}
		id, perr := strconv.ParseUint(args[1], 10, 64)
		if perr != nil {
			return slashResult{errorMessage: fmt.Sprintf("/fact edit: invalid id %q", args[1])}
		}
		current, _, err := m.facade.FactContent(id)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/fact edit: %v", err)}
		}
		return slashResult{
			openEditor: &openEditorRequest{
				Title:        fmt.Sprintf("Edit fact #%d", id),
				InitialValue: factEditScaffold(current),
				Kind:         editorKindFactUpdate,
				FactTargetID: id,
			},
		}

	default:
		return slashResult{errorMessage: "unknown /fact verb: " + args[0]}
	}
}

// handleDebugCommand drives the hidden /debug command. It exists so a
// developer can eyeball the "special" chat rows (context-guard notice,
// agent-error) live without having to actually trigger a compaction or
// an LLM failure. Everything it does is local and visual; it never
// touches the session, the LLM or disk.
//
//	/debug special   inject both a context-guard notice and an agent error
//	/debug guard     inject only a context-guard notice (expandable summary)
//	/debug error     inject only an agent-error row
//	/debug embedding <text> compute and print the embedding vector for the given text
func (m *Model) handleDebugCommand(args []string) slashResult {
	verb := "special"
	if len(args) > 0 {
		verb = args[0]
	}

	notice := Message{
		Kind: MessageNotice,
		Text: "User asked baifo to fix a silent-hang bug after exec. baifo " +
			"traced it to eventFromA2A dropping failed task events, patched " +
			"the executor translation, added regression tests, and rebuilt the " +
			"binary. Then reworked the special-row styling into colour header " +
			"bands and made the context-guard summary expandable.",
	}
	agentErr := Message{
		Kind: MessageAgentError,
		Text: `llm error response: "overloaded_error: the model is temporarily overloaded, please retry shortly"`,
	}

	switch verb {
	case "special", "both":
		return slashResult{
			systemMessage:  "debug: injected context-guard notice + agent error (expand the notice with Enter)",
			injectMessages: []Message{notice, agentErr},
		}
	case "guard", "notice":
		return slashResult{
			systemMessage:  "debug: injected context-guard notice (expand it with Enter)",
			injectMessages: []Message{notice},
		}
	case "error", "err":
		return slashResult{
			systemMessage:  "debug: injected agent-error row",
			injectMessages: []Message{agentErr},
		}
	case "embedding", "embed":
		if len(args) < 2 {
			return slashResult{errorMessage: "usage: /debug embedding <text>"}
		}
		text := strings.Join(args[1:], " ")
		eng, err := embeddings.New()
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("failed to load embeddings engine: %v", err)}
		}
		vec, err := eng.Embed(text)
		if err != nil {
			return slashResult{errorMessage: fmt.Sprintf("failed to embed text: %v", err)}
		}

		var sum float32
		for _, v := range vec {
			sum += v * v
		}
		norm := math.Sqrt(float64(sum))

		display := fmt.Sprintf("[%f, %f, %f, %f, %f, ..., %f]", vec[0], vec[1], vec[2], vec[3], vec[4], vec[len(vec)-1])
		return slashResult{
			systemMessage: fmt.Sprintf("Embedding for %q (dim=%d, norm=%.4f):\n%s", text, len(vec), norm, display),
		}
	default:
		return slashResult{errorMessage: "usage: /debug [special|guard|error|embedding]"}
	}
}

// mcpAuthDoneMsg is the tea.Msg the authenticateMCPCmd produces
// when an OAuth flow finishes. We carry the MCP name and any error
// so the Model can append a precise system/error chat row.
//
// We define it next to the command that emits it to keep the
// async flow local: there is no other producer of this message,
// no other consumer than handleAgentChunk's neighbour branches in
// Update.
type mcpAuthDoneMsg struct {
	name string
	err  error
}

// mcpTestDoneMsg carries the result of the async TestMCPConnection
// call. Mirrors mcpAuthDoneMsg's shape; `status` is the one-line
// string the facade builds (✓/✗ + details) which the Model
// surfaces verbatim as a system row.
type mcpTestDoneMsg struct {
	name   string
	status string
	err    error
}

// authenticateMCPCmd returns a tea.Cmd that runs the OAuth flow
// for the given MCP name OFF the BubbleTea goroutine. This is
// load-bearing for usability: the interactive OAuth flow opens a
// browser tab and waits for the redirect, which can take
// 30 seconds or more. Without the async hop the entire TUI would
// freeze — input, rendering, ticks, all blocked.
//
// The command produces an mcpAuthDoneMsg that the Model handles
// by appending a follow-up system message ("authenticated X" /
// "authenticate X failed: …"). Errors are forwarded verbatim so
// the user sees the actual OAuth library message.
func authenticateMCPCmd(f facade.Facade, name string, force bool) tea.Cmd {
	return func() tea.Msg {
		// context.Background here is deliberate: the OAuth flow
		// must outlive the user's keystroke that triggered it.
		// The Facade implementation enforces its own timeout
		// (mcps/authenticate.go's ~1-minute budget).
		err := f.AuthenticateMCP(context.Background(), name, force)
		return mcpAuthDoneMsg{name: name, err: err}
	}
}

// testMCPConnectionCmd returns a tea.Cmd that runs an MCP
// connect-and-list-tools round trip off the BubbleTea goroutine.
// We keep it async because http MCPs can take a couple of
// seconds to handshake, and a stdio MCP has to spawn a process —
// neither of which we want to block the TUI on.
func testMCPConnectionCmd(f facade.Facade, name string) tea.Cmd {
	return func() tea.Msg {
		// The facade enforces its own 15s deadline so a hung
		// MCP doesn't pin the message loop forever.
		status, err := f.TestMCPConnection(context.Background(), name)
		return mcpTestDoneMsg{name: name, status: status, err: err}
	}
}

// resolveWorkerRef looks up a worker reference (either the opaque
// worker_id like "w_a3f9" or the friendly Spec.Name) against the
// live workers list cached on the model. Returns the canonical
// worker_id on success, "" when nothing matches.
//
// IDs win over names so a worker called "w_abc" (silly but legal)
// doesn't shadow a real id that happens to share its prefix. After
// the id pass we try exact name match, then case-insensitive prefix.
func (m *Model) resolveWorkerRef(ref string) string {
	if ref == "" {
		return ""
	}
	for _, w := range m.workers {
		if w.ID == ref {
			return w.ID
		}
	}
	for _, w := range m.workers {
		if w.Name == ref {
			return w.ID
		}
	}
	lower := strings.ToLower(ref)
	for _, w := range m.workers {
		if strings.HasPrefix(strings.ToLower(w.Name), lower) {
			return w.ID
		}
	}
	return ""
}

// handleSettingsCommand handles the `/settings` family. Settings are
// config-only: there is no in-memory toggle. The command is a thin
// front door to baifo.yaml.
//
//	/settings         show where baifo.yaml lives.
//	/settings edit    open baifo.yaml in the embedded editor.
//	/settings reload  re-read .baifo/ from disk.
//
// Editing opens the embedded editor; on save the file watcher detects
// the change and triggers a reload, so the user sees the new state
// without restarting baifo. Runtime preferences (auto-scroll,
// keep-tools-expanded, theme, a2a) all live in baifo.yaml and take
// effect on the next start — there is no live toggle for them.
func (m *Model) handleSettingsCommand(args []string) slashResult {
	if m.facade == nil {
		return slashResult{errorMessage: "no facade available"}
	}
	path := m.facade.ConfigDir() + "/baifo.yaml"
	if len(args) == 0 {
		return slashResult{systemMessage: "config file: " + path + "\n\nverbs:\n  /settings edit     open baifo.yaml in the editor\n  /settings reload   re-read config from disk"}
	}
	switch args[0] {
	case "edit":
		return slashResult{
			openEditor: &openEditorRequest{
				Title:    "Edit " + path,
				SavePath: path,
			},
			systemMessage: "opening " + path + " in editor…",
		}
	case "reload":
		// Manual trigger of ReloadFromDisk. The file watcher usually
		// does this on its own when the user saves baifo.yaml, but a
		// manual /settings reload is useful when something external (a
		// vcs checkout, a sync from another machine, ...) changed the
		// file behind baifo's back.
		//
		// We do NOT emit our own systemMessage here: the reload
		// produces a ReloadEvent that the Model handler already turns
		// into a "config reloaded" row. Doing both would double-line
		// the chat for one user action.
		ctx := context.Background()
		if err := m.facade.ReloadFromDisk(ctx); err != nil {
			return slashResult{errorMessage: fmt.Sprintf("/settings reload: %v", err)}
		}
		return slashResult{}
	default:
		return slashResult{errorMessage: "unknown /settings verb: " + args[0]}
	}
}

// startNewFact opens the embedded editor with the fact scaffold so
// the user can type a multi-line memory entry. Used by the /fact add
// path when no inline content was given.
func (m Model) startNewFact() (tea.Model, tea.Cmd) {
	return m.openEmbeddedEditor(openEditorRequest{
		Title:        "Add fact",
		InitialValue: factScaffold(),
		Kind:         editorKindFactUpsert,
	})
}

// factScaffold returns the YAML skeleton the editor opens for a new
// fact. Two fields: content (required, multiline allowed via the
// literal block scalar '|') and category (optional).
func factScaffold() string {
	return `# Adding a new fact to the agent's long-term memory.
#
# Save (Ctrl+S) to store, Esc to cancel.
# The agent will be able to recall this entry on future turns via
# its search_memory tool.

# Required. The text the agent should remember. Multi-line is fine;
# use a literal block scalar ('|') for readability.
content: |
  Replace this with the thing you want the agent to remember.

# Optional. A short tag so you can group related entries later.
# category: preference
`
}

// factEditScaffold returns the YAML skeleton seeded with the fact's
// current content. The category field is omitted because edits do
// not touch it; only the content is updated and the timestamp is
// refreshed to "now" on save.
func factEditScaffold(content string) string {
	var b strings.Builder
	b.WriteString("# Editing an existing fact.\n")
	b.WriteString("#\n")
	b.WriteString("# Save (Ctrl+S) to update, Esc to cancel.\n")
	b.WriteString("# Only 'content' is editable here. The timestamp is\n")
	b.WriteString("# refreshed to 'now' on save.\n")
	b.WriteString("\n")
	b.WriteString("content: |\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
