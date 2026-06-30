// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package yamledit

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// anchorFixture exercises the full merge-key topology that triggered the
// three bugs: one anchor entry carrying root: true and two merge-key
// consumers that silently inherit it via <<: *coordinator.  Running all
// four tests against a single fixture lets us spot regressions across the
// entire interaction surface at once.
const anchorFixture = `agents:
  - &coordinator
    name: coordinator-opus
    root: true
  - <<: *coordinator
    name: coordinator-google
  - <<: *coordinator
    name: coordinator-azure
`

// agentEntry / agentsDoc are minimal decode targets used to verify full
// yaml.Unmarshal merge semantics — the same perspective baifo's loader has
// when it reads agents.yaml.  A struct decode (not a raw yaml.Node lookup)
// means go-yaml resolves every <<: merge, so "coordinator-google inherits
// root: true from the anchor" is exactly what the test measures.
type agentEntry struct {
	Name string `yaml:"name"`
	Root bool   `yaml:"root"`
}

type agentsDoc struct {
	Agents []agentEntry `yaml:"agents"`
}

// decodeAgents fully unmarshals data and returns the agent slice with all
// merge keys already applied.
func decodeAgents(t *testing.T, data []byte) []agentEntry {
	t.Helper()
	var doc agentsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decodeAgents: %v", err)
	}
	return doc.Agents
}

// TestSetExclusiveBool_MergeConsumerPromoted promotes coordinator-google,
// which currently holds no explicit root key but INHERITS root: true from
// the anchor via <<.  Without the merge-aware fix the anchor would still
// carry root: true after the call, so the decoder would see two roots.
func TestSetExclusiveBool_MergeConsumerPromoted(t *testing.T) {
	p := writeFixture(t, anchorFixture)
	root, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if err := SetExclusiveBool(root, "agents", "coordinator-google", "root"); err != nil {
		t.Fatalf("SetExclusiveBool: %v", err)
	}
	if err := SaveFile(p, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	agents := decodeAgents(t, data)

	rootCount := 0
	for _, a := range agents {
		if a.Root {
			rootCount++
			if a.Name != "coordinator-google" {
				t.Errorf("unexpected root carrier: %q", a.Name)
			}
		}
	}
	if rootCount != 1 {
		t.Errorf("expected exactly 1 root; got %d; agents=%v", rootCount, agents)
	}
}

// TestSetExclusiveBool_AnchorPromoted re-promotes coordinator-opus, which is
// both the anchor AND the current root carrier.  The merge consumers must
// receive an explicit root: false override so they do not silently inherit
// root: true from the anchor after the save; without the fix the decoder
// would see three roots (anchor + two alias-inherited).
func TestSetExclusiveBool_AnchorPromoted(t *testing.T) {
	p := writeFixture(t, anchorFixture)
	root, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if err := SetExclusiveBool(root, "agents", "coordinator-opus", "root"); err != nil {
		t.Fatalf("SetExclusiveBool: %v", err)
	}
	if err := SaveFile(p, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// The on-disk file must carry at least one explicit root: false so that
	// the merge consumers cannot silently inherit root: true from the anchor.
	if !strings.Contains(string(data), "root: false") {
		t.Errorf("expected explicit root: false on merge consumers; output:\n%s", data)
	}

	agents := decodeAgents(t, data)
	rootCount := 0
	for _, a := range agents {
		if a.Root {
			rootCount++
			if a.Name != "coordinator-opus" {
				t.Errorf("unexpected root carrier: %q", a.Name)
			}
		}
	}
	if rootCount != 1 {
		t.Errorf("expected exactly 1 root; got %d; agents=%v", rootCount, agents)
	}
}

// multilineFixture simulates an agents.yaml that was previously saved as a
// double-quoted one-liner because some lines had trailing spaces — the exact
// form the bug leaves on disk.  The escaped \n sequences and the embedded
// trailing blanks are intentional.
const multilineFixture = "agents:\n  - name: writer\n    prompt: \"first line  \\nsecond line\\nthird line  \\n\"\n"

// TestSaveFile_MultilineTrailingSpaces_BecomesLiteralBlock loads a file that
// has a double-quoted multiline prompt with trailing spaces per line.
// SaveFile must trim the trailing whitespace and switch the node to LiteralStyle
// so the output is a `|` block scalar (no \n escapes).  A second Load+Save
// cycle must be byte-for-byte identical, proving the normalisation is stable.
func TestSaveFile_MultilineTrailingSpaces_BecomesLiteralBlock(t *testing.T) {
	p := writeFixture(t, multilineFixture)

	root, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := SaveFile(p, root); err != nil {
		t.Fatalf("first SaveFile: %v", err)
	}

	data1, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read after first save: %v", err)
	}
	s1 := string(data1)
	if !strings.Contains(s1, "prompt: |") {
		t.Errorf("expected literal block scalar (prompt: |); got:\n%s", s1)
	}
	// The double-quote escape form must be gone.
	if strings.Contains(s1, `\n`) {
		t.Errorf("escaped \\n still present — multiline was not unfolded; got:\n%s", s1)
	}

	// Second Load+Save must produce identical bytes: normalisation is idempotent.
	root2, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile for second save: %v", err)
	}
	if err := SaveFile(p, root2); err != nil {
		t.Fatalf("second SaveFile: %v", err)
	}
	data2, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read after second save: %v", err)
	}
	if !bytes.Equal(data1, data2) {
		t.Errorf("second save not idempotent:\n-- first save --\n%s\n-- second save --\n%s", data1, data2)
	}
}

// TestRoundTrip_NoExplicitMergeTag verifies that a file containing <<: *anchor
// round-trips through SaveFile WITHOUT the !!merge tag appearing in the
// output.  go-yaml's loader always sets Tag="!!merge" on merge-key scalars;
// our normaliser must clear it before marshalling so the on-disk form stays
// clean and human-readable.
func TestRoundTrip_NoExplicitMergeTag(t *testing.T) {
	p := writeFixture(t, anchorFixture)
	root, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := SaveFile(p, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "!!merge") {
		t.Errorf("!!merge tag appeared in output; got:\n%s", data)
	}
}
