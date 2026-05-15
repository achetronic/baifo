// Licensed under the Apache License, Version 2.0; see LICENSE.

package yamledit

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// agentsFixture mixes a root entry, a sub-agent and comments so we can
// prove SetExclusiveBool flips the flag, strips the old one, and keeps
// the surrounding comments intact.
const agentsFixture = `# agents.yaml — hand-edited.
agents:
  - name: alpha      # the current root
    root: true
    description: first
  - name: bravo
    description: second
  - name: charlie
    description: third
`

func TestSetExclusiveBool_PromotesAndDemotes(t *testing.T) {
	p := writeFixture(t, agentsFixture)
	root, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if err := SetExclusiveBool(root, "agents", "bravo", "root"); err != nil {
		t.Fatalf("SetExclusiveBool: %v", err)
	}
	if err := SaveFile(p, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	reloaded, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile reload: %v", err)
	}
	if boolField(FindEntry(reloaded, "agents", "alpha"), "root") {
		t.Errorf("alpha should no longer be root")
	}
	if !boolField(FindEntry(reloaded, "agents", "bravo"), "root") {
		t.Errorf("bravo should be the new root")
	}
	if boolField(FindEntry(reloaded, "agents", "charlie"), "root") {
		t.Errorf("charlie should not be root")
	}

	out := mustRead(t, p)
	if !strings.Contains(out, "# agents.yaml — hand-edited.") {
		t.Errorf("head comment lost:\n%s", out)
	}
	// bravo's flag must read as an unquoted bool, not a quoted string.
	if !strings.Contains(out, "root: true") {
		t.Errorf("expected unquoted `root: true`:\n%s", out)
	}
	if strings.Contains(out, `root: "true"`) {
		t.Errorf("root flag was quoted as a string:\n%s", out)
	}
}

func TestSetExclusiveBool_UnknownNameLeavesFileUntouched(t *testing.T) {
	p := writeFixture(t, agentsFixture)
	root, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := SetExclusiveBool(root, "agents", "nope", "root"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// alpha still the lone root in memory.
	if !boolField(FindEntry(root, "agents", "alpha"), "root") {
		t.Errorf("alpha should still be root after a failed promotion")
	}
}

// boolField reports whether entry has `field` set to the boolean true.
func boolField(entry *yaml.Node, field string) bool {
	if entry == nil || entry.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if entry.Content[i].Value == field {
			return entry.Content[i+1].Value == "true"
		}
	}
	return false
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}
