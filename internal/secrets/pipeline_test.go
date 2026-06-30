// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"strings"
	"testing"
)

func newStoreWithSecrets(t *testing.T, kv map[string]string) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir, "test-passphrase-for-pipeline")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for k, v := range kv {
		if err := s.Set(k, v, ""); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}
	return s
}

func TestExpandReplacesAllowedPlaceholder(t *testing.T) {
	s := newStoreWithSecrets(t, map[string]string{
		"github_token": "ghp_xyz",
	})

	args := map[string]any{
		"headers": map[string]any{
			"Authorization": "Bearer ${secret:github_token}",
		},
	}
	out, _, err := Expand(context.Background(), s, AllowAll{}, args)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	got := out.(map[string]any)["headers"].(map[string]any)["Authorization"].(string)
	if got != "Bearer ghp_xyz" {
		t.Errorf("Expand: got %q, want %q", got, "Bearer ghp_xyz")
	}
}

func TestExpandLeavesDisallowedPlaceholderInPlace(t *testing.T) {
	s := newStoreWithSecrets(t, map[string]string{
		"github_token": "ghp_xyz",
		"private":      "should-not-leak",
	})

	args := map[string]any{
		"a": "${secret:github_token}",
		"b": "${secret:private}",
	}
	out, _, err := Expand(context.Background(), s, NewAllowList([]string{"github_token"}), args)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	m := out.(map[string]any)
	if m["a"] != "ghp_xyz" {
		t.Errorf("allowed: got %q, want %q", m["a"], "ghp_xyz")
	}
	if m["b"] != "${secret:private}" {
		t.Errorf("disallowed: got %q, want %q", m["b"], "${secret:private}")
	}
}

func TestExpandLeavesUnknownPlaceholderInPlace(t *testing.T) {
	s := newStoreWithSecrets(t, nil)

	out, _, err := Expand(context.Background(), s, AllowAll{}, "Bearer ${secret:does_not_exist}")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if out != "Bearer ${secret:does_not_exist}" {
		t.Errorf("unknown: got %q, want placeholder preserved", out)
	}
}

func TestRedactScrubsExpandedValues(t *testing.T) {
	s := newStoreWithSecrets(t, map[string]string{
		"github_token": "ghp_super_distinctive",
	})

	args := map[string]any{
		"h": "Bearer ${secret:github_token}",
	}
	_, ctx, err := Expand(context.Background(), s, AllowAll{}, args)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	result := map[string]any{
		"echoed": "request was: Bearer ghp_super_distinctive",
	}
	redacted, err := Redact(ctx, result)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}

	got := redacted.(map[string]any)["echoed"].(string)
	if !strings.Contains(got, "${secret:github_token}") {
		t.Errorf("Redact: got %q, want placeholder restored", got)
	}
	if strings.Contains(got, "ghp_super_distinctive") {
		t.Errorf("Redact: raw secret leaked: %q", got)
	}
}

func TestRedactNoOpWhenNothingExpanded(t *testing.T) {
	ctx := context.Background()
	out, err := Redact(ctx, "hello world")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if out != "hello world" {
		t.Errorf("Redact (empty): got %q, want unchanged", out)
	}
}

func TestExpandWalksNestedListsAndMaps(t *testing.T) {
	s := newStoreWithSecrets(t, map[string]string{
		"k": "VALUE",
	})

	args := map[string]any{
		"list": []any{
			"a",
			"${secret:k}",
			map[string]any{
				"inner": "${secret:k}",
			},
		},
	}
	out, _, err := Expand(context.Background(), s, AllowAll{}, args)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	list := out.(map[string]any)["list"].([]any)
	if list[1] != "VALUE" {
		t.Errorf("list[1]: got %q, want %q", list[1], "VALUE")
	}
	inner := list[2].(map[string]any)["inner"]
	if inner != "VALUE" {
		t.Errorf("nested map: got %q, want %q", inner, "VALUE")
	}
}

// TestExpandWithEmptyAllowlistRefusesEverything is the end-to-end
// regression for the new least-privilege contract: a sub-agent
// built with an empty allowed_secrets slice must not be able to
// dereference any secret, not even one whose name it knows.
// Pinned because AllowerFor used to map "[]" to AllowAll.
func TestExpandWithEmptyAllowlistRefusesEverything(t *testing.T) {
	s := newStoreWithSecrets(t, map[string]string{
		"github_token": "ghp_xyz",
	})
	allower := AllowerFor([]string{})
	args := map[string]any{"a": "Bearer ${secret:github_token}"}
	out, _, err := Expand(context.Background(), s, allower, args)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	m := out.(map[string]any)
	if m["a"] != "Bearer ${secret:github_token}" {
		t.Errorf("placeholder should survive: got %q", m["a"])
	}
}

// TestAllowerForEmptyDeniesAll is the regression for the
// "[] = least-privilege" contract documented in SECRETS.md: any
// agent built from an explicit allowlist that happens to be empty
// (or nil) must reject every secret. The root agent bypasses
// AllowerFor entirely and is wired to AllowAll in the builder; this
// function is only used for sub-agents.
func TestAllowerForEmptyDeniesAll(t *testing.T) {
	a := AllowerFor(nil)
	if a.Allowed("anything") {
		t.Error("empty allowlist (nil) should deny all")
	}
	a = AllowerFor([]string{})
	if a.Allowed("anything") {
		t.Error("empty allowlist ([]) should deny all")
	}
}

func TestAllowerForListRestricts(t *testing.T) {
	a := AllowerFor([]string{"yes"})
	if !a.Allowed("yes") {
		t.Error("listed name should be allowed")
	}
	if a.Allowed("no") {
		t.Error("unlisted name should be denied")
	}
}

// TestAllowAllPermitsEverything documents the root-only allower.
func TestAllowAllPermitsEverything(t *testing.T) {
	if !(AllowAll{}).Allowed("anything") {
		t.Error("AllowAll should permit every name")
	}
}

// TestAllowNonePermitsNothing documents the explicit deny allower
// used as a defensive default and as the "[] from a sub-agent" gate.
func TestAllowNonePermitsNothing(t *testing.T) {
	if (AllowNone{}).Allowed("anything") {
		t.Error("AllowNone should deny every name")
	}
}
