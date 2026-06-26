// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"sort"
	"strings"
)

// palette_tree.go is the single source of truth for the slash-command
// autocomplete popup. The actual dispatcher in commands.go switches
// on strings; we mirror that switch as a navigable tree so the popup
// can show:
//
//   1. every top-level command after the user types `/`;
//   2. every sub-verb after `/mcps ` (and the same for every other
//      family);
//   3. nothing once the user has typed past the last verb that has
//      structured children (e.g. `/mcps add my-name <arg>`), because
//      everything after that point is free-form data the popup has
//      no useful suggestions for.
//
// The tree is intentionally a static var instead of being introspected
// from the dispatcher: the dispatcher's switch statements aren't
// reflectable, and duplicating the structure here is cheap, easy to
// review, and keeps the help text close to the verbs themselves.
// When you add a new sub-verb to commands.go, mirror it below.

// commandNode is one entry in the slash-command tree. Names live
// stripped of the leading "/": top-level nodes are "sessions",
// "mcps", etc. Children inherit the same shape so the tree can
// nest arbitrarily, even though today nothing goes beyond depth 2.
type commandNode struct {
	// Name is the literal token the user types. Always lower-case.
	Name string

	// Summary is the one-line description shown to the right of
	// the name in the popup. Keep it under ~50 columns so the
	// popup stays readable on narrow terminals.
	Summary string

	// Usage is the longer "what arguments follow" hint, surfaced
	// in the popup footer when this node is the active selection.
	// Empty means "no arguments".
	Usage string

	// Children are the deeper sub-verbs. nil means this is a leaf
	// command (the popup closes once the user moves past it).
	Children []commandNode
}

// slashCommandTree mirrors the dispatch tree in commands.go. Top-level
// entries are sorted alphabetically except where a more useful order
// (e.g. /help and /quit at the bottom) reads better in the popup.
var slashCommandTree = []commandNode{
	{
		Name:    "agent",
		Summary: "manage static agent templates",
		Usage:   "[list|add|edit|delete|set-root] [NAME]",
		Children: []commandNode{
			{Name: "list", Summary: "list configured static agents"},
			{Name: "add", Summary: "create a new agent template", Usage: "[NAME]"},
			{Name: "edit", Summary: "edit an existing agent", Usage: "NAME"},
			{Name: "delete", Summary: "delete an agent template", Usage: "NAME"},
			{Name: "set-root", Summary: "make this agent the root", Usage: "NAME"},
		},
	},
	{
		Name:    "fact",
		Summary: "manage the agent's long-term memory",
		Usage:   "[list|add|edit|delete] [...]",
		Children: []commandNode{
			{Name: "list", Summary: "browse stored facts in an overlay"},
			{Name: "add", Summary: "add a fact (inline text or open editor)", Usage: "[\"content\"]"},
			{Name: "edit", Summary: "edit fact by id", Usage: "ID"},
			{Name: "delete", Summary: "delete fact by id", Usage: "ID"},
		},
	},
	{
		Name:    "mcp",
		Summary: "manage MCP servers",
		Usage:   "[list|add|edit|delete|authenticate|test|logout] [NAME]",
		Children: []commandNode{
			{Name: "list", Summary: "list configured MCPs"},
			{Name: "add", Summary: "add a new MCP entry", Usage: "NAME"},
			{Name: "edit", Summary: "edit an existing MCP", Usage: "NAME"},
			{Name: "delete", Summary: "delete an MCP entry", Usage: "NAME"},
			{Name: "auth", Summary: "run the OAuth flow for an MCP", Usage: "NAME [--force]"},
			{Name: "test", Summary: "connect + list tools to verify the MCP", Usage: "NAME"},
			{Name: "logout", Summary: "forget cached credentials (token + DCR client)", Usage: "NAME"},
		},
	},
	{
		Name:    "provider",
		Summary: "manage LLM providers",
		Usage:   "[list|add|edit|delete] [NAME]",
		Children: []commandNode{
			{Name: "list", Summary: "list configured providers"},
			{Name: "add", Summary: "create a new provider entry", Usage: "[NAME]"},
			{Name: "edit", Summary: "edit an existing provider", Usage: "NAME"},
			{Name: "delete", Summary: "delete a provider entry", Usage: "NAME"},
		},
	},
	{
		Name:    "root",
		Summary: "switch the chat back to the root agent",
	},
	{
		Name:    "secret",
		Summary: "manage encrypted secrets",
		Usage:   "[list|set|delete|encode|decode] [NAME]",
		Children: []commandNode{
			{Name: "list", Summary: "list secret names (never values)"},
			{Name: "set", Summary: "set a secret via the masked prompt", Usage: "[NAME]"},
			{Name: "delete", Summary: "delete a secret by name", Usage: "NAME"},
			{Name: "encode", Summary: "encrypt every plaintext entry on disk"},
			{Name: "decode", Summary: "decrypt every entry into plaintext"},
		},
	},
	{
		Name:    "session",
		Summary: "manage chat sessions",
		Usage:   "[list|new|switch|rename|delete] [ID]",
		Children: []commandNode{
			{Name: "list", Summary: "open the sessions list overlay"},
			{Name: "new", Summary: "start a fresh session"},
			{Name: "switch", Summary: "activate an existing session", Usage: "ID"},
			{Name: "rename", Summary: "rename a session", Usage: "ID new title"},
			{Name: "delete", Summary: "delete a session", Usage: "ID"},
		},
	},
	{
		Name:    "settings",
		Summary: "view, edit or reload baifo.yaml",
		Usage:   "[edit|reload]",
		Children: []commandNode{
			{Name: "edit", Summary: "open baifo.yaml in the embedded editor"},
			{Name: "reload", Summary: "re-read config from disk"},
		},
	},
	{
		Name:    "skill",
		Summary: "manage installed skills",
		Usage:   "[list|add|edit|delete|install] [NAME|URL]",
		Children: []commandNode{
			{Name: "list", Summary: "list installed skills"},
			{Name: "add", Summary: "create a new skill scaffold", Usage: "[NAME]"},
			{Name: "edit", Summary: "edit a skill's SKILL.md", Usage: "NAME"},
			{Name: "delete", Summary: "delete a skill package", Usage: "NAME"},
			{Name: "install", Summary: "install a skill from a URL", Usage: "URL"},
		},
	},
	{
		Name:    "worker",
		Summary: "manage live workers",
		Usage:   "[list|talk|kill|collect] [ID|name]",
		Children: []commandNode{
			{Name: "list", Summary: "open the live workers overlay"},
			{Name: "talk", Summary: "switch the chat to a worker", Usage: "ID|name"},
			{Name: "kill", Summary: "cancel a running worker", Usage: "ID|name"},
			{Name: "collect", Summary: "harvest a worker's output", Usage: "ID|name"},
		},
	},
	{
		Name:    "help",
		Summary: "show keybindings and command list",
	},
	{
		Name:    "quit",
		Summary: "exit baifo",
	},
}

