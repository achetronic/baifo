// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package skills

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// skillFileName is the canonical filename of a skill manifest.
const skillFileName = "SKILL.md"

// frontmatterDelim is the line that opens and closes the YAML
// frontmatter block at the top of SKILL.md.
const frontmatterDelim = "---"

// ErrNotFound is returned when a skill slug does not exist on disk.
var ErrNotFound = errors.New("skill not found")

// parseFrontmatter splits a SKILL.md file into its frontmatter and body.
//
// The frontmatter is the YAML block between two `---` lines at the very
// top of the file. Missing frontmatter is a parse error; we don't
// silently allow header-less SKILL.md files because the loader requires
// `name` and `description`.
//
// The parser is tolerant: extra keys are returned verbatim in the map.
// Values are coerced to strings using yaml.Marshal so nested structures
// (lists, maps) survive round-trips even if no current caller uses them.
func parseFrontmatter(data []byte) (map[string]string, string, error) {
	lines := bytes.SplitN(data, []byte("\n"), -1)
	if len(lines) == 0 || strings.TrimSpace(string(lines[0])) != frontmatterDelim {
		return nil, "", fmt.Errorf("SKILL.md must start with a '%s' frontmatter delimiter", frontmatterDelim)
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(string(lines[i])) == frontmatterDelim {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, "", fmt.Errorf("SKILL.md frontmatter is not closed by a '%s' line", frontmatterDelim)
	}

	rawYAML := bytes.Join(lines[1:closeIdx], []byte("\n"))
	body := string(bytes.Join(lines[closeIdx+1:], []byte("\n")))
	body = strings.TrimLeft(body, "\n")

	var raw map[string]any
	if err := yaml.Unmarshal(rawYAML, &raw); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}

	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = stringify(v)
	}
	return out, body, nil
}

// stringify coerces a yaml-decoded value into a string. Scalars become
// their canonical text form; lists and maps are re-emitted as compact
// YAML so the original information is preserved without binding the
// loader to a specific schema.
func stringify(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int, int64, float64:
		return fmt.Sprintf("%v", val)
	default:
		b, err := yaml.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return strings.TrimRight(string(b), "\n")
	}
}
