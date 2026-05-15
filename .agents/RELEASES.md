# RELEASES.md: how baifo reaches its users

Tagged releases matching `v*` (e.g. `v0.1.0`) trigger `.github/workflows/release.yml`. The workflow also has a `workflow_dispatch` trigger for manual runs (goreleaser detects the missing tag and falls back to snapshot mode, no publish). In both cases the workflow calls [goreleaser](https://goreleaser.com/) (schema v2) to produce every artifact in one shot. Configuration lives in `.goreleaser.yaml` at the repo root; the workflow itself is a thin driver.

## What a release ships

- **Binaries**, one per platform/arch combination:
  - `linux/amd64`, `linux/arm64`, `linux/armv7` (Raspberry Pi).
  - `darwin/amd64`, `darwin/arm64`.
  - `windows/amd64`, `windows/arm64`.
  - `freebsd/amd64`, `freebsd/arm64`.

  Each binary is statically linked (`CGO_ENABLED=0`) and bundled with `LICENSE` and `README.md` in a `.tar.gz` (or `.zip` on Windows).

- **Checksums** as a single `baifo_<version>_checksums.txt` (SHA-256). Users verify with `sha256sum --check baifo_<version>_checksums.txt`.

- **Source tarball** as `baifo_<version>_source.tar.gz`: a clean snapshot of the repo at the tagged commit, useful for downstream packagers (Nix, AUR, FreeBSD ports, etc.) that prefer building from a stable archive rather than a `git clone`.

- **Multi-arch Docker image** pushed to `ghcr.io/achetronic/baifo:<version>` plus `:latest` (where `<version>` does not contain a leading `v` prefix). The manifest list resolves to `linux/amd64` or `linux/arm64` automatically. Base image: `gcr.io/distroless/static-debian12:nonroot`.

- **Homebrew formula** updated in the [`achetronic/homebrew-baifo`](https://github.com/achetronic/homebrew-baifo) tap. Stable tags only; pre-releases skip the brew step via `skip_upload: auto` in `.goreleaser.yaml`.

- **GitHub Release**, published as a **draft** so a human can review the auto-generated changelog before announcing. The changelog groups commits by conventional-commits prefix (`feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, plus an "Other" bucket for anything that doesn't match). Noise prefixes (`test:`, `ci:`, `chore:`, `style:`, `build:`) are filtered out.

## Pre-releases

Tags containing a suffix such as `vX.Y.Z-rc1`, `vX.Y.Z-beta2`, or `vX.Y.Z-alpha.3` are automatically flagged as **pre-release** on GitHub. They still produce binaries, checksums, source tarballs, and Docker images, but the Homebrew formula is left untouched because `brew install baifo` tracks stable versions only.

## Required repository secrets

| Secret                      | Purpose                                                                                  |
| --------------------------- | ---------------------------------------------------------------------------------------- |
| `HOMEBREW_TAP_GITHUB_TOKEN` | PAT with `repo` scope on `achetronic/homebrew-baifo`. Without it, the Homebrew step fails. |
| `GITHUB_TOKEN`              | Provided automatically by GitHub Actions. Authorizes GHCR push and GitHub Release.       |

An unset `HOMEBREW_TAP_GITHUB_TOKEN` causes the entire release workflow to fail. This is intentional to prevent partial releases where binaries publish but the Homebrew tap remains un-updated.

## Smoke-testing locally

```bash
# Validate the schema without building anything.
goreleaser check

# Cross-compile every target into ./dist without publishing,
# pushing, signing, docker building, or announcing.
goreleaser release --snapshot --clean --skip=publish,sign,docker,announce
```

The CI job `release-check` (in `.github/workflows/ci.yml`) executes these validation commands on every push to `main` and on every pull request targeting `main`, catching syntax errors or cross-compilation failures before a tag is pushed.

## Release procedure

1. Confirm the `main` branch is green and no unpublished release drafts exist.
2. Select the tag version based on SemVer:
   - `vX.Y.Z`: Stable release (triggers Homebrew tap update).
   - `vX.Y.Z-rcN`: Release candidate (skips Homebrew).
3. Create and push the tag:
   ```bash
   git tag -a vX.Y.Z -m "baifo vX.Y.Z"
   git push origin vX.Y.Z
   ```
4. Monitor the Release workflow under GitHub Actions.
5. Open the draft release, verify the auto-generated changelog, customize details if desired, and publish.

If the release fails or produces bad artifacts:

- **Delete the draft release** on GitHub.
- **Delete the git tag**: `git push --delete origin vX.Y.Z` and `git tag -d vX.Y.Z`.
- **Remove bad container tags** from the GHCR package settings web UI.
- **Revert the Homebrew update** by reverting the corresponding commit in the `achetronic/homebrew-baifo` repository.
- Fix issues, retag, and push again.

## Release automation rationale

goreleaser is the release driver for baifo. It collapses cross-compilation, packaging, Docker manifest generation, Homebrew formula updates, and release note generation into a single declarative configuration, replacing custom release scripts.
