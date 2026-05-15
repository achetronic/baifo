// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// Package installer downloads and extracts skill packages distributed
// as .zip or .tar.gz archives. The result must validate against ADK's
// SKILL.md frontmatter rules; otherwise the install is aborted and
// nothing lands in the user's skills directory.
//
// Why URL+archive only (no git): keeps the dependency surface to
// stdlib, avoids requiring `git` in PATH, and matches how most agent
// skill catalogues already distribute (GitHub release zips, tar.gz
// from a static host). Users who want to install from a git repo can
// reach for `git archive` and point us at the resulting tarball.
package installer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	adkskill "google.golang.org/adk/tool/skilltoolset/skill"
)

// httpClient is the package-level HTTP client. Timeout is generous
// because release archives on slower hosts can take a few seconds.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// maxArchiveSize is the cap (bytes) on the downloaded archive size.
// 50 MiB is comfortably above any reasonable skill but small enough
// that a malicious URL pointing at /dev/urandom can't fill the disk.
const maxArchiveSize int64 = 50 << 20

// maxFileSize caps any single file inside the archive. Same reasoning
// as maxArchiveSize: defence against zip-bomb style attacks.
const maxFileSize int64 = 10 << 20

// maxFiles caps the file count inside an archive.
const maxFiles = 5000

// Install fetches sourceURL, detects whether the payload is a .zip or
// .tar.gz, extracts it to a temp directory, validates that the result
// contains a SKILL.md with a valid frontmatter, and then atomically
// renames the temp directory into destBase/<skill-name>.
//
// The skill name is taken from the parsed frontmatter; the original
// archive's directory name is ignored. This way an archive that
// extracts to "my-skill-main/" (the common GitHub release pattern)
// lands as "my-skill/" on disk.
//
// Returns the installed skill name on success, or an error and leaves
// destBase untouched on any failure.
func Install(ctx context.Context, sourceURL, destBase string) (string, error) {
	kind, err := detectKind(sourceURL)
	if err != nil {
		return "", err
	}

	tmpRoot, err := os.MkdirTemp("", "baifo-skill-install-*")
	if err != nil {
		return "", fmt.Errorf("create tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpRoot)

	body, err := fetch(ctx, sourceURL)
	if err != nil {
		return "", err
	}
	defer body.Close()

	stagingDir := filepath.Join(tmpRoot, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	switch kind {
	case archiveZip:
		if err := extractZip(body, stagingDir); err != nil {
			return "", fmt.Errorf("extract zip: %w", err)
		}
	case archiveTarGz:
		if err := extractTarGz(body, stagingDir); err != nil {
			return "", fmt.Errorf("extract tar.gz: %w", err)
		}
	}

	skillRoot, err := findSkillRoot(stagingDir)
	if err != nil {
		return "", err
	}

	skillMD, err := os.ReadFile(filepath.Join(skillRoot, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("read SKILL.md: %w", err)
	}
	fm, _, err := adkskill.ParseBytes(skillMD)
	if err != nil {
		return "", fmt.Errorf("validate SKILL.md: %w", err)
	}

	finalDir := filepath.Join(destBase, fm.Name)
	if _, err := os.Stat(finalDir); err == nil {
		return "", fmt.Errorf("skill %q already installed; delete it first", fm.Name)
	}
	if err := os.MkdirAll(destBase, 0o755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}
	if err := os.Rename(skillRoot, finalDir); err != nil {
		return "", fmt.Errorf("move into place: %w", err)
	}
	return fm.Name, nil
}

// archiveKind enumerates what we know how to extract.
type archiveKind int

const (
	archiveZip archiveKind = iota + 1
	archiveTarGz
)

// detectKind picks the extraction strategy from the URL suffix.
func detectKind(rawURL string) (archiveKind, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return 0, fmt.Errorf("unsupported scheme %q (use http or https)", u.Scheme)
	}
	path := strings.ToLower(u.Path)
	switch {
	case strings.HasSuffix(path, ".zip"):
		return archiveZip, nil
	case strings.HasSuffix(path, ".tar.gz"), strings.HasSuffix(path, ".tgz"):
		return archiveTarGz, nil
	}
	return 0, fmt.Errorf("unsupported archive type (URL must end in .zip, .tar.gz or .tgz)")
}

// fetch issues a GET to sourceURL with the context and returns the
// response body. HTTP errors >= 400 are turned into Go errors so the
// caller doesn't have to switch on status codes. The body is capped
// at maxArchiveSize by the extractors themselves; here we just hand
// the connection over.
func fetch(ctx context.Context, sourceURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "baifo-skill-installer")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode >= 400 {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("failed to close response body after bad status", "status", resp.StatusCode, "error", closeErr)
		}
		return nil, fmt.Errorf("download: http %d", resp.StatusCode)
	}
	return resp.Body, nil
}
