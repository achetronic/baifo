// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

// ResolveMCPs implements the "empty list means all" rule for the
// root agent's MCP catalogue.
//
//   - configured == nil or empty → return every MCP currently registered.
//     The root is the coordinator of the whole system; it sees what the
//     user has connected without having to maintain a redundant
//     allowlist in baifo.yaml.
//
//   - configured non-empty → respect it verbatim. Power users that want
//     to hide a particular MCP from the root (e.g. a destructive one
//     that should only be reachable through a specific static worker)
//     still have a way to opt out by listing only the MCPs they want.
//
// The function does NOT validate that configured names exist in the
// registry; the agent builder reports unknown names with a clearer
// error later, and that path is exercised by an existing test.
func ResolveMCPs(configured []string, allRegistered []string) []string {
	if len(configured) > 0 {
		return configured
	}
	if len(allRegistered) == 0 {
		return nil
	}
	out := make([]string, len(allRegistered))
	copy(out, allRegistered)
	return out
}

// ResolveSkills mirrors ResolveMCPs for skills: empty list in
// the config means "load every skill baifo knows about", a non-empty
// list is a hard restriction.
//
// Skills are non-mutating instructions (markdown files), so giving the
// root access to all of them is low-risk. Static workers keep their
// own narrow skill list because their job is narrower by design.
func ResolveSkills(configured []string, allAvailable []string) []string {
	if len(configured) > 0 {
		return configured
	}
	if len(allAvailable) == 0 {
		return nil
	}
	out := make([]string, len(allAvailable))
	copy(out, allAvailable)
	return out
}
