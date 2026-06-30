// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// isolateHome makes HOME, XDG_CONFIG_HOME and BAIFO_HOME point at an
// empty temp dir, so the fallbacks in DiscoverDir never accidentally
// pick up the developer's real config during testing.
func isolateHome(t *testing.T) string {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(empty, "xdg"))
	t.Setenv("BAIFO_HOME", "")
	return empty
}

// cwdAt cds into dir for the duration of the test.
func cwdAt(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
}

func TestDiscoverDirHonoursFlag(t *testing.T) {
	isolateHome(t)
	target := filepath.Join(t.TempDir(), dirName)
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := DiscoverDir(target)
	if err != nil {
		t.Fatalf("DiscoverDir: %v", err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

func TestDiscoverDirFlagMissingFails(t *testing.T) {
	isolateHome(t)
	_, err := DiscoverDir("/definitely/not/a/real/path")
	if !errors.Is(err, ErrDirNotFound) {
		t.Errorf("got %v, want ErrDirNotFound", err)
	}
}

func TestDiscoverDirHonoursBAIFOHOME(t *testing.T) {
	isolateHome(t)
	target := filepath.Join(t.TempDir(), "custom")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("BAIFO_HOME", target)

	got, err := DiscoverDir("")
	if err != nil {
		t.Fatalf("DiscoverDir: %v", err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

func TestDiscoverDirWalksUpFromCwd(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	project := filepath.Join(root, "project")
	nested := filepath.Join(project, "src", "deep")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	baifoDir := filepath.Join(project, dirName)
	if err := os.Mkdir(baifoDir, 0o700); err != nil {
		t.Fatalf("mkdir .baifo: %v", err)
	}

	cwdAt(t, nested)

	got, err := DiscoverDir("")
	if err != nil {
		t.Fatalf("DiscoverDir: %v", err)
	}
	if got != baifoDir {
		t.Errorf("got %q, want %q", got, baifoDir)
	}
}

func TestDiscoverDirFallsBackToHome(t *testing.T) {
	home := isolateHome(t)
	cwdAt(t, t.TempDir())

	baifoDir := filepath.Join(home, dirName)
	if err := os.Mkdir(baifoDir, 0o700); err != nil {
		t.Fatalf("mkdir .baifo in HOME: %v", err)
	}

	got, err := DiscoverDir("")
	if err != nil {
		t.Fatalf("DiscoverDir: %v", err)
	}
	if got != baifoDir {
		t.Errorf("got %q, want %q", got, baifoDir)
	}
}

func TestDiscoverDirNoneFoundReturnsErrDirNotFound(t *testing.T) {
	isolateHome(t)
	cwdAt(t, t.TempDir())

	_, err := DiscoverDir("")
	if !errors.Is(err, ErrDirNotFound) {
		t.Errorf("got %v, want ErrDirNotFound", err)
	}
}
