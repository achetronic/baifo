// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// dirName is the conventional name of baifo's per-project config directory.
const dirName = ".baifo"

// envHome is the environment variable that overrides every fallback.
const envHome = "BAIFO_HOME"

// ErrDirNotFound is returned by DiscoverDir when no .baifo/ directory can
// be located using the resolution rules. Callers should treat it as the
// trigger for first-run initialisation.
var ErrDirNotFound = errors.New(".baifo directory not found")

// DiscoverDir locates the .baifo/ configuration directory according to
// the priority order documented in DECISIONS.md #1:
//
//  1. flagDir (--config-dir), if non-empty
//  2. $BAIFO_HOME
//  3. $PWD/.baifo/, walking up the tree
//  4. $XDG_CONFIG_HOME/baifo/
//  5. $HOME/.baifo/
//
// The first hit wins. Returns ErrDirNotFound when nothing matches.
func DiscoverDir(flagDir string) (string, error) {
	for _, candidate := range candidateDirs(flagDir) {
		if candidate.path == "" {
			continue
		}
		if candidate.required {
			abs, err := requireExistingDir(candidate.path)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
		if abs, ok := existingDir(candidate.path); ok {
			return abs, nil
		}
	}
	return "", ErrDirNotFound
}

// DefaultDir returns the path of the .baifo directory that the first-run
// wizard should create when no config is found anywhere.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, dirName), nil
}

// candidate represents one entry in the resolution order. `required`
// means the path was given explicitly (flag / env): if it doesn't
// exist we want to fail loudly rather than silently fall through.
type candidate struct {
	path     string
	required bool
}

// candidateDirs builds the resolution order. Each entry may be empty —
// the caller skips empties — which simplifies optional lookups (env
// vars, fallbacks) without conditional branching.
func candidateDirs(flagDir string) []candidate {
	out := []candidate{
		{path: flagDir, required: true},
		{path: os.Getenv(envHome), required: true},
	}

	if walked, ok := walkUpFromCwd(); ok {
		out = append(out, candidate{path: walked})
	}
	out = append(out, candidate{path: xdgConfigDir()})
	out = append(out, candidate{path: homeFallback()})
	return out
}

// walkUpFromCwd searches from $PWD upwards for a child named ".baifo".
// Returns the absolute path of the first match and true, or "" and
// false when none is found (or when getwd fails).
func walkUpFromCwd() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for dir := cwd; ; {
		target := filepath.Join(dir, dirName)
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			return target, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// xdgConfigDir returns $XDG_CONFIG_HOME/baifo, falling back to
// $HOME/.config/baifo when XDG_CONFIG_HOME is unset. Returns "" if even
// the home directory cannot be resolved.
func xdgConfigDir() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "baifo")
}

// homeFallback returns $HOME/.baifo, or "" when the home dir cannot be
// resolved.
func homeFallback() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, dirName)
}

// existingDir returns the absolute path of p and true if p is an
// existing directory, or "" and false otherwise.
func existingDir(p string) (string, bool) {
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	return abs, true
}

// requireExistingDir is existingDir but returns a descriptive error
// instead of (false). Used for paths the user provided explicitly.
func requireExistingDir(p string) (string, error) {
	abs, ok := existingDir(p)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrDirNotFound, p)
	}
	return abs, nil
}
