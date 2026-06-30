// SPDX-License-Identifier: Apache-2.0

// Package yamledit provides comment-preserving edits to baifo.yaml.
//
// Why this exists: baifo lets users hand-edit baifo.yaml AND also offers
// CRUD wizards from the TUI. If the wizards rewrote the file by
// marshalling the in-memory config.Config struct, every comment, blank
// line and key-order tweak the user made by hand would be wiped on the
// next "save". Operators would learn to never use the wizard.
//
// This package operates on yaml.Node trees instead: it loads the file
// as a Node, surgically inserts/updates/deletes the requested item,
// and re-marshals only what changed. Comments, indentation and
// unrelated keys round-trip untouched.
//
// Scope of v1: only the `mcps` sequence is supported. Other top-level
// blocks (providers, agents, ...) will get their own helpers as the
// CRUD UI grows.
package yamledit

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// MCPsKey is the top-level YAML key under which MCP entries live.
const MCPsKey = "mcps"

// ErrNotFound is returned by DeleteMCP when the requested name does
// not exist in the mcps sequence.
var ErrNotFound = errors.New("mcp not found")

// LoadFile reads path and returns its root *yaml.Node. Pass the result
// to UpsertMCP / DeleteMCP and persist it back with SaveFile.
//
// A missing file is NOT treated as an error here: an empty document
// is returned instead so callers can build baifo.yaml from scratch
// without an extra existence check.
func LoadFile(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyDoc(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return emptyDoc(), nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if root.Kind == 0 {
		return emptyDoc(), nil
	}
	return &root, nil
}

// SaveFile marshals root back to path. The file is written atomically
// via a temp file + rename so a crash mid-write cannot leave an
// invalid baifo.yaml on disk.
//
// Before encoding, SaveFile performs two normalisation passes that
// keep the on-disk file clean regardless of how the node tree was
// assembled:
//
//  1. Multiline string scalars whose lines have trailing spaces or tabs
//     are trimmed and forced to literal-block style (|). This prevents
//     yaml.v3's emitter from downgrading them to a double-quoted
//     one-liner with \n escapes, which would corrupt prompts and stay
//     corrupted forever.
//
//  2. Merge-key scalars (tag "!!merge", value "<<") have their tag
//     cleared so the emitter writes plain `<<:` instead of the noisy
//     `!!merge <<:` prefix that yaml.v3 otherwise adds on round-trip.
//
// The indent width is sniffed from the existing file on disk so that
// a user's 4-space (or 2-space) formatting is preserved rather than
// being overwritten by an arbitrary default.
func SaveFile(path string, root *yaml.Node) error {
	indent := sniffIndent(path)
	normalizeNodes(root)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshal close: %w", err)
	}

	out := buf.Bytes()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		if err := os.Remove(tmp); err != nil {
			slog.Warn("cannot remove temp file", "path", tmp, "error", err)
		}
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// UpsertMCP inserts a new MCP entry into root.mcps, or replaces the
// existing one with the same name. entry must be a MappingNode.
//
// The original mcps sequence is preserved: append on insert, in-place
// replace on update. Any head/foot/line comments on the surrounding
// sequence stay where they were.
func UpsertMCP(root *yaml.Node, name string, entry *yaml.Node) error {
	return UpsertEntry(root, MCPsKey, name, entry)
}

// UpsertEntry is the generic version of UpsertMCP. The top-level
// sectionKey ("mcps", "agents", "providers", ...) is created if
// missing; entries inside it are identified by their "name" field.
//
// Useful for any baifo.yaml or agents.yaml block that uses the same
// shape: a sequence of mappings each carrying a Name. New CRUDs
// (agents, providers, secrets) wire to this helper without
// duplicating the comment-preservation logic.
func UpsertEntry(root *yaml.Node, sectionKey, name string, entry *yaml.Node) error {
	if entry == nil || entry.Kind != yaml.MappingNode {
		return fmt.Errorf("entry must be a mapping node, got kind %d", kindOf(entry))
	}
	mapping := topMapping(root)
	if mapping == nil {
		return errors.New("root yaml document does not contain a mapping")
	}

	seq := findOrCreateSequence(mapping, sectionKey)
	for i, item := range seq.Content {
		if itemName(item) == name {
			seq.Content[i] = entry
			return nil
		}
	}
	seq.Content = append(seq.Content, entry)
	return nil
}

// DeleteMCP removes the MCP entry whose name field equals name. Returns
// ErrNotFound if no such entry exists.
func DeleteMCP(root *yaml.Node, name string) error {
	return DeleteEntry(root, MCPsKey, name)
}

// DeleteEntry is the generic version of DeleteMCP. The named entry
// is removed from the sectionKey sequence; the sequence itself is
// preserved even if it becomes empty (the user sees the section
// header staying put as a visual reminder).
func DeleteEntry(root *yaml.Node, sectionKey, name string) error {
	mapping := topMapping(root)
	if mapping == nil {
		return errors.New("root yaml document does not contain a mapping")
	}
	seq := findSequence(mapping, sectionKey)
	if seq == nil {
		return ErrNotFound
	}
	for i, item := range seq.Content {
		if itemName(item) == name {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// SetExclusiveBool makes name the sole carrier of a boolean field
// within sectionKey: it sets `field: true` on that entry and ensures
// every other entry has an effective value of false for the field.
//
// Why merge-awareness matters: agents.yaml commonly uses YAML anchors
// and merge keys (<<: *anchor) so that shared settings like `prompt`
// live in one place. When the OLD holder of `field: true` is an anchor
// entry, all `<<: *anchor` consumers inherit the flag at decode time.
// The naïve approach (set target to true, removeField on everyone else)
// leaves those consumers still inheriting true — the loader's
// at-most-one validation then fails.
//
// The fix: for each non-target entry, removeField strips any explicit
// key. Then mergeInheritedBool follows the <<-chain in the CURRENT
// node tree; if the entry would still end up true through inheritance,
// an explicit `field: false` override is inserted to block it.
//
// Returns ErrNotFound when name is absent under sectionKey, so the
// caller can leave the file untouched.
func SetExclusiveBool(root *yaml.Node, sectionKey, name, field string) error {
	mapping := topMapping(root)
	if mapping == nil {
		return errors.New("root yaml document does not contain a mapping")
	}
	seq := findSequence(mapping, sectionKey)
	if seq == nil {
		return ErrNotFound
	}
	// Pre-scan: confirm the target exists before mutating anything,
	// so a typo'd name leaves the tree (and the file) untouched.
	found := false
	for _, item := range seq.Content {
		if item != nil && item.Kind == yaml.MappingNode && itemName(item) == name {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	for _, item := range seq.Content {
		if item == nil || item.Kind != yaml.MappingNode {
			continue
		}
		if itemName(item) == name {
			setBoolField(item, field, true)
		} else {
			// Remove any explicit value for field so the entry is
			// clean before we re-evaluate what it inherits.
			removeField(item, field)
			// If the entry still inherits field=true through a <<
			// merge key, insert an explicit false to shadow it.
			// Without this override the YAML decoder would promote
			// the entry to "true" via the anchor, breaking the
			// at-most-one invariant enforced by LoadAgents.
			visited := make(map[*yaml.Node]bool)
			if mergeInheritedBool(item, field, visited) {
				setBoolField(item, field, false)
			}
		}
	}
	return nil
}

// setBoolField sets `field` to val on a mapping entry. When the key
// already exists its value node is rewritten in place (keeping the
// key's position and any attached comments); otherwise the pair is
// inserted right after the `name` key, or appended if there is none.
func setBoolField(entry *yaml.Node, field string, val bool) {
	v := "false"
	if val {
		v = "true"
	}
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if entry.Content[i].Value == field {
			entry.Content[i+1].Kind = yaml.ScalarNode
			entry.Content[i+1].Tag = "!!bool"
			entry.Content[i+1].Value = v
			entry.Content[i+1].Style = 0
			return
		}
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field}
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
	insertAt := len(entry.Content)
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if entry.Content[i].Value == "name" {
			insertAt = i + 2
			break
		}
	}
	rest := append([]*yaml.Node(nil), entry.Content[insertAt:]...)
	entry.Content = append(entry.Content[:insertAt], key, value)
	entry.Content = append(entry.Content, rest...)
}

// removeField drops the key/value pair for field from a mapping
// entry. No-op when the key is absent.
func removeField(entry *yaml.Node, field string) {
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if entry.Content[i].Value == field {
			entry.Content = append(entry.Content[:i], entry.Content[i+2:]...)
			return
		}
	}
}

// mergeInheritedBool reports whether field evaluates to true through
// merge-key (<<: *anchor) inheritance for mapping node n. It does NOT
// check the explicit key on n itself — call it AFTER removeField has
// already been applied, so the function sees only what would come
// through the anchor chain.
//
// The visited map guards against anchor cycles: if the same mapping
// appears twice during the walk we skip it rather than looping
// forever. Each call-site should pass a freshly allocated map.
func mergeInheritedBool(n *yaml.Node, field string, visited map[*yaml.Node]bool) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}
	if visited[n] {
		return false
	}
	visited[n] = true

	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k.Value != "<<" {
			continue
		}
		v := n.Content[i+1]
		switch v.Kind {
		case yaml.AliasNode:
			if v.Alias != nil && effectiveBoolViaAnchor(v.Alias, field, visited) {
				return true
			}
		case yaml.SequenceNode:
			for _, item := range v.Content {
				if item.Kind == yaml.AliasNode && item.Alias != nil {
					if effectiveBoolViaAnchor(item.Alias, field, visited) {
						return true
					}
				}
			}
		}
	}
	return false
}

