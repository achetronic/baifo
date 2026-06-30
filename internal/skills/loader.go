// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package skills loads skill packages from .baifo/skills/{slug}/SKILL.md.
//
// Same model as Magec's decision #29: SKILL.md is markdown with a YAML
// frontmatter that declares at least `name` and `description`. The
// optional subdirectories references/, assets/ and scripts/ are exposed
// to the agent through the skill toolset's filesystem whitelist (built
// by the agent layer, not here).
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is the in-memory representation of a SKILL.md package.
type Skill struct {
	// Slug is the directory name under .baifo/skills/, used as the
	// canonical identifier (kebab-case).
	Slug string

	// Name is the human-readable name from the frontmatter. Required.
	Name string

	// Description is the natural-language description from the
	// frontmatter, used as input to the LLM when deciding whether to
	// activate the skill. Required.
	Description string

	// Extra holds any frontmatter key that is not Name or Description.
	// Values are preserved verbatim so future loaders can opt in to
	// fields like version, author, tags, etc. without forcing a schema
	// migration here.
	Extra map[string]string

	// Body is the markdown content of SKILL.md, with the frontmatter
	// block stripped. Loaded fresh from disk on every call to Load.
	Body string

	// Root is the absolute path of the skill directory. Tools and
	// helpers that need to resolve references/, assets/ or scripts/
	// should use this as their base.
	Root string
}

// Loader reads skills on demand from a base directory. There is no
// caching layer: every Load and List call goes to disk. This matches
// the spec in ARCHITECTURE.md and keeps hot-reload trivial — edit a
// SKILL.md and the next agent build picks the change up.
type Loader struct {
	skillsDir string
}

// NewLoader returns a Loader rooted at <configDir>/skills. The
// directory does not need to exist yet; it is created lazily when a
// skill is installed.
func NewLoader(configDir string) *Loader {
	return &Loader{skillsDir: filepath.Join(configDir, "skills")}
}

// Dir returns the absolute path of the skills directory.
func (l *Loader) Dir() string {
	return l.skillsDir
}

// List enumerates the slugs of every skill package under the loader's
// directory. The result is sorted lexicographically. Missing directory
// returns an empty slice and no error so callers do not need to
// special-case fresh installations.
func (l *Loader) List() ([]string, error) {
	entries, err := os.ReadDir(l.skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills directory: %w", err)
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(l.skillsDir, e.Name(), skillFileName)); err != nil {
			// Not a skill package — silently skip.
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// Load reads and parses the skill named slug. Returns ErrNotFound when
// the slug does not exist or has no SKILL.md.
func (l *Loader) Load(slug string) (*Skill, error) {
	if slug == "" {
		return nil, fmt.Errorf("slug is required")
	}
	root := filepath.Join(l.skillsDir, slug)
	path := filepath.Join(root, skillFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, slug)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", slug, err)
	}

	name := strings.TrimSpace(fm["name"])
	desc := strings.TrimSpace(fm["description"])
	if name == "" {
		return nil, fmt.Errorf("skill %s: missing required field 'name'", slug)
	}
	if desc == "" {
		return nil, fmt.Errorf("skill %s: missing required field 'description'", slug)
	}

	extra := make(map[string]string)
	for k, v := range fm {
		if k == "name" || k == "description" {
			continue
		}
		extra[k] = v
	}

	return &Skill{
		Slug:        slug,
		Name:        name,
		Description: desc,
		Extra:       extra,
		Body:        body,
		Root:        root,
	}, nil
}

// LoadAll loads every skill found by List. Useful for the /skills tab.
// Errors on individual skills are returned as a multi-line error.
func (l *Loader) LoadAll() ([]*Skill, error) {
	slugs, err := l.List()
	if err != nil {
		return nil, err
	}
	out := make([]*Skill, 0, len(slugs))
	var errs []string
	for _, s := range slugs {
		sk, err := l.Load(s)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		out = append(out, sk)
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("load skills:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return out, nil
}