// paletteSuggest computes what should appear in the autocomplete
// popup given the raw composer text (including the leading slash).
// It is pure: deterministic, no I/O, no Model access — which makes
// it trivial to unit-test the whole tree-walk + filter logic.
//
// The contract is:
//
//   - When `line` does not start with '/', return no suggestions
//     and `visible == false`. The popup is hidden.
//
//   - When `line == "/"`, return every top-level node. This is the
//     "I just hit slash" state.
//
//   - When `line == "/mc"`, walk into the top level, filter by
//     prefix "mc", return the matches. The popup is in
//     "completing the verb the user is currently typing" mode.
//
//   - When `line == "/mcps "` (trailing space) — meaning the user
//     committed `mcps` and is now starting the next token — walk
//     into the `mcps` node's children and return them all
//     unfiltered. This is the cascade that gives the feature its
//     "multi-level" feel.
//
//   - When `line == "/mcps ad"`, return only `add`.
//
//   - When `line == "/mcps add my-name"` and the matched leaf has
//     no further structured children, return no suggestions (the
//     remaining tokens are free-form arguments — a NAME, an ID, an
//     URL). The popup hides itself so it stops getting in the way
//     once the user is typing data.
//
// The function also reports the substring of `line` that should be
// replaced when the user accepts the highlighted suggestion. That
// is, the prefix of the last token under the cursor. For the empty
// trailing token (after a space) the replace range is empty and
// acceptance simply appends.
//
// Returned values:
//   - visible:    whether the popup should be shown at all.
//   - matches:    items to render (already trimmed by prefix).
//   - replaceLen: how many bytes at the end of `line` will be
//     replaced by the chosen completion. Always
//     0 <= replaceLen <= len(lastToken).
func paletteSuggest(line string) (visible bool, matches []paletteItem, replaceLen int) {
	if !strings.HasPrefix(line, "/") {
		return false, nil, 0
	}

	// Drop the leading slash; everything else is whitespace-delimited
	// tokens. We keep an eye on whether the line ends with a space:
	// trailing whitespace means "the previous token is committed and
	// we're starting the next one", which changes whether we filter
	// or list-all at the current depth.
	rest := line[1:]
	trailingSpace := strings.HasSuffix(rest, " ")

	// strings.Fields collapses arbitrary runs of whitespace, which
	// is what we want: the user typing `/mcps   ad` should still
	// resolve to the `mcps` node and a prefix of `ad`.
	tokens := strings.Fields(rest)

	// Walk down the tree following the committed tokens. Every
	// token EXCEPT the last must resolve to an exact-named child;
	// if any of them doesn't, the current input is past the
	// structured part of the tree and we hide the popup.
	cursor := slashCommandTree
	depth := 0
	// committedCount = how many tokens we have already "consumed"
	// to descend the tree, leaving the rest (zero or one tokens)
	// as the active prefix.
	committedCount := len(tokens)
	if !trailingSpace && committedCount > 0 {
		// The last token is still being typed — it's the prefix,
		// not a committed verb. Walk down with one fewer token.
		committedCount--
	}

	for i := 0; i < committedCount; i++ {
		next, ok := findChild(cursor, tokens[i])
		if !ok {
			// The user typed something we don't know how to
			// descend into. Keep the popup hidden so we don't
			// guess.
			return false, nil, 0
		}
		if len(next.Children) == 0 {
			// This verb has no structured children; the next
			// tokens are free-form arguments. Hide the popup
			// rather than nag the user with stale suggestions
			// from a sibling branch.
			return false, nil, 0
		}
		cursor = next.Children
		depth++
	}

	// Compute the active prefix and the replace length.
	prefix := ""
	if !trailingSpace && len(tokens) > committedCount {
		prefix = tokens[committedCount]
	}
	replaceLen = len(prefix)

	// Build the suggestion list. When prefix is empty (we just
	// landed at a fresh level via a space) we return everything;
	// otherwise we case-insensitively prefix-filter so the popup
	// narrows as the user types.
	lowerPrefix := strings.ToLower(prefix)
	matches = matches[:0]
	for _, n := range cursor {
		if lowerPrefix == "" || strings.HasPrefix(strings.ToLower(n.Name), lowerPrefix) {
			matches = append(matches, paletteItem{
				Name:    n.Name,
				Summary: n.Summary,
				Usage:   n.Usage,
				IsLeaf:  len(n.Children) == 0,
				// At depth 0 the displayed name is "/name"; deeper
				// in the tree it's just "name" because the chain
				// up to it is already in the composer.
				DisplayName: prependSlashIf(n.Name, depth == 0),
			})
		}
	}

	if len(matches) == 0 {
		return false, nil, 0
	}

	// Single alphabetical order — no view/command grouping. Everything
	// in the popup is a command and they sort case-insensitively by
	// name at every depth.
	sort.Slice(matches, func(i, j int) bool {
		return strings.ToLower(matches[i].Name) < strings.ToLower(matches[j].Name)
	})

	return true, matches, replaceLen
}

