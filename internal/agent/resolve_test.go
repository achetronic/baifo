// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"reflect"
	"sort"
	"testing"
)

func TestResolveRootMCPs_EmptyMeansAll(t *testing.T) {
	got := ResolveMCPs(nil, []string{"filesystem", "browse", "github"})
	sort.Strings(got)
	want := []string{"browse", "filesystem", "github"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty config: got %v, want %v", got, want)
	}
}

func TestResolveRootMCPs_EmptyConfigEmptyRegistry(t *testing.T) {
	got := ResolveMCPs(nil, nil)
	if got != nil {
		t.Errorf("empty everywhere: got %v, want nil", got)
	}
}

func TestResolveRootMCPs_ExplicitListIsRespected(t *testing.T) {
	got := ResolveMCPs([]string{"filesystem"}, []string{"filesystem", "browse", "github"})
	if !reflect.DeepEqual(got, []string{"filesystem"}) {
		t.Errorf("explicit list: got %v, want [filesystem]", got)
	}
}

func TestResolveRootMCPs_DoesNotAliasInput(t *testing.T) {
	registry := []string{"a", "b"}
	got := ResolveMCPs(nil, registry)
	got[0] = "mutated"
	if registry[0] == "mutated" {
		t.Errorf("ResolveMCPs aliased its input slice")
	}
}

func TestResolveRootSkills_EmptyMeansAll(t *testing.T) {
	got := ResolveSkills(nil, []string{"go-tests", "tf-review"})
	sort.Strings(got)
	want := []string{"go-tests", "tf-review"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty config: got %v, want %v", got, want)
	}
}

func TestResolveRootSkills_ExplicitListIsRespected(t *testing.T) {
	got := ResolveSkills([]string{"go-tests"}, []string{"go-tests", "tf-review"})
	if !reflect.DeepEqual(got, []string{"go-tests"}) {
		t.Errorf("explicit list: got %v, want [go-tests]", got)
	}
}
