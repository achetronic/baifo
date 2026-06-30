// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeSkill creates a SKILL.md inside <skillsDir>/<slug>/ with the given body.
func writeSkill(t *testing.T, skillsDir, slug, body string) {
	t.Helper()
	dir := filepath.Join(skillsDir, slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestListEmptyOnFreshInstall(t *testing.T) {
	loader := NewLoader(t.TempDir())
	slugs, err := loader.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(slugs) != 0 {
		t.Errorf("expected empty list, got %v", slugs)
	}
}

func TestLoadParsesValidSkill(t *testing.T) {
	cfgDir := t.TempDir()
	loader := NewLoader(cfgDir)
	writeSkill(t, loader.Dir(), "skill-creator", `---
name: skill-creator
description: |
  Create new skills and improve existing ones.
version: 1
---

# Body
This is the body of the skill.
`)

	sk, err := loader.Load("skill-creator")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if sk.Slug != "skill-creator" {
		t.Errorf("Slug: got %q", sk.Slug)
	}
	if sk.Name != "skill-creator" {
		t.Errorf("Name: got %q", sk.Name)
	}
	if sk.Description == "" {
		t.Errorf("Description should not be empty")
	}
	if sk.Extra["version"] != "1" {
		t.Errorf("Extra[version]: got %q, want %q", sk.Extra["version"], "1")
	}
	if sk.Body == "" {
		t.Errorf("Body should not be empty")
	}
}

func TestLoadRejectsMissingRequiredFields(t *testing.T) {
	cfgDir := t.TempDir()
	loader := NewLoader(cfgDir)
	writeSkill(t, loader.Dir(), "broken", `---
name: broken
---

body
`)

	if _, err := loader.Load("broken"); err == nil {
		t.Error("expected error for missing description, got nil")
	}
}

func TestLoadRejectsMissingFrontmatter(t *testing.T) {
	cfgDir := t.TempDir()
	loader := NewLoader(cfgDir)
	writeSkill(t, loader.Dir(), "no-fm", "just markdown\n")

	if _, err := loader.Load("no-fm"); err == nil {
		t.Error("expected error for missing frontmatter, got nil")
	}
}

func TestLoadReturnsNotFoundForUnknownSlug(t *testing.T) {
	loader := NewLoader(t.TempDir())
	_, err := loader.Load("does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestListIgnoresDirsWithoutSkillFile(t *testing.T) {
	cfgDir := t.TempDir()
	loader := NewLoader(cfgDir)
	writeSkill(t, loader.Dir(), "valid", `---
name: valid
description: ok
---
body
`)
	// Create a sibling directory with no SKILL.md
	if err := os.MkdirAll(filepath.Join(loader.Dir(), "bogus"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	slugs, err := loader.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(slugs) != 1 || slugs[0] != "valid" {
		t.Errorf("got %v, want [valid]", slugs)
	}
}

func TestLoadAllSurfacesIndividualErrors(t *testing.T) {
	cfgDir := t.TempDir()
	loader := NewLoader(cfgDir)
	writeSkill(t, loader.Dir(), "good", `---
name: good
description: works
---
body
`)
	writeSkill(t, loader.Dir(), "bad", `---
name: bad
---
body
`)

	skills, err := loader.LoadAll()
	if err == nil {
		t.Error("LoadAll: expected error from 'bad', got nil")
	}
	if len(skills) != 1 || skills[0].Slug != "good" {
		t.Errorf("expected only 'good' to load, got %v", skills)
	}
}
