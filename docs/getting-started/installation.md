---
title: Installation
description: Binary download, container image (GHCR), go install, and building from source. Pick whichever flavor matches your deployment.
---

# Installation

api-test ships as a single static Go binary plus an OCI container image
on GHCR. Pick whichever fits your deployment.

## Container image (recommended)

Distroless multi-arch image, signed via cosign on tag.

```bash
docker pull ghcr.io/plexara/api-test:latest

docker run --rm -p 8080:8080 \
  -v $(pwd)/configs/api-test.dev.yaml:/etc/api-test/api-test.yaml:ro \
  ghcr.io/plexara/api-test:latest --config /etc/api-test/api-test.yaml
```

Tags: `latest` (HEAD of `main`), `vX.Y.Z` (released versions),
`vX.Y` (latest patch in a minor line), `vX` (latest minor in a major
line). Use `vX.Y.Z` for reproducible builds; use `vX` for latest
non-breaking updates.

## Binary download

Pre-built binaries for linux/amd64, linux/arm64, and darwin/arm64 on
the [releases page](https://github.com/plexara/api-test/releases). Each
release ships a SHA-256 checksum file and a cosign signature.

```bash
# Linux amd64
curl -sLO https://github.com/plexara/api-test/releases/latest/download/api-test_Linux_x86_64.tar.gz
tar xzf api-test_Linux_x86_64.tar.gz
./api-test --version
```

## go install

If you have Go on your PATH:

```bash
go install github.com/plexara/api-test/cmd/api-test@latest
```

The resulting binary lands in `$GOBIN` (or `$GOPATH/bin`). Version
metadata is `dev / none / unknown` because the build skipped goreleaser's
`-ldflags -X` plumbing; for an authoritative version, use a release
binary or the container image.

## From source

```bash
git clone https://github.com/plexara/api-test
cd api-test
make build       # produces ./bin/api-test
./bin/api-test --version
```

`make build` stamps version, commit, and date into the binary via
`-ldflags -X`. Run `make help` to see the full target catalog.

To build the container image locally:

```bash
make docker
docker run --rm -p 8080:8080 api-test:vX.Y.Z-dirty --config /etc/api-test/api-test.yaml
```

The Dockerfile is a multi-stage build that compiles a static linux/amd64
binary then copies it into `gcr.io/distroless/static-debian12:nonroot`.
The binary doubles as its own healthcheck (`--healthcheck`) so the image
doesn't need curl/wget.

## What you need next

api-test wants Postgres for the audit log. The
[quickstart](quickstart.md) runs the binary in anonymous mode (no
Postgres needed) today; the full Postgres + Keycloak + portal stack
lands with M3. To deploy on your own infrastructure once auditing is
on, point `database.url` at a Postgres 14+ instance; migrations run
on boot.
