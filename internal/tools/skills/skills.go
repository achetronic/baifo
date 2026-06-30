// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package skills wraps ADK's skilltoolset behind the same
// `Tools.ADKTools()` shape every other baifo-owned toolset uses
// (spawn, todos, meta, memory). Same motivation: the composition
// root asks every tool source the same question.
//
// The actual tools (list_skills, load_skill, load_skill_resource)
// come straight from google.golang.org/adk/tool/skilltoolset. We
// don't reimplement them. We do reach for the skill.Source
// implementation baifo owns in internal/skills.Loader.
package skills

import (
	"context"

	"google.golang.org/adk/tool"
	skilltoolset "google.golang.org/adk/tool/skilltoolset"
	"google.golang.org/adk/tool/skilltoolset/skill"
)

// Tools wraps a skill.Source — the ADK abstraction over a tree of
// SKILL.md files — with the baifo toolset contract. The Source is
// provided by internal/skills.Loader.Source() at composition time.
type Tools struct {
	Source skill.Source
}

// New is the canonical constructor. Source comes from
// internal/skills.Loader.Source(); nil disables the toolset
// silently to match pre-existing boot semantics.
func New(src skill.Source) *Tools {
	return &Tools{Source: src}
}

// ADKTools returns list_skills, load_skill, load_skill_resource.
// Returns nil (not an error) when Source is nil so a missing
// .baifo/skills/ directory cannot break the boot.
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	if t == nil || t.Source == nil {
		return nil, nil
	}
	ts, err := skilltoolset.New(context.Background(), skilltoolset.Config{Source: t.Source})
	if err != nil {
		return nil, err
	}
	return ts.Tools(nil)
}
