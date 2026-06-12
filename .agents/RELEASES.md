# Releases

Tagged releases matching `v*` (e.g. `v0.1.0`) trigger `.github/workflows/release.yml`.

The pipeline:

1. Checkout with `lfs: true` — the quantized weights
   (`internal/embeddings/assets/nomic-embed-text.weights`, ~132MB,
   `go:embed`-ed into the binary) are committed via Git LFS. A default
   checkout leaves a ~130-byte pointer file instead.
2. **Verify step** — fails the run if the `.weights` file is missing or smaller
   than 100MB (i.e. LFS didn't pull). Without this guard, `go:embed` would
   happily bake in the pointer and the release would ship binaries with broken
   embeddings.
3. `make release` — cross-compiles `dist/baifo-{linux,darwin,windows}-{amd64,arm64}`.
4. `gh release create` — uploads everything in `dist/` to the GitHub release
   for the tag, with auto-generated notes.

The weights are produced once (not per release) with `make fetch-model`, which
downloads the nomic-embed-text-v1.5 safetensors from Hugging Face and quantizes
them to int8 with `internal/embeddings/tools/convert.py`. Re-run it only when
upgrading the model, then commit the new `.weights` through LFS.

## Cutting a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

That's it. To re-cut a botched release, delete it first:

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
