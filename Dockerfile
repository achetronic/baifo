# baifo distroless-base image.
#
# goreleaser cross-compiles the binary on the host (CGO_ENABLED=0,
# already static) and feeds it to `docker buildx` via the per-arch
# build context. By the time we get here, the binary already
# matches the target platform, so this file is intentionally
# minimal: copy the binary, drop CA certs in for HTTPS to LLM
# providers, run as a non-root user.
#
# We deliberately don't use a multi-stage build with `golang:alpine`:
# goreleaser is already the build stage, and re-running `go build`
# inside docker would double the work and lose the version
# ldflags goreleaser injected.

FROM gcr.io/distroless/static-debian12:nonroot

# Metadata the OCI ecosystem looks for. The dynamic ones
# (revision, version, created) get overridden by the labels we
# pass via `build_flag_templates` in .goreleaser.yaml; declaring
# them here too is a belt-and-braces against running `docker
# build` outside of the release pipeline.
LABEL org.opencontainers.image.title="baifo"
LABEL org.opencontainers.image.description="Single-binary, terminal-native agent harness."
LABEL org.opencontainers.image.source="https://github.com/achetronic/baifo"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# Distroless `nonroot` UID is 65532. We don't need root for any
# of baifo's runtime paths.
USER nonroot:nonroot

# Persist the .baifo/ tree at /home/nonroot/.baifo by default. The
# user can mount a volume there or pass --config-dir to override.
WORKDIR /home/nonroot

# goreleaser (dockers_v2) lays out the build context with one
# directory per target platform (linux/amd64/baifo, linux/arm64/baifo)
# and buildx provides TARGETPLATFORM, so this single COPY picks the
# right binary for each arch of the multi-arch manifest. The file
# name matches the `binary:` field in builds[].
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/baifo /usr/local/bin/baifo

ENTRYPOINT ["/usr/local/bin/baifo"]
