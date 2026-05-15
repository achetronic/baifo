// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZip writes every file in the archive to dest. Defends
// against zip-slip (entries whose paths escape dest via "..") and
// caps the number/size of entries. Symlinks are silently dropped:
// skills don't need them and they widen the attack surface.
func extractZip(r io.Reader, dest string) error {
	// archive/zip needs a ReaderAt, so we spool to a temp file first.
	tmp, err := os.CreateTemp("", "baifo-skill-zip-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	size, err := io.Copy(tmp, r)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	if size > maxArchiveSize {
		return fmt.Errorf("archive exceeds %d bytes", maxArchiveSize)
	}

	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	if len(zr.File) > maxFiles {
		return fmt.Errorf("archive has too many entries (%d > %d)", len(zr.File), maxFiles)
	}

	for _, f := range zr.File {
		if err := extractZipEntry(f, dest); err != nil {
			return err
		}
	}
	return nil
}

// extractZipEntry materialises one file/dir from the archive.
func extractZipEntry(f *zip.File, dest string) error {
	target, err := safeJoin(dest, f.Name)
	if err != nil {
		return err
	}
	mode := f.Mode()
	if mode&os.ModeSymlink != 0 {
		// Silently skip symlinks for safety.
		return nil
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return fmt.Errorf("create %q: %w", target, err)
	}
	defer w.Close()
	if _, err := io.Copy(w, io.LimitReader(rc, maxFileSize+1)); err != nil {
		return fmt.Errorf("copy %q: %w", target, err)
	}
	return nil
}

// extractTarGz writes every file in the gzipped tar to dest. Same
// safety posture as extractZip: zip-slip guard, symlinks dropped,
// caps on count and size.
func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar entry: %w", err)
		}
		count++
		if count > maxFiles {
			return fmt.Errorf("archive has too many entries (>%d)", maxFiles)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent: %w", err)
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %q: %w", target, err)
			}
			n, err := io.Copy(w, io.LimitReader(tr, maxFileSize+1))
			if closeErr := w.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close %q: %w", target, closeErr)
			}
			if err != nil {
				return fmt.Errorf("copy %q: %w", target, err)
			}
			if n > maxFileSize {
				return fmt.Errorf("file %q exceeds %d bytes", target, maxFileSize)
			}
		default:
			// Symlinks, hardlinks, devices, fifos: skip silently.
		}
	}
}

// safeJoin resolves base + relPath, defending against zip-slip
// payloads (entries whose name contains ".." or starts with "/"). The
// resulting path is guaranteed to be within base.
func safeJoin(base, relPath string) (string, error) {
	// Reject leading slash and any '..' segment outright instead of
	// trying to normalise them away. filepath.Clean would happily turn
	// "../evil" into "evil" because it treats the path as starting
	// from root — that's exactly the trap that zip-slip exploits.
	if strings.HasPrefix(relPath, "/") {
		return "", fmt.Errorf("archive entry %q has absolute path", relPath)
	}
	normalised := strings.ReplaceAll(relPath, "\\", "/")
	for _, seg := range strings.Split(normalised, "/") {
		if seg == ".." {
			return "", fmt.Errorf("archive entry %q escapes destination", relPath)
		}
	}
	clean := filepath.Clean(normalised)
	if clean == "." {
		return base, nil
	}
	full := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", relPath, err)
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive entry %q escapes destination", relPath)
	}
	return full, nil
}

// findSkillRoot locates the directory containing SKILL.md. We allow:
//   - SKILL.md directly in stagingDir
//   - SKILL.md in a single immediate sub-directory (GitHub release
//     pattern: "repo-main/SKILL.md")
//
// Anything else (multiple subdirs, deeper nesting, no SKILL.md)
// returns an error.
func findSkillRoot(stagingDir string) (string, error) {
	if fileExists(filepath.Join(stagingDir, "SKILL.md")) {
		return stagingDir, nil
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return "", fmt.Errorf("read staging: %w", err)
	}
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	if len(subdirs) == 1 {
		candidate := filepath.Join(stagingDir, subdirs[0])
		if fileExists(filepath.Join(candidate, "SKILL.md")) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no SKILL.md found at archive root or single sub-directory")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
