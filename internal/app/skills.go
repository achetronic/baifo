// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	adkskill "google.golang.org/adk/tool/skilltoolset/skill"

	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/skills"
	"github.com/achetronic/baifo/internal/skills/installer"
)

// SkillDetails implements facade.Facade. It walks the skills loader, parses
// each SKILL.md frontmatter once, and returns a compact summary.
// Errors on individual skills are swallowed so a single malformed
// SKILL.md does not blank the whole list — those skills just don't
// appear, and /skill edit can still load them via SkillContent.
func (a *App) SkillDetails() []facade.SkillDetail {
	if a.skills == nil {
		return nil
	}
	slugs, err := a.skills.List()
	if err != nil {
		return nil
	}
	ctx := context.Background()
	out := make([]facade.SkillDetail, 0, len(slugs))
	for _, slug := range slugs {
		fm, err := a.skills.FrontmatterOf(ctx, slug)
		if err != nil {
			// Show the slug at least so the user can edit + fix it.
			out = append(out, facade.SkillDetail{Name: slug, Description: "(invalid SKILL.md)"})
			continue
		}
		out = append(out, facade.SkillDetail{
			Name:        fm.Name,
			Description: firstLine(fm.Description),
		})
	}
	return out
}

// SkillContent implements facade.Facade. Reads <skillsDir>/<name>/SKILL.md
// straight from disk to preserve comments and ordering. Returns a
// fmt-wrapped error when the file is missing so the TUI can surface
// it without exposing the absolute path.
func (a *App) SkillContent(name string) (string, error) {
	if a.skills == nil {
		return "", fmt.Errorf("skills loader not initialised")
	}
	data, err := a.skills.ReadSkillMD(name)
	if err != nil {
		return "", fmt.Errorf("read SKILL.md for %q: %w", name, err)
	}
	return string(data), nil
}

// SkillScaffold implements facade.Facade by delegating to the package-level
// scaffold function. It's a method on App for facade.Facade conformance.
func (a *App) SkillScaffold(suggestedName string) string {
	return skills.Scaffold(suggestedName)
}

// UpsertSkill implements facade.Facade. Parses content via ADK's strict
// frontmatter validator, uses the parsed name as the directory slug,
// and writes the file. If the parsed name already exists, the file
// is overwritten in place (i.e. "edit" and "add" share the same
// path; the editor enforces "new vs existing" at the UX layer).
//
// Note: baifo does NOT rename a skill's directory if the user changes
// the name field in the frontmatter. That would silently move the
// skill out from under any agent template that references the old
// slug. Renames are a future explicit verb.
func (a *App) UpsertSkill(ctx context.Context, content string) error {
	if a.skills == nil {
		return fmt.Errorf("skills loader not initialised")
	}
	fm, _, err := adkskill.ParseBytes([]byte(content))
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	if err := a.skills.WriteSkillMD(fm.Name, []byte(content)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	// Skills are read on demand by the toolset (filesystem source
	// re-reads on every call), so no in-memory reload is needed.
	// We still emit a facade.ReloadEvent so any open Settings overlay
	// refreshes its rows.
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}
	return nil
}

// DeleteSkill implements facade.Facade. Defers the path sanity-check to the
// loader, which refuses anything that would escape the skills
// directory.
func (a *App) DeleteSkill(ctx context.Context, name string) error {
	if a.skills == nil {
		return fmt.Errorf("skills loader not initialised")
	}
	if err := a.skills.DeleteSkill(name); err != nil {
		return err
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}
	return nil
}

// InstallSkill implements facade.Facade. Delegates to the installer package,
// which downloads, validates and extracts the archive into the
// loader's skills directory atomically. A facade.ReloadEvent is emitted so
// any open overlay refreshes.
func (a *App) InstallSkill(ctx context.Context, sourceURL string) (string, error) {
	if a.skills == nil {
		return "", fmt.Errorf("skills loader not initialised")
	}
	name, err := installer.Install(ctx, sourceURL, a.skills.Dir())
	if err != nil {
		return "", err
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}
	return name, nil
}

// firstLine returns s up to the first newline. Descriptions can be
// multi-paragraph; the Settings row only has space for one line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
