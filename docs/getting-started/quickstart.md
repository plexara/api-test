---
title: Quickstart
description: Get the api-test binary running in under a minute and hit every endpoint with curl.
---

# Quickstart

```bash
git clone https://github.com/plexara/api-test
cd api-test
make dev
```

`make dev` spins up the full local stack: starts Postgres + Keycloak
via `docker-compose.dev.yml`, waits for both to be ready, builds the
SPA into `internal/ui/dist/` if missing, and runs the binary against
`configs/api-test.live.yaml`. On the first run it writes `.env.dev`
with random cookie / API-key / bearer secrets (gitignored, reused on
subsequent runs).

For the fastest iteration loop without standing up Postgres or
Keycloak, use `make dev-anon` — anonymous mode, no audit, no portal:

```text
make dev-anon   →   go run ./cmd/api-test --config configs/api-test.dev.yaml
```

When the stack is up:

<div class="def-cards" markdown>

<div class="def-card" markdown>
<div class="def-card__head"><code class="def-card__name">http://localhost:8080/v1/...</code></div>
<div class="def-card__body" markdown>Endpoint groups. See [Endpoints overview](../endpoints/overview.md).</div>
</div>

<div class="def-card" markdown>
<div class="def-card__head"><code class="def-card__name">http://localhost:8080/healthz</code></div>
<div class="def-card__body" markdown>Liveness probe.</div>
</div>

<div class="def-card" markdown>
<div class="def-card__head"><code class="def-card__name">http://localhost:8080/portal/</code></div>
<div class="def-card__body" markdown>Portal. Sign in with `dev` / `dev` (OIDC) or paste an API key.</div>
</div>

<div class="def-card" markdown>
<div class="def-card__head"><code class="def-card__name">http://localhost:8081/</code></div>
<div class="def-card__body" markdown>Keycloak admin console (`admin` / `admin`).</div>
</div>

</div>

## Auth-enabled iteration

To exercise the inbound auth chain without standing up Keycloak, run the
binary against a config that enables `api_keys.file` and/or
`bearer.tokens` while leaving `audit.enabled: false`:

```bash
cat > /tmp/api-test-auth.yaml <<'EOF'
auth:
  allow_anonymous: false
api_keys:
  file:
    - { name: "devkey", key: "dev-secret-1" }
bearer:
  tokens:
    - { name: "devbearer", token: "dev-bearer-1" }
endpoints:
  identity: { enabled: true }
  data:     { enabled: true }
  failure:  { enabled: true }
  echo:     { enabled: true }
EOF

go run ./cmd/api-test --config /tmp/api-test-auth.yaml
```

`make dev-secrets` (idempotent — only writes if missing) creates a
gitignored `.env.dev` with random `APITEST_DEV_KEY` /
`APITEST_DEV_BEARER` / `APITEST_COOKIE_SECRET` values; `make dev`
sources it automatically.

## Verify it works

A quick curl smoke test against the running server (anonymous mode):

```bash
# Self-describing root
curl -s http://localhost:8080/ | jq

# Liveness
curl -s http://localhost:8080/healthz

# Identity (anonymous)
curl -s http://localhost:8080/v1/whoami | jq

# Deterministic fixture
curl -s http://localhost:8080/v1/fixed/hello | jq

# Exact-N-bytes response
curl -s 'http://localhost:8080/v1/sized?bytes=64' | jq

# Seeded lorem
curl -s 'http://localhost:8080/v1/lorem?words=10&seed=cat' | jq

# Forced failure
curl -s -o - -w "STATUS=%{http_code}\n" http://localhost:8080/v1/status/418

# Echo
curl -s -X POST http://localhost:8080/v1/echo \
  -H 'Content-Type: application/json' \
  -d '{"hello":"world"}' | jq
```

In the auth-enabled config above, prefix every endpoint call with
`-H "X-API-Key: dev-secret-1"` (or `?api_key=dev-secret-1` in the
query string), or `-H "Authorization: Bearer dev-bearer-1"`.

## Stop the stack

In the foreground binary's terminal: `Ctrl-C`. To tear down the
Postgres + Keycloak containers as well:

```bash
make dev-down   # stops containers, keeps volumes (Postgres data persists)
```

Add `-v` to the underlying compose command if you want to wipe the
audit history along with the containers.

## Next

- [Register with Plexara](register-with-plexara.md) — wire api-test in
  as a connection in a running Plexara instance.
- [Endpoints overview](../endpoints/overview.md) — catalog of every
  route and what gateway behavior it exercises.
- [YAML reference](../configuration/reference.md) — every config key
  with its default and environment override.
- [Testing a gateway](../operations/gateway-testing.md) — patterns for
  asserting on the Plexara API gateway end-to-end.
