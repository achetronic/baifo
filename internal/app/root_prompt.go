// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"strings"
)

// root_prompt.go owns the chunk of system prompt that describes the
// root agent's special capabilities (the spawn family, memory,
// todos, skills, meta-tools, secrets) to whichever agent the user
// has flagged as root in agents.yaml.
//
// Why we inject this instead of leaving it in the user's prompt:
// the spawn / memory / todos / meta tools are NOT a property of
// "being the baifo brand"; they're a property of being the root. If
// the user demotes the default baifo agent and flags a custom one
// as root, that custom prompt would never know about
// search_memory or query_agent, the model would invent tool calls
// from scratch or just ignore the toolbox entirely.
//
// The split:
//   - The YAML prompt (in agents.yaml) carries PERSONA: voice,
//     style, domain focus, how to greet, what languages to mirror.
//   - This file carries CAPABILITIES: the names of the tools the
//     root has, when to reach for them, what they do.
//
// We compose at boot (and on every reload) from the live state of
// the App: only mention spawn when spawn is enabled, only mention
// meta-tools when they're switched on, etc., so the model never
// hears about a tool it can't actually call.

// rootCapabilitiesHeader is the leading line of the injected
// section. It sits between the user's prompt and the capabilities
// block so the model can see the seam clearly: "the following is
// non-negotiable, comes from the runtime, and lists what you
// physically have access to."
const rootCapabilitiesHeader = "## Runtime capabilities (managed by baifo - do not override)"

// rootMemoryBlock describes the long-term memory tools. Only
// included when the facts store is wired (always true today, but
// the check stays so disabling facts in a future config never
// leaves a stale instruction behind).
const rootMemoryBlock = `### Long-term memory (` + "`facts`" + `)
Tools: ` + "`search_memory`, `save_to_memory`, `update_memory`, `delete_memory`" + `.
Persists across sessions and restarts. Save anything the user
states as a stable fact about themselves, their projects, their
preferences, their domain, their workflow, or your own decisions
worth remembering ("the user is bilingual ES/EN", "they prefer
concise replies", "their main project lives at /repo/foo", "we
agreed X for reason Y"). Search it before answering questions that
may have been answered before.`

// rootTodosBlock describes the per-session task tracker. Always
// available because todos are an in-session SessionState key, no
// external dependency.
const rootTodosBlock = `### Task tracking (` + "`todos`" + `)
Tools: ` + "`todos_list`, `todos_write`, `todos_update`, `todos_clear`" + `.
Per-session checklist whose items survive context-window
compaction (the contextguard plugin forwards them through
summaries automatically). Use it for any multi-step work with 3+
distinct steps (coding, research, drafting, anything) so you
never lose track mid-way. Status is one of ` + "`pending`, `in_progress`, `completed`" +
	`; keep exactly one item in_progress.`

