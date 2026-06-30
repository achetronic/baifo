// SPDX-License-Identifier: Apache-2.0

package yamledit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// fixtureWithComments is the canonical input we use to prove that
// comment-preserving round-trip works. It mixes head comments, line
// comments and a foot comment on purpose.
const fixtureWithComments = `# Top-level config for baifo.
# Hand-edited by Alby on day X.

root:
  name: "test-root"   # the name shown in the header

# MCP servers available to the root agent.
mcps:
  - name: filesystem    # local FS sandbox
    type: builtin
    builtin: filesystem
  - name: github
    type: http
    endpoint: https://api.example.com/mcp

# End of config.
`

// writeFixture drops body into a fresh temp file and returns its path.
func writeFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "baifo.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

func TestLoadFile_MissingReturnsEmptyDoc(t *testing.T) {
	root, err := LoadFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if root == nil {
		t.Fatalf("expected non-nil root for missing file")
	}
	if names := ListMCPNames(root); len(names) != 0 {
		t.Fatalf("expected no MCPs in empty doc, got %v", names)
	}
}

func TestRoundTrip_PreservesCommentsAndUnrelatedKeys(t *testing.T) {
	path := writeFixture(t, fixtureWithComments)
	root, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := SaveFile(path, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	// The original comments must survive. yaml.v3 normalises some
	// whitespace and quoting, so we don't compare byte-for-byte; we
	// assert on the user-visible payload.
	want := []string{
		"# Top-level config for baifo.",
		"# Hand-edited by Alby on day X.",
		"# the name shown in the header",
		"# MCP servers available to the root agent.",
		"# local FS sandbox",
		"# End of config.",
		"root:",
		"name: filesystem",
		"name: github",
	}
	gotStr := string(got)
	for _, w := range want {
		if !strings.Contains(gotStr, w) {
			t.Errorf("round-trip lost %q\n--- output ---\n%s", w, gotStr)
		}
	}
}

func TestUpsertMCP_AppendsNewEntry(t *testing.T) {
	path := writeFixture(t, fixtureWithComments)
	root, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	entry := BuildMCPEntry(config.MCPEntry{
		Name:    "browse",
		Type:    "builtin",
		Builtin: "browse",
	})
	if err := UpsertMCP(root, "browse", entry); err != nil {
		t.Fatalf("UpsertMCP: %v", err)
	}
	if err := SaveFile(path, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "name: browse") {
		t.Errorf("new entry not present in output:\n%s", got)
	}
	// Existing entries must still be there.
	for _, n := range []string{"name: filesystem", "name: github"} {
		if !strings.Contains(string(got), n) {
			t.Errorf("existing entry %q vanished:\n%s", n, got)
		}
	}
	// And the comments around them too.
	for _, c := range []string{
		"# MCP servers available to the root agent.",
		"# local FS sandbox",
		"# End of config.",
	} {
		if !strings.Contains(string(got), c) {
			t.Errorf("comment %q lost after upsert:\n%s", c, got)
		}
	}
}

func TestUpsertMCP_ReplacesExistingByName(t *testing.T) {
	path := writeFixture(t, fixtureWithComments)
	root, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	// Replace the existing github entry with a new endpoint.
	entry := BuildMCPEntry(config.MCPEntry{
		Name:     "github",
		Type:     "http",
		Endpoint: "https://api.changed.example.com/mcp",
	})
	if err := UpsertMCP(root, "github", entry); err != nil {
		t.Fatalf("UpsertMCP: %v", err)
	}
	if err := SaveFile(path, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "api.example.com") {
		t.Errorf("old endpoint still present:\n%s", got)
	}
	if !strings.Contains(string(got), "api.changed.example.com") {
		t.Errorf("new endpoint missing:\n%s", got)
	}
	// We should still have exactly 2 mcp names.
	names := ListMCPNames(root)
	if len(names) != 2 || names[0] != "filesystem" || names[1] != "github" {
		t.Errorf("expected [filesystem github], got %v", names)
	}
}

func TestDeleteMCP_Removes(t *testing.T) {
	path := writeFixture(t, fixtureWithComments)
	root, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := DeleteMCP(root, "github"); err != nil {
		t.Fatalf("DeleteMCP: %v", err)
	}
	names := ListMCPNames(root)
	if len(names) != 1 || names[0] != "filesystem" {
		t.Errorf("expected [filesystem], got %v", names)
	}

	// Deleting a non-existent entry must surface ErrNotFound, not panic.
	if err := DeleteMCP(root, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown name, got %v", err)
	}
}

func TestBuildMCPEntry_HeadersAndAuthRender(t *testing.T) {
	path := writeFixture(t, "root:\n  name: x\n")
	root, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	entry := BuildMCPEntry(config.MCPEntry{
		Name:     "secured",
		Type:     "http",
		Endpoint: "https://mcp.example.com",
		Headers: map[string]string{
			"X-Tenant-ID": "acme",
			"X-Trace":     "${secret:TRACE_KEY}",
		},
		Auth: config.MCPAuth{
			Kind:            config.MCPAuthKindOAuth,
			ClientID:        "abc123",
			ClientSecretRef: "GITHUB_OAUTH_SECRET",
		},
	})
	if err := UpsertMCP(root, "secured", entry); err != nil {
		t.Fatalf("UpsertMCP: %v", err)
	}
	if err := SaveFile(path, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	gotStr := string(got)
	for _, w := range []string{
		"name: secured",
		"endpoint: https://mcp.example.com",
		"X-Tenant-ID: acme",
		"X-Trace: ${secret:TRACE_KEY}",
		"kind: oauth",
		"client_id: abc123",
		"client_secret_ref: GITHUB_OAUTH_SECRET",
	} {
		if !strings.Contains(gotStr, w) {
			t.Errorf("missing rendered field %q\n--- output ---\n%s", w, gotStr)
		}
	}
}

func TestBuildMCPEntry_OmitsAuthWhenEmpty(t *testing.T) {
	// kind=none + no client_id / no secret_ref → no auth block at all.
	entry := BuildMCPEntry(config.MCPEntry{
		Name:     "plain",
		Type:     "http",
		Endpoint: "https://mcp.example.com",
	})
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if entry.Content[i].Value == "auth" {
			t.Fatalf("auth block should be omitted when empty")
		}
	}
}

func TestUpsertMCP_CreatesMCPsKeyIfMissing(t *testing.T) {
	path := writeFixture(t, "root:\n  name: x\n")
	root, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	entry := BuildMCPEntry(config.MCPEntry{
		Name: "filesystem", Type: "builtin", Builtin: "filesystem",
	})
	if err := UpsertMCP(root, "filesystem", entry); err != nil {
		t.Fatalf("UpsertMCP: %v", err)
	}
	if err := SaveFile(path, root); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "mcps:") {
		t.Errorf("mcps key not created:\n%s", got)
	}
	if !strings.Contains(string(got), "name: filesystem") {
		t.Errorf("entry not appended:\n%s", got)
	}
}
