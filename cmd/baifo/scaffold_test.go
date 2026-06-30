// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// TestScaffoldedConfigParses writes the first-run files exactly as the
// wizard does and loads them back through the real loaders, so a typo or
// bad indentation in the templates fails the build instead of only
// surfacing on a user's first launch.
func TestScaffoldedConfigParses(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldConfigDir(dir); err != nil {
		t.Fatalf("scaffoldConfigDir: %v", err)
	}

	if _, err := config.Load(filepath.Join(dir, config.FileName)); err != nil {
		t.Errorf("generated baifo.yaml does not load: %v", err)
	}
	if _, err := config.LoadAgents(filepath.Join(dir, config.AgentsFileName)); err != nil {
		t.Errorf("generated agents.yaml does not load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); err != nil {
		t.Errorf("data dir not created: %v", err)
	}
}

// TestDefaultBaifoYAMLCoversSchema is the guard that keeps the first-run
// baifo.yaml template in lock-step with the on-disk schema. It mirrors
// the per-resource scaffold coverage tests in internal/scaffolds: if a
// field is added to config.Config (or any nested struct) without being
// mentioned in defaultBaifoYAML, this test fails until the template
// documents it. Commented-out is fine: the goal is "the user can see
// the field exists", not "the template sets a value".
func TestDefaultBaifoYAMLCoversSchema(t *testing.T) {
	fields := yamlFieldNames(config.Config{})
	missing := templateMissingFields(defaultBaifoYAML, fields)
	if len(missing) > 0 {
		t.Errorf("defaultBaifoYAML missing fields: %s\nKnown fields: %s",
			strings.Join(missing, ", "), strings.Join(fields, ", "))
	}
}

// TestDefaultAgentsYAMLCoversSchema is the agents.yaml counterpart: the
// seeded root agent must mention every field config.AgentTemplate (and
// its nested structs) understands.
func TestDefaultAgentsYAMLCoversSchema(t *testing.T) {
	fields := yamlFieldNames(config.AgentTemplate{})
	missing := templateMissingFields(defaultAgentsYAML, fields)
	if len(missing) > 0 {
		t.Errorf("defaultAgentsYAML missing fields: %s\nKnown fields: %s",
			strings.Join(missing, ", "), strings.Join(fields, ", "))
	}
}

// yamlFieldNames returns every YAML field name declared on struct s via
// the `yaml:"..."` tag, recursing into nested struct types so callers
// get dotted paths like "auth.kind". Tags marked "-" or empty are
// skipped. Map/slice element types are NOT followed: the template
// documents collections by example, not one entry per element.
func yamlFieldNames(s any) []string {
	t := reflect.TypeOf(s)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var out []string
	collectYAMLFields(t, "", &out)
	return out
}

func collectYAMLFields(t reflect.Type, prefix string, out *[]string) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		*out = append(*out, path)

		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectYAMLFields(ft, path, out)
		}
	}
}

// templateMissingFields reports which of fields are NOT mentioned in the
// template. A field counts as mentioned when its leaf name appears as a
// YAML key on some line (uncommented, commented (# key:) or as a list
// item (- key:). Dotted paths are matched by their last component)
// because nested keys don't carry the parent in YAML form.
func templateMissingFields(template string, fields []string) []string {
	var missing []string
	for _, full := range fields {
		parts := strings.Split(full, ".")
		leaf := parts[len(parts)-1]
		if !mentionsKey(template, leaf+":") {
			missing = append(missing, full)
		}
	}
	return missing
}

// mentionsKey finds needle ("foo:") as a YAML key anywhere in template.
// It tolerates leading whitespace, an optional comment marker, and an
// optional list-item dash, so it matches "foo:", "# foo:" and
// "#   - foo:" alike. Any false positive (a key mentioned in prose that
// happens to look like "foo:") is acceptable: the test asks "documented
// at all?", not "wired correctly?".
func mentionsKey(template, needle string) bool {
	for _, line := range strings.Split(template, "\n") {
		stripped := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(stripped, "#") {
			stripped = strings.TrimLeft(strings.TrimPrefix(stripped, "#"), " \t")
		}
		stripped = strings.TrimLeft(strings.TrimPrefix(stripped, "- "), " \t")
		if strings.HasPrefix(stripped, needle) {
			return true
		}
	}
	return false
}