// rootSpawnBlock describes the worker fleet. Composed
// dynamically because baifo.yaml's spawn.mode toggles whether
// static templates, dynamic ad-hoc spawns, or both are live.
func rootSpawnBlock(staticEnabled, dynamicEnabled bool) string {
	if !staticEnabled && !dynamicEnabled {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Workers\n")
	switch {
	case staticEnabled && dynamicEnabled:
		b.WriteString("Tools: `spawn_static_agent` for templated workers, " +
			"`spawn_dynamic_agent` for ad-hoc ones (you compose prompt, MCPs, " +
			"skills, secrets). Use `query_agent`, `list_running_agents`, " +
			"`inspect_agent`, `collect_agent`, `kill_agent` to drive them.\n")
	case staticEnabled:
		b.WriteString("Tools: `spawn_static_agent` for templated workers from " +
			"`list_agents`. Use `query_agent`, `list_running_agents`, " +
			"`inspect_agent`, `collect_agent`, `kill_agent` to drive them.\n")
	case dynamicEnabled:
		b.WriteString("Tools: `spawn_dynamic_agent` to compose an ad-hoc worker " +
			"(you set prompt, MCPs, skills, secrets). Use `query_agent`, " +
			"`list_running_agents`, `inspect_agent`, `collect_agent`, " +
			"`kill_agent` to drive them.\n")
	}
	b.WriteString("Delegate anything that can run in parallel or that wants a ")
	b.WriteString("narrower focus than you can give it from the main chat.")
	if staticEnabled && dynamicEnabled {
		b.WriteString(" Prefer static templates from `list_agents` over " +
			"`spawn_dynamic_agent` when one fits.")
	}
	return b.String()
}

// rootSkillsBlock describes the skills loader. Always included
// when the skills loader is wired (we don't gate on the catalog
// being non-empty: the model still benefits from knowing the
// tools exist so it can react gracefully to a "no skills
// installed" outcome).
const rootSkillsBlock = `### Skills
Tools: ` + "`list_skills`, `load_skill`, `load_skill_resource`" + `.
Skills are how the user gives you domain-specific playbooks (a
writing style, a code-review checklist, a research protocol).
Check them before answering in an unfamiliar domain.`

// rootModelsBlock describes the model-discovery tool. Always present:
// the root needs it both to size a dynamic worker's model on the fly
// and to pick a sensible model id when the user asks it to author a
// static agent template in agents.yaml.
const rootModelsBlock = `### Models
Tool: ` + "`list_models`" + `.
Lists every model your configured providers offer, with each
provider's small/cheap and large/capable defaults plus per-model
context window, reasoning capability and cost. Call it before you
choose a model for a new agent: dynamic (` + "`spawn_dynamic_agent`" + `)
or static (when the user asks you to write an entry in agents.yaml),
so you match a smaller/cheaper or larger/more-capable model to the
task instead of defaulting to your own.`

// rootSecretsBlock is short and always present: the secrets
// pipeline is invariant. The model sees ${secret:NAME}
// placeholders and must not try to "fix" them by guessing the
// underlying value.
const rootSecretsBlock = `### Secrets
The user's secrets live in an encrypted store. You reference them
by placeholder: write ` + "`${secret:NAME}`" + ` literally in tool args
and the runtime expands them just before the call; the raw value
never enters your context. Never try to fabricate, paste, or echo
a secret's value. ` + "`list_secret_names`" + ` (when available) shows
which names are registered.`

// rootContextCompactionBlock is the recovery procedure after a
// context summary. Always relevant when the contextguard plugin
// is wired (and it is, unconditionally, for the root).
const rootContextCompactionBlock = `### After a context compaction (a summary appears mid-conversation)
The summarizer preserves the ` + "`todos`" + ` list automatically but it
may have dropped specifics from older turns. Before continuing,
silently:
  1. Call ` + "`todos_list`" + ` to recover the plan.
  2. Call ` + "`search_memory`" + ` with the topic you're working on, to
     re-surface relevant long-term facts.
Then resume from the in_progress todo. Don't announce these reads
to the user unless they ask.`

// rootCapabilitiesFlags bundles the runtime toggles that decide
// which capability blocks land in the final preamble. Filling it
// in is the App's job (see App.buildRootPrompt below); keeping
// the data on a struct lets the composer be pure and trivially
// testable.
type rootCapabilitiesFlags struct {
	Memory         bool
	Todos          bool
	StaticSpawn    bool
	DynamicSpawn   bool
	Models         bool
	Skills         bool
	Secrets        bool
	ContextCompact bool
}

// composeRootPrompt joins the user-supplied prompt with the
// capabilities block. The seam is two blank lines so the model
// reads the runtime section as its own logical chunk; the header
// is bold-flagged with a markdown heading so an LLM that respects
// structure understands it cannot be overridden by the user
// prompt above.
//
// Pure function (no I/O, no App access) so unit tests can pin
// the exact output for every flag combination.
func composeRootPrompt(userPrompt string, flags rootCapabilitiesFlags) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(userPrompt, "\n"))

	parts := make([]string, 0, 8)
	if flags.Memory {
		parts = append(parts, rootMemoryBlock)
	}
	if flags.Todos {
		parts = append(parts, rootTodosBlock)
	}
	if sp := rootSpawnBlock(flags.StaticSpawn, flags.DynamicSpawn); sp != "" {
		parts = append(parts, sp)
	}
	if flags.Models {
		parts = append(parts, rootModelsBlock)
	}
	if flags.Skills {
		parts = append(parts, rootSkillsBlock)
	}
	if flags.Secrets {
		parts = append(parts, rootSecretsBlock)
	}
	if flags.ContextCompact {
		parts = append(parts, rootContextCompactionBlock)
	}

	if len(parts) == 0 {
		return userPrompt
	}

	b.WriteString("\n\n")
	b.WriteString(rootCapabilitiesHeader)
	b.WriteString("\n\n")
	b.WriteString(strings.Join(parts, "\n\n"))
	return b.String()
}

// buildRootPrompt is the wiring entrypoint: it reads the live App
// state and the supplied user prompt, and returns the final
// system prompt the Builder should hand to the LLM. Called once
// per buildRoot pass (boot and every reload).
//
// The App holds the right locks; we read
// a.cfg (Secrets, Spawn), a.facts and a.skills, which are stable
// enough during a single buildRoot call that no explicit lock is
// required here.
func (a *App) buildRootPrompt(userPrompt string) string {
	flags := rootCapabilitiesFlags{
		// Memory: gated on the facts store actually being wired.
		// Disabling facts in a future config would silence the
		// memory paragraph automatically.
		Memory: a.facts != nil,
		// Todos: always available, the implementation is an
		// in-session SessionState key, no external dependency.
		Todos: true,
		// Spawn: mirrors the gate that decides whether the tools
		// are actually injected (see spawnToolsForRoot).
		StaticSpawn:  a.cfg.Spawn.StaticEnabled(),
		DynamicSpawn: a.cfg.Spawn.DynamicEnabled(),
		// Models: always available. list_models is wired
		// unconditionally so the root can size models both for
		// dynamic spawns and for static templates it authors.
		Models: true,
		// Skills: gated on the loader; we don't gate on a
		// non-empty catalogue because the model still benefits
		// from knowing the tool exists.
		Skills: a.skills != nil,
		// Secrets: always present, the pipeline runs for every
		// agent regardless of allowlist.
		Secrets: true,
		// ContextCompact: the contextguard plugin always wraps
		// the root, so the recovery instructions are always
		// relevant.
		ContextCompact: true,
	}
	return composeRootPrompt(userPrompt, flags)
}
