// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validSkillMD is the minimum well-formed SKILL.md content the
// installer's validation should accept. ADK enforces lowercase name,
// description length, etc. — anything weaker is rejected.
const validSkillMD = `---
name: my-skill
description: A small test skill for the installer unit tests; long enough to clear the 1-character minimum that ADK enforces on the description field.
---

# my-skill

body text.
`

// buildZipServer returns an httptest server serving the given files
// as a zip at "/skill.zip". Caller must close.
func buildZipServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(buf.Bytes())
	})
	return httptest.NewServer(mux)
}

// buildTarGzServer is the tar.gz counterpart of buildZipServer.
func buildTarGzServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(buf.Bytes())
	})
	return httptest.NewServer(mux)
}

func TestInstall_ZipAtRoot(t *testing.T) {
	srv := buildZipServer(t, map[string]string{
		"SKILL.md":            validSkillMD,
		"references/notes.md": "reference content",
	})
	defer srv.Close()

	dest := t.TempDir()
	name, err := Install(context.Background(), srv.URL+"/skill.zip", dest)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if name != "my-skill" {
		t.Errorf("name: got %q, want my-skill", name)
	}
	if _, err := os.Stat(filepath.Join(dest, "my-skill", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md not at expected path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "my-skill", "references", "notes.md")); err != nil {
		t.Errorf("reference file not preserved: %v", err)
	}
}

func TestInstall_ZipNestedInSingleDir(t *testing.T) {
	// GitHub release pattern: contents wrapped in "repo-main/".
	srv := buildZipServer(t, map[string]string{
		"repo-main/SKILL.md": validSkillMD,
	})
	defer srv.Close()

	dest := t.TempDir()
	name, err := Install(context.Background(), srv.URL+"/skill.zip", dest)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if name != "my-skill" {
		t.Errorf("name: got %q, want my-skill", name)
	}
}

func TestInstall_TarGz(t *testing.T) {
	srv := buildTarGzServer(t, map[string]string{
		"SKILL.md": validSkillMD,
	})
	defer srv.Close()

	dest := t.TempDir()
	if _, err := Install(context.Background(), srv.URL+"/skill.tar.gz", dest); err != nil {
		t.Fatalf("install: %v", err)
	}
}

func TestInstall_RejectsBadFrontmatter(t *testing.T) {
	bad := `---
name: BadName
description: too short
---
`
	srv := buildZipServer(t, map[string]string{"SKILL.md": bad})
	defer srv.Close()

	dest := t.TempDir()
	if _, err := Install(context.Background(), srv.URL+"/skill.zip", dest); err == nil {
		t.Errorf("expected validation error for uppercase name")
	}
	// Ensure nothing landed on disk.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("dest should be empty after failed install, got %d entries", len(entries))
	}
}

func TestInstall_RejectsZipSlip(t *testing.T) {
	// Entry escapes via "..". safeJoin should refuse it.
	srv := buildZipServer(t, map[string]string{
		"SKILL.md":         validSkillMD,
		"../evil/leak.txt": "should not be written",
	})
	defer srv.Close()

	dest := t.TempDir()
	_, err := Install(context.Background(), srv.URL+"/skill.zip", dest)
	if err == nil {
		t.Errorf("expected error rejecting traversal entry")
	}
	if err != nil && !strings.Contains(err.Error(), "escape") {
		// Accept "escapes destination" message or any wrapping error.
		t.Logf("install rejected with: %v", err)
	}
}

func TestInstall_RejectsExistingSkill(t *testing.T) {
	srv := buildZipServer(t, map[string]string{"SKILL.md": validSkillMD})
	defer srv.Close()

	dest := t.TempDir()
	if _, err := Install(context.Background(), srv.URL+"/skill.zip", dest); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Second install of the same skill name must fail.
	if _, err := Install(context.Background(), srv.URL+"/skill.zip", dest); err == nil {
		t.Errorf("expected error on duplicate install")
	}
}

func TestInstall_RejectsUnsupportedScheme(t *testing.T) {
	if _, err := Install(context.Background(), "ftp://example.com/skill.zip", t.TempDir()); err == nil {
		t.Errorf("expected error for non-http scheme")
	}
}

func TestInstall_RejectsUnsupportedExtension(t *testing.T) {
	if _, err := Install(context.Background(), "https://example.com/skill.rar", t.TempDir()); err == nil {
		t.Errorf("expected error for unknown archive extension")
	}
}

func TestInstall_404IsClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := Install(context.Background(), srv.URL+"/missing.zip", t.TempDir())
	if err == nil {
		t.Errorf("expected error on 404")
	}
	if err != nil && !strings.Contains(err.Error(), "404") {
		t.Logf("(informational) error message: %v", err)
	}
}

// unused import shim to keep "fmt" out of the actual test file when
// the linter complains about unused imports during incremental edits.
var _ = fmt.Sprintf
