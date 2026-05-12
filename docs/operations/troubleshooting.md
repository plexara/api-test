---
title: Troubleshooting
description: Common api-test pitfalls and how to diagnose them — 401s, missing audit rows, empty portals, OIDC failures, integration-test flakes.
---

# Troubleshooting

A list of failure modes that come up often enough to be worth writing
down. Every entry: symptom, what it actually means, fix.

## 401 Unauthorized everywhere

**Symptom**: every `/v1/*` call returns 401, even with a key that
worked yesterday.

**Likely causes**:

- `auth.allow_anonymous: false` and the key isn't in any store. Confirm
  with `make dev-anon` (anonymous mode) — if that works, the issue is
  the credential, not the wiring.
- Bad credential, not missing. **A bad key does not fall back to
  anonymous** even when `allow_anonymous: true`. Send no auth header
  at all to take the anonymous path. See
  [Authentication › Anonymous mode](../configuration/auth.md#anonymous-mode).
- File-store key value is `${APITEST_DEV_KEY}` and the env var is
  empty. The `${VAR:-default}` interpolation lets you set a fallback;
  without a `:-`, the literal `${VAR}` survives only if `VAR` is set.

**Diagnose**:

```bash
curl -i http://localhost:8080/v1/whoami                          # see WWW-Authenticate header
curl -i -H "X-API-Key: $APITEST_DEV_KEY" http://localhost:8080/v1/whoami
```

The `WWW-Authenticate` response header tells you whether api-test saw
"no credential" (`Bearer realm="api-test"`) or "bad credential"
(`Bearer realm="api-test", error="invalid_token"`).

## 401 on the portal API only

**Symptom**: the SPA loads, but every `/api/v1/portal/*` request 401s.

**Likely causes**:

- No portal session cookie *and* no API key on the request. The portal
  requires one or the other. Sign in via OIDC, or paste an API key on
  the sign-in screen.
- `portal.cookie_secure: true` over plain HTTP. The browser refuses to
  send a `Secure` cookie back to a non-TLS endpoint. Either run behind
  TLS or flip the flag off for local dev.
- `portal.cookie_secret` is empty. The session store fails to start
  cleanly with no secret; check the boot log for `session store:`.

## /portal/ returns `{"status":"banner"...}` instead of the SPA

**Symptom**: visiting `/portal/` in a browser shows raw JSON, not the
React UI.

**Cause**: `internal/ui/dist/` only contains `.gitkeep` — the
`//go:embed` is empty, so the mux falls back to a stub JSON banner.

**Fix**:

```bash
make ui          # builds ui/dist/ → internal/ui/dist/
make build       # rebuild the binary so the embed picks up the bundle
```

`make build` (and `make verify`) refuse to build when the embed is
empty. Bare `go build ./...` does not — that's the path that produces
this surprise.

## Audit log is empty in the portal

**Symptom**: requests succeed, but `/portal/audit` is empty or
out-of-date.

**Likely causes**:

- `audit.enabled: false` in config. The shipped `*.dev.yaml` profile
  has it off; only `*.live.yaml` enables it.
- `database.url` is empty. With `audit.enabled: true` and no database,
  the binary fails to start; if it started, you're on the dev config.
- Health, readiness, well-known, and the portal's own auth flow are
  **intentionally** skipped — they don't generate audit rows. Only
  `/v1/*` requests do.
- The async buffer dropped the events. Check the binary's stderr for
  `audit buffer full; dropping events`. Default depth is 4096; raise
  it if you hit sustained drop warnings.

## OIDC login redirects loop

**Symptom**: the IdP redirects back to api-test, which redirects back
to the IdP, repeatedly.

**Likely causes**:

- `oidc.issuer` mismatches the IdP's actual issuer claim. Visit
  `${issuer}/.well-known/openid-configuration` and confirm the
  `issuer` field in the response matches the config exactly (including
  trailing slash).
- `oidc.audience` doesn't match the IdP's token `aud` claim. Decode a
  token at [jwt.io](https://jwt.io) and compare.
- Clock skew. `oidc.clock_skew_seconds` defaults to 30; if the binary
  and the IdP disagree by more, validation fails with `exp` or `nbf`
  errors. Check the binary log for `oidc:` warnings.

## `make integration` hangs or times out

**Symptom**: integration suite stalls at "starting postgres container."

**Likely causes**:

- Docker isn't running. The `make integration` target gates on
  `docker info`; if you see a hang, you started Docker after the
  target gate ran.
- Resource limits on the Docker VM. `testcontainers` pulls
  `postgres:16-alpine` (~250 MiB) and needs ~512 MiB free.
- Ryuk (the testcontainers reaper) is being blocked by a corporate
  proxy. Set `TESTCONTAINERS_RYUK_DISABLED=true` if you trust your
  own cleanup, or whitelist `quay.io/testcontainers/ryuk`.

## `make verify` passes locally but CI fails

**Symptom**: green `make verify`, red CI.

**First check**: pinned tool versions in `Makefile`
(`GOLANGCI_LINT_VERSION`, `GOSEC_VERSION`, `SEMGREP_VERSION`) must
match the versions in `.github/workflows/ci.yml`. CI installs from
those refs, the Makefile installs to `bin/tools/`. Drift = different
outcomes.

**Second check**: `semgrep` is the most likely culprit. The Makefile
warns on version drift but doesn't fail — if CI uses a newer rule
set, it can flag code the local pinned version accepts. Run
`pipx install --force semgrep==<CI version>` to align.

**Third check**: integration tests sometimes flake on `docker compose
up` race conditions in CI. Re-run; if it persists, it's a real bug.

## Plexara can't reach api-test

**Symptom**: Plexara connection registration succeeds, but invoking
the connection returns "upstream unreachable."

**Likely causes**:

- `server.base_url` doesn't match the actual reachable URL. Plexara
  uses this for redirect and OpenAPI server URLs; if it points at
  `localhost` while Plexara is in a different network namespace,
  every redirect breaks.
- TLS: api-test is plain HTTP behind a TLS-terminating LB and the
  Plexara connection is configured `https://...`. Check that the LB
  is actually forwarding to api-test.
- Health probe disabled. Plexara may pre-flight `/healthz`; if you
  blocked that path in front of api-test, the connection looks dead
  even when `/v1/*` would work.

## When in doubt

- The [Architecture](../reference/architecture.md) diagrams document
  the exact request flow.
- The audit log (when enabled) is the source of truth for what
  api-test actually saw — query it before assuming the gateway is at
  fault.
- File an issue with the binary's startup log (config, "listening",
  any WARN/ERROR lines) at
  <https://github.com/plexara/api-test/issues>.