// effectiveBoolViaAnchor returns the effective boolean value of field
// inside the anchor mapping n, considering both the explicit key (if
// present) and any further <<-inheritance within n itself. This is the
// recursive companion to mergeInheritedBool: together they implement
// a depth-first walk of the anchor DAG.
func effectiveBoolViaAnchor(n *yaml.Node, field string, visited map[*yaml.Node]bool) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}
	if visited[n] {
		return false
	}
	// Check explicit key first (explicit always wins over inherited).
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == field {
			return n.Content[i+1].Value == "true"
		}
	}
	// Fall back to what n itself inherits through its own <<.
	return mergeInheritedBool(n, field, visited)
}

// normalizeNodes performs a recursive normalization pass over the
// yaml.Node tree before it is marshalled by SaveFile. Two categories
// of nodes are touched; everything else is left completely alone so
// that comments, anchors, and user formatting round-trip untouched.
//
// 1. String scalars with embedded newlines:
//   - Trailing spaces and tabs are stripped from every line. This
//     prevents yaml.v3's emitter from silently downgrading a literal
//     block scalar to a double-quoted one-liner with \n escapes: the
//     spec strips trailing whitespace at line breaks inside block
//     scalars, so the emitter refuses to use block style when a line
//     ends in whitespace.
//   - Style is forced to LiteralStyle (|). Even if the value was
//     previously collapsed into DoubleQuotedStyle by an old version of
//     SaveFile, this repairs it on the next write.
//
// 2. Merge-key scalars (tag "!!merge", value "<<"):
//   - The tag is cleared to "" so the emitter writes plain `<<:`
//     instead of `!!merge <<:`. yaml.v3's decoder still recognises
//     empty-tag << as a merge key (isMerge checks n.Tag == "").
//
// Alias nodes carry no Content so the walk over n.Content is safe.
func normalizeNodes(n *yaml.Node) {
	if n == nil {
		return
	}
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!str" && strings.Contains(n.Value, "\n"):
		// Trim trailing whitespace from every line so the emitter can
		// safely use block-scalar style without violating the spec.
		lines := strings.Split(n.Value, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimRight(line, " \t")
		}
		n.Value = strings.Join(lines, "\n")
		n.Style = yaml.LiteralStyle
	case n.Kind == yaml.ScalarNode && n.Tag == "!!merge":
		// Clear the explicit !!merge tag. The encoder emits implicit
		// tags (tag == "") as plain values; the decoder accepts
		// Value="<<" with Tag="" as a merge key.
		n.Tag = ""
	}
	for _, child := range n.Content {
		normalizeNodes(child)
	}
}

