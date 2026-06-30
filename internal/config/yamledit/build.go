// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package yamledit

import (
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/achetronic/baifo/internal/config"
)

// BuildMCPEntry converts a config.MCPEntry into a yaml.Node ready to
// be passed to UpsertMCP. Fields that are zero in the input are
// omitted from the resulting node, so the rendered YAML stays as
// concise as the user expects from a hand-written file.
//
// The field order is fixed and matches the documented schema: name,
// type, builtin, endpoint, insecure, headers, command, args, env,
// workdir, auth. This consistent layout helps diffs stay readable
// when the user edits baifo.yaml by hand later.
func BuildMCPEntry(e config.MCPEntry) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	addScalar(m, "name", e.Name)
	addScalar(m, "type", e.Type)

	if e.Type == "builtin" && e.Builtin != "" {
		addScalar(m, "builtin", e.Builtin)
	}

	if e.Endpoint != "" {
		addScalar(m, "endpoint", e.Endpoint)
	}
	if e.Insecure {
		addBool(m, "insecure", true)
	}
	if len(e.Headers) > 0 {
		addStringMap(m, "headers", e.Headers)
	}

	if e.Command != "" {
		addScalar(m, "command", e.Command)
	}
	if len(e.Args) > 0 {
		addStringList(m, "args", e.Args)
	}
	if len(e.Env) > 0 {
		addStringMap(m, "env", e.Env)
	}
	if e.Workdir != "" {
		addScalar(m, "workdir", e.Workdir)
	}

	if auth := buildAuth(e.Auth); auth != nil {
		addNode(m, "auth", auth)
	}

	return m
}

// buildAuth returns the auth sub-mapping for a MCPEntry, or nil when
// the auth block should be omitted entirely (kind=none and no other
// fields set). Returning nil instead of an empty mapping keeps the
// YAML clean for the common "I just want headers" case.
func buildAuth(a config.MCPAuth) *yaml.Node {
	if a.EffectiveKind() == config.MCPAuthKindNone &&
		a.ClientID == "" &&
		a.ClientSecretRef == "" {
		return nil
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addScalar(m, "kind", a.EffectiveKind())
	if a.ClientID != "" {
		addScalar(m, "client_id", a.ClientID)
	}
	if a.ClientSecretRef != "" {
		addScalar(m, "client_secret_ref", a.ClientSecretRef)
	}
	return m
}

// --- low-level append helpers ---

func addScalar(m *yaml.Node, key, val string) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val},
	)
}

func addBool(m *yaml.Node, key string, val bool) {
	v := "false"
	if val {
		v = "true"
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v},
	)
}

func addStringList(m *yaml.Node, key string, vals []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range vals {
		seq.Content = append(seq.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
}

// addStringMap renders a string→string map with sorted keys. Map
// iteration order is non-deterministic in Go, and we want stable
// YAML output so that re-saving a file with no changes yields a
// byte-identical result.
func addStringMap(m *yaml.Node, key string, vals map[string]string) {
	inner := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		addScalar(inner, k, vals[k])
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		inner,
	)
}

func addNode(m *yaml.Node, key string, node *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		node,
	)
}

// BuildAgentEntry converts a config.AgentTemplate into a yaml.Node
// ready to be passed to UpsertEntry with sectionKey="agents". The
// field order matches the on-disk convention used in agents.yaml so
// hand-rolled and TUI-edited files round-trip the same way.
//
// Empty fields are omitted to keep the YAML tight; users that want
// to advertise an explicit empty default (e.g. sandbox.mode:
// isolated) can type it themselves and the comment preservation
// machinery keeps it on subsequent edits.
func BuildAgentEntry(a config.AgentTemplate) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	addScalar(m, "name", a.Name)
	if a.Root {
		// Marshalled with the !!bool tag so the YAML reads as
		// `root: true` (no quotes) and round-trips back into a
		// Go bool cleanly when LoadAgents re-parses the file.
		addBool(m, "root", true)
	}
	if a.Utility {
		addBool(m, "utility", true)
	}
	if a.Description != "" {
		addScalar(m, "description", a.Description)
	}
	if a.Prompt != "" {
		// Multi-line prompts render nicest as literal block scalars
		// (|), which yaml.v3 picks automatically when the string
		// contains a newline. We just hand it the value.
		prompt := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: a.Prompt}
		if containsNewline(a.Prompt) {
			prompt.Style = yaml.LiteralStyle
		}
		addNode(m, "prompt", prompt)
	}

	if a.LLM.Effective() != "" || a.LLM.Model != "" {
		llm := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if p := a.LLM.Effective(); p != "" {
			addScalar(llm, "provider", p)
		}
		if a.LLM.Model != "" {
			addScalar(llm, "model", a.LLM.Model)
		}
		addNode(m, "llm", llm)
	}

	if len(a.Skills) > 0 {
		addStringList(m, "skills", a.Skills)
	}
	if len(a.MCPs) > 0 {
		addStringList(m, "mcps", a.MCPs)
	}
	if len(a.AllowedSecrets) > 0 {
		addStringList(m, "allowed_secrets", a.AllowedSecrets)
	}

	return m
}

// containsNewline reports whether s has at least one '\n'. Tiny
// helper to keep BuildAgentEntry readable.
func containsNewline(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return true
		}
	}
	return false
}

// BuildProviderEntry converts a config.ProviderEntry into a yaml.Node
// ready to be passed to UpsertEntry with sectionKey="providers".
// Fields are emitted in the documented order; empty fields are
// omitted so the rendered YAML stays tight.
func BuildProviderEntry(p config.ProviderEntry) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addScalar(m, "name", p.Name)
	if p.Type != "" {
		addScalar(m, "type", p.Type)
	}
	if p.URL != "" {
		addScalar(m, "url", p.URL)
	}
	if p.APIKey != "" {
		addScalar(m, "api_key", p.APIKey)
	}
	if len(p.Headers) > 0 {
		addStringMap(m, "headers", p.Headers)
	}
	// Only emit streaming when the operator set it explicitly (pointer
	// is non-nil); an omitted field means "use the default" and we keep
	// the rendered YAML tight by not writing it.
	if p.Streaming != nil {
		addBool(m, "streaming", *p.Streaming)
	}
	return m
}
