// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package version is the single source of truth for the
// baifo binary's build metadata: semantic version, commit SHA
// and build timestamp.
//
// The three fields are stamped at link time by goreleaser /
// the Makefile via -ldflags:
//
//	-X github.com/achetronic/baifo/internal/version.tag=v0.4.1
//	-X github.com/achetronic/baifo/internal/version.commit=ab12cd3
//	-X github.com/achetronic/baifo/internal/version.date=2026-05-22T15:14:11Z
//
// Anything in the codebase that needs to brand outgoing
// requests, surface "what version am I running", or persist a
// versioned record should import from here. Defining the
// variables once means renaming or restamping is a single-file
// change.
package version

// Build-time variables. Defaults are intentionally honest about
// being unstamped ("dev" / "none" / "unknown") so a `go run` or
// `go test` invocation doesn't pretend to be a real release.
var (
	tag    = "dev"
	commit = "none"
	date   = "unknown"
)

// Tag returns the semantic version of this build, e.g. "v0.4.1"
// for a release or "dev" when run uninstrumented. Use this for
// User-Agent strings, the --version subcommand and any other
// surface where a human or operator wants to know what they're
// looking at.
func Tag() string { return tag }

// Commit returns the short git SHA the binary was compiled
// from, e.g. "ab12cd3". Useful for bug reports that need to be
// tied back to a specific commit when the tag alone is too
// coarse (snapshot builds, between-tag CI artefacts).
func Commit() string { return commit }

// Date returns the ISO-8601 UTC build timestamp. Stamped by
// goreleaser; "unknown" otherwise.
func Date() string { return date }
