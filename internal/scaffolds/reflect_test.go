// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package scaffolds

import (
	"reflect"
	"strings"
)

// yamlFieldNames returns the list of YAML field names declared on
// struct s via the `yaml:"..."` tag. Nested struct fields are
// included with their full dotted path so callers can assert
// "headers" plus "auth.kind" appear in a scaffold.
//
// The function is intentionally simple: it walks the struct once,
// follows nested struct types, and skips tags marked "-" or empty.
// We do NOT recurse into map / slice element types because the
// scaffold format documents collections by example, not by type
// (you write `headers: {...}` once, not one entry per allowed key).
//
// Used by the *_scaffold_test.go files to fail the build whenever
// someone adds a field to a config struct without mentioning it in
// the corresponding scaffold's comment block. That keeps user-
// facing documentation in lock-step with the on-disk schema.
func yamlFieldNames(s any) []string {
	t := reflect.TypeOf(s)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var out []string
	collectYAMLFields(t, "", &out)
	return out
}

// collectYAMLFields walks t and appends every yaml-tagged field to
// out. The prefix accumulates the dotted path so nested fields land
// as "parent.child".
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
		// yaml tag may carry options after a comma; we only want
		// the leading name.
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		*out = append(*out, path)

		// Recurse into nested struct types so users see
		// "auth.kind" as well as "auth".
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectYAMLFields(ft, path, out)
		}
	}
}

// scaffoldMentions reports whether scaffold contains every name in
// fields. Mentioning a field means the name appears at least once,
// uncommented or commented: the test is "did we acknowledge the
// field exists?", not "did we set a value?".
//
// We treat the field as mentioned when "name:" appears anywhere in
// the scaffold, ignoring leading whitespace and the optional comment
// marker '#'. Dotted paths (e.g. "auth.kind") are matched by their
// last component because nested fields don't carry the parent in
// YAML form.
func scaffoldMentions(scaffold string, fields []string) []string {
	var missing []string
	for _, full := range fields {
		// "auth.kind" -> "kind:" for the line-level search.
		parts := strings.Split(full, ".")
		leaf := parts[len(parts)-1]
		needle := leaf + ":"
		if !containsField(scaffold, needle) {
			missing = append(missing, full)
		}
	}
	return missing
}

// containsField finds needle as a YAML key in scaffold. We accept
// the key either at the start of a line (possibly indented), after
// a '#' comment marker (possibly indented), or with arbitrary
// indentation. Any false positive there ("description:" inside a
// comment vs as a real key) is acceptable: the test is for
// "documented at all", not "wired correctly".
func containsField(scaffold, needle string) bool {
	for _, line := range strings.Split(scaffold, "\n") {
		// Strip everything before the key: leading whitespace, an
		// optional comment marker, and the whitespace after it.
		stripped := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(stripped, "#") {
			stripped = strings.TrimPrefix(stripped, "#")
			stripped = strings.TrimLeft(stripped, " \t")
		}
		if strings.HasPrefix(stripped, needle) {
			return true
		}
	}
	return false
}