// sniffIndent reads the existing file at path and returns the
// indentation width of the first line that begins with spaces (a
// proxy for the indent under the first top-level YAML mapping key).
// The result is clamped to [2, 8]; if the file is absent, empty, or
// has no indented lines 2 is returned so new files get a sensible
// default. Sniffing before each SaveFile keeps the user's chosen
// formatting stable across TUI-driven edits.
func sniffIndent(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 2
	}
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 || line[0] != ' ' {
			continue
		}
		n := 0
		for n < len(line) && line[n] == ' ' {
			n++
		}
		if n >= 2 {
			if n > 8 {
				return 8
			}
			return n
		}
	}
	return 2
}

// ListMCPNames returns every MCP name declared under root.mcps, in
// the order they appear on disk.
func ListMCPNames(root *yaml.Node) []string {
	return ListEntryNames(root, MCPsKey)
}

// ListEntryNames is the generic version of ListMCPNames. Returns
// every name found under sectionKey in document order, or an empty
// slice when the key is absent.
func ListEntryNames(root *yaml.Node, sectionKey string) []string {
	mapping := topMapping(root)
	if mapping == nil {
		return nil
	}
	seq := findSequence(mapping, sectionKey)
	if seq == nil {
		return nil
	}
	out := make([]string, 0, len(seq.Content))
	for _, item := range seq.Content {
		if n := itemName(item); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// FindMCP returns the *yaml.Node of the named MCP entry, or nil if
// not found.
func FindMCP(root *yaml.Node, name string) *yaml.Node {
	return FindEntry(root, MCPsKey, name)
}

// FindEntry is the generic version of FindMCP. Returns the entry
// node aliasing the document; callers should treat it as read-only
// or deep-copy before mutating.
func FindEntry(root *yaml.Node, sectionKey, name string) *yaml.Node {
	mapping := topMapping(root)
	if mapping == nil {
		return nil
	}
	seq := findSequence(mapping, sectionKey)
	if seq == nil {
		return nil
	}
	for _, item := range seq.Content {
		if itemName(item) == name {
			return item
		}
	}
	return nil
}

// --- helpers below this line ---

// emptyDoc returns a root that marshals back to an empty mapping. We
// avoid a zero-value Node because yaml.Marshal of one yields "null\n".
func emptyDoc() *yaml.Node {
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{mapping},
	}
}

// topMapping returns the top-level mapping node of root, accounting
// for the DocumentNode wrapper produced by yaml.Unmarshal. Returns
// nil if the document does not start with a mapping (e.g. a sequence
// or a scalar), which we cannot edit safely.
func topMapping(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// findSequence returns the SequenceNode child of mapping for key, or
// nil if it does not exist.
func findSequence(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k, v := mapping.Content[i], mapping.Content[i+1]
		if k.Value == key {
			if v.Kind == yaml.SequenceNode {
				return v
			}
			return nil
		}
	}
	return nil
}

// findOrCreateSequence is like findSequence but appends a fresh empty
// sequence under key if the key is absent. When the key exists but
// its value is null (e.g. "mcps:" with no body), the value is
// overwritten with an empty sequence so we can append to it.
func findOrCreateSequence(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k, v := mapping.Content[i], mapping.Content[i+1]
		if k.Value == key {
			if v.Kind == yaml.SequenceNode {
				return v
			}
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			mapping.Content[i+1] = seq
			return seq
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	seqNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mapping.Content = append(mapping.Content, keyNode, seqNode)
	return seqNode
}

// itemName returns the value of the "name" field of a mapping node
// representing one MCP entry, or "" if the field is missing.
func itemName(item *yaml.Node) string {
	if item == nil || item.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value == "name" {
			return item.Content[i+1].Value
		}
	}
	return ""
}

func kindOf(n *yaml.Node) yaml.Kind {
	if n == nil {
		return 0
	}
	return n.Kind
}
