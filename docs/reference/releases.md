---
title: Releases
description: Version policy, container image tags, migration notes for api-test.
---

# Releases

api-test follows semver. `vMAJOR.MINOR.PATCH`:

- **Major** — breaking changes to config keys, endpoint paths, or the
  audit schema.
- **Minor** — new endpoint groups, new auth modes, new portal pages.
  Backward-compatible config and schema migrations.
- **Patch** — bug fixes, documentation, internal refactors.

## Container tags

Every release publishes:

- `vX.Y.Z` — exact version. Use this for reproducible deployments.
- `vX.Y` — latest patch in the minor line. Auto-tracks security fixes.
- `vX` — latest minor in the major line. Auto-tracks new features.
- `latest` — HEAD of `main`. **Not** for production.

All multi-arch (linux/amd64, linux/arm64) and signed via cosign.

## Migration notes

### Unreleased / pre-1.0

api-test is currently pre-1.0 and shipping milestones in sequence
(M1 → M5). Each milestone may rename config keys or rework schema
without a major-version bump. Pin to a specific commit if you need
stability before 1.0.

The current milestone status:

| Milestone | Scope | Status |
| --- | --- | --- |
| M1 | HTTP fixture skeleton; identity / data / failure / echo groups | Released |
| M2 | DB + audit + non-OAuth inbound auth (file API key, bearer, DB key) | Released |
| M3 | Portal + React SPA + Keycloak + OIDC JWT validator + browser OIDC PKCE | Released |
| M4 | In-tree OpenAPI generator + remaining groups (pagination, methods, security, export, streaming) | Released |
| M5 | Docs site + CI + goreleaser + Kubernetes examples + plexara-connection.yaml | In progress |

## Where to find releases

- [GitHub releases](https://github.com/plexara/api-test/releases) —
  binary tarballs, checksums, cosign signatures, release notes.
- [GHCR container registry](https://github.com/plexara/api-test/pkgs/container/api-test) —
  multi-arch images, all tags.
- [git tags](https://github.com/plexara/api-test/tags) — `vX.Y.Z`.

## Upgrade workflow

For a `vX.Y.Z` → `vX.Y.Z+1` patch upgrade:

1. Pull the new image / binary.
2. Roll the deployment. Migrations run automatically on boot.
3. Verify `/healthz` and `/readyz` come back 200.

For a `vX.Y` → `vX.(Y+1)` minor upgrade:

1. Read the release notes for new config keys; backfill any that
   moved from "off by default" to "on by default".
2. Run the binary against your existing config; migrations apply
   forward-only.
3. Roll the deployment; readiness gating handles the in-flight
   request drain.

For a `vX` → `v(X+1)` major upgrade:

1. Read the migration notes section in the release notes carefully.
2. Test against staging first.
3. Plan a maintenance window if the audit schema migration needs to
   rebuild indexes.

## Breaking changes

Each major and minor release records its breaking changes in the
GitHub release notes — that's the canonical registry. For the pre-1.0
era, individual milestone PRs (M1–M5) are the source of truth; the
[git log on `main`](https://github.com/plexara/api-test/commits/main)
records every config-key rename and schema migration. Migrations are
forward-only, so a fresh deploy off `main` always boots; the friction
is in-place upgrades against an existing audit history.

Specifically watch for:

- **Config-key renames** — `pkg/config` rejects unknown keys silently
  (YAML deserialization is lenient). After an upgrade, grep your
  config against [`configs/api-test.example.yaml`](https://github.com/plexara/api-test/blob/main/configs/api-test.example.yaml)
  to confirm every key you set still exists.
- **Audit schema** — backed by [golang-migrate](https://github.com/golang-migrate/migrate).
  Migrations are forward-only and run automatically on boot; rolling
  back to a prior binary is **not** safe without first restoring the
  schema manually.
- **Endpoint paths** — moving an endpoint between groups would
  invalidate any audit-row analytics that filter on
  `endpoint_group`. Endpoint paths are stable post-1.0; pre-1.0 path
  changes are called out in the relevant PR.
