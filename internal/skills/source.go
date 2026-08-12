// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	adkskill "google.golang.org/adk/v2/tool/skilltoolset/skill"
)

// Source returns the ADK skill.Source bound to the loader's skills
// directory. Created lazily on the first call; the underlying
// implementation is the fs.FS-backed variant ADK ships, which gives
// us conformance with the agentskills.io spec for free (frontmatter
// validation, name rules, etc).
//
// The Source is the thing baifo hands to skilltoolset.New so the root
// agent can list, load and read skill resources at runtime. Callers
// that only need to enumerate slugs from the TUI keep using
// Loader.List for backwards compatibility.
//
// Note: the directory may not exist yet on a fresh install. We
// create it lazily here so the Source's fs.FS has something to walk.
func (l *Loader) Source() (adkskill.Source, error) {
	if err := os.MkdirAll(l.skillsDir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure skills dir: %w", err)
	}
	return adkskill.NewFileSystemSource(os.DirFS(l.skillsDir)), nil
}

// FrontmatterOf reads the named skill's SKILL.md and returns the
// parsed ADK frontmatter struct. Returns an error wrapping
// skill.ErrSkillNotFound when the directory or its SKILL.md is
// missing. Used by the Facade methods that surface skill metadata
// to the TUI (Settings overlay, /skill list rich rendering).
func (l *Loader) FrontmatterOf(ctx context.Context, name string) (*adkskill.Frontmatter, error) {
	src, err := l.Source()
	if err != nil {
		return nil, err
	}
	fm, err := src.LoadFrontmatter(ctx, name)
	if err != nil {
		return nil, err
	}
	return fm, nil
}

// ReadSkillMD returns the raw bytes of <skillsDir>/<name>/SKILL.md.
// Used by the Facade to seed the embedded editor with the on-disk
// content, comments and whitespace included. Returns os.IsNotExist
// for absent files so callers can render their own error message.
func (l *Loader) ReadSkillMD(name string) ([]byte, error) {
	path := filepath.Join(l.skillsDir, name, skillFileName)
	return os.ReadFile(path)
}

// WriteSkillMD writes content to <skillsDir>/<name>/SKILL.md,
// creating the directory if needed. Atomic via tmp + rename to
// avoid leaving a half-written file on a crash. Callers are
// expected to have validated content via skill.ParseBytes first.
func (l *Loader) WriteSkillMD(name string, content []byte) error {
	dir := filepath.Join(l.skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	final := filepath.Join(dir, skillFileName)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// DeleteSkill removes <skillsDir>/<name> recursively. Refuses to
// touch anything outside skillsDir so a maliciously-named slug
// cannot escape the sandbox. Returns ErrNotFound when the
// directory is absent.
func (l *Loader) DeleteSkill(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid skill name: %q", name)
	}
	dir := filepath.Join(l.skillsDir, name)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs: %w", err)
	}
	skillsAbs, err := filepath.Abs(l.skillsDir)
	if err != nil {
		return fmt.Errorf("abs skillsDir: %w", err)
	}
	// Defence in depth: the resolved path must sit inside skillsDir.
	rel, err := filepath.Rel(skillsAbs, abs)
	if err != nil || rel == ".." || rel == "." || filepath.IsAbs(rel) {
		return fmt.Errorf("skill path escapes skills directory")
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return err
	}
	return os.RemoveAll(abs)
}

// Scaffold returns the SKILL.md content baifo drops into the editor
// for /skill add. The body is short on purpose: enough to remind the
// user where to put what, no padding. Validation is the same one
// ADK applies on save, so the scaffold is guaranteed to pass through
// untouched if the user just types Ctrl+S.
func Scaffold(suggestedName string) string {
	name := suggestedName
	if name == "" {
		name = "my-skill"
	}
	return `---
name: ` + name + `
description: Briefly describe when the agent should use this skill. Be specific: list the situations and inputs that trigger it. The description is what the model reads to decide whether to load the skill.
---

# ` + name + `

Replace this body with the actual instructions the agent should follow when this skill is active.

Optional sub-directories you can add next to this SKILL.md:

- ` + "`references/`" + ` — extra docs or examples the agent may load on demand.
- ` + "`assets/`" + ` — templates and other resources.
- ` + "`scripts/`" + ` — executable helpers (not yet wired into baifo).
`
}

// ValidateSkillMD parses content as a SKILL.md document and returns
// the validation errors gathered by ADK. Returns an empty slice on
// success. The editor uses this as its Ctrl+S validator so users
// see schema errors before the file ever touches disk.
func ValidateSkillMD(content []byte) []error {
	_, _, err := adkskill.ParseBytes(content)
	if err != nil {
		return []error{err}
	}
	return nil
}

// LoadResourceCloser is a tiny convenience for callers (notably the
// future "show resource" path in the TUI) that need to read a file
// inside a skill via the Source interface.
func (l *Loader) LoadResourceCloser(ctx context.Context, name, resourcePath string) (io.ReadCloser, error) {
	src, err := l.Source()
	if err != nil {
		return nil, err
	}
	return src.LoadResource(ctx, name, resourcePath)
}

// Forward sentinel errors from ADK so callers can use errors.Is
// against them without importing the ADK package directly.
var (
	ErrInvalidSkillName    = adkskill.ErrInvalidSkillName
	ErrInvalidFrontmatter  = adkskill.ErrInvalidFrontmatter
	ErrSkillNotFound       = adkskill.ErrSkillNotFound
	ErrDuplicateSkill      = adkskill.ErrDuplicateSkill
	ErrInvalidResourcePath = adkskill.ErrInvalidResourcePath
	ErrResourceNotFound    = adkskill.ErrResourceNotFound
)

// IsADKNotFound reports whether err signals a missing skill on the
// ADK side. Useful for the Facade which has its own ErrNotFound but
// needs to bridge both.
func IsADKNotFound(err error) bool { return errors.Is(err, adkskill.ErrSkillNotFound) }
