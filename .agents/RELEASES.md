# Releases

`.github/workflows/release.yml` runs in two ways:

- **Automatic** — you publish a GitHub release (with its tag). The
  `release: published` event fires; the pipeline builds from that tag's
  commit and uploads the assets to that same release. (We listen to the
  `release` event, not a tag `push`, because releases created from the
  GitHub UI/API don't reliably emit a tag-push event.)
- **Manual** (`workflow_dispatch`) with two inputs:
  - `version` — the semver/tag of the release to publish assets to. It
    must already exist (the release + tag), e.g. `v0.1.0`.
  - `from_master` (bool, default `false`) — when `false`, checkout the
    `version` tag. When `true`, checkout **master HEAD** instead, but the
    assets are still published to the `version` release. Use it to ship
    the current master code under an existing release tag.

The pipeline:

1. **Resolve target** — from whichever trigger fired, computes `tag` (the
   release the assets go to) and `ref` (the git ref actually built).
2. Checkout the resolved `ref` with `lfs: true` — the quantized weights
   (`internal/embeddings/assets/nomic-embed-text.weights`, ~132MB,
   `go:embed`-ed into the binary) are committed via Git LFS. A default
   checkout leaves a ~130-byte pointer file instead.
3. **Verify step** — fails the run if the `.weights` file is missing or smaller
   than 100MB (i.e. LFS didn't pull). Without this guard, `go:embed` would
   happily bake in the pointer and the release would ship binaries with broken
   embeddings.
4. `make release VERSION=<tag>` — cross-compiles
   `dist/baifo-{linux,darwin,windows}-{amd64,arm64}`. The tag is passed
   explicitly so the embedded version matches the release even when the
   code is built from master (otherwise `git describe` would disagree).
5. **Publish** — `gh release upload <tag> dist/* --clobber` (creating the
   release first only as a fallback if it somehow doesn't exist).

The weights are produced once (not per release) with `make fetch-model`, which
downloads the nomic-embed-text-v1.5 safetensors from Hugging Face and quantizes
them to int8 with `internal/embeddings/tools/convert.py`. Re-run it only when
upgrading the model, then commit the new `.weights` through LFS.

## Cutting a release

Create and publish the release on GitHub (UI or CLI); that also creates
the tag and triggers the build automatically:

```bash
gh release create v0.1.0 --title v0.1.0 --generate-notes
```

To rebuild/republish assets for an existing release by hand, run the
`Release` workflow manually (Actions tab or `gh workflow run`):

```bash
# build from the v0.1.0 tag
gh workflow run release.yml -f version=v0.1.0

# build from current master, but publish to the v0.1.0 release
gh workflow run release.yml -f version=v0.1.0 -f from_master=true
```

To re-cut a botched release, delete it first:

```bash
gh release delete v0.1.0 --yes
git tag -f v0.1.0 && git push -f origin v0.1.0
```

## Artifacts

Plain binaries (no archives, no checksums, no Docker images, no Homebrew):

- `baifo-linux-amd64`, `baifo-linux-arm64`
- `baifo-darwin-amd64`, `baifo-darwin-arm64`
- `baifo-windows-amd64.exe`, `baifo-windows-arm64.exe`

## Required secrets

None beyond the default `GITHUB_TOKEN` (needs `contents: write`, declared in
the workflow).

## History

goreleaser (and the CI workflow) drove releases until v0.0.0; both were removed
in favor of this make-based pipeline. Docker images and the Homebrew tap were
dropped at the same time.