// findChild returns the named child of the given node list, with a
// case-insensitive exact match. Returns ok=false when nothing
// matches.
func findChild(nodes []commandNode, name string) (commandNode, bool) {
	lower := strings.ToLower(name)
	for _, n := range nodes {
		if strings.ToLower(n.Name) == lower {
			return n, true
		}
	}
	return commandNode{}, false
}

// prependSlashIf returns "/"+name when atTopLevel, else name. The
// popup shows top-level commands with the slash so the visual
// matches the trigger keystroke ("/" is what the user just typed);
// deeper levels show bare verbs because the slash is already in
// the composer text.
func prependSlashIf(name string, atTopLevel bool) string {
	if atTopLevel {
		return "/" + name
	}
	return name
}

// paletteItem is one row in the popup. It's a flattened view of
// the underlying commandNode plus a couple of presentation flags
// computed at suggest time.
type paletteItem struct {
	// Name is the raw token the user accepted ("add", "list",
	// "mcps"). Inserted into the composer verbatim.
	Name string

	// DisplayName is what gets painted in the popup row. At the
	// top level it includes a leading "/"; at deeper levels it's
	// just the bare verb. Cosmetic only.
	DisplayName string

	// Summary mirrors commandNode.Summary.
	Summary string

	// Usage mirrors commandNode.Usage. Carried for reference and
	// possible future surfaces (the popup itself no longer renders a
	// usage footer — it crowds the narrow composer).
	Usage string

	// IsLeaf is true when accepting this item lands the user on a
	// node with no further children. The popup uses it to decide
	// whether to append a trailing space (more verbs to type) or
	// not (the next thing is free-form).
	IsLeaf bool
}
