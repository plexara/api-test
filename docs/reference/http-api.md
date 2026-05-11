---
title: HTTP API
description: Health probes, well-known metadata, and the portal/admin API surface.
---

# HTTP API

This page documents the non-`/v1/*` HTTP surface: health probes,
well-known metadata, and the portal + admin API. The `/v1/*` endpoint
groups have their own pages under [Endpoints](../endpoints/overview.md).

## Health and readiness

```http
GET /healthz       → 200 "ok"
GET /readyz        → 200 "ready"   (normally)
                   → 503 "draining" (during shutdown grace)
```

Both return plain text. Useful for K8s probes, load-balancer health
checks, and the binary's own `--healthcheck` flag.

## OpenAPI document

```http
GET /openapi.json
GET /openapi.yaml
```

The full OpenAPI 3.x document for every registered endpoint group.
Generated in-tree from the same metadata the portal uses, so it can't
drift from the served routes. A boot-time self-check fails startup if
a route is mounted without a doc entry (or vice versa).

## Discovery

```http
GET /docs
```

Renders a Redoc / Swagger UI view of `/openapi.json` for human
inspection. The portal's Discovery page renders its own native
reference against the same `/openapi.json` document (for full
light/dark parity with the portal); `/docs` is still served as the
canonical Redoc view and is linked from the portal header.

## Well-known metadata

```http
GET /.well-known/oauth-protected-resource
GET /.well-known/openid-configuration   (when oidc.enabled)
```

RFC 9728 protected-resource metadata advertises which OIDC issuer
api-test accepts tokens from, so OAuth2-aware clients can discover
the IdP without out-of-band config.

## Portal API

Read-only endpoints under `/api/v1/portal/`. All require an authenticated
operator session (browser cookie or `X-API-Key`).

| Method + path | Returns |
| --- | --- |
| `GET /api/v1/portal/me` | Current operator identity. |
| `GET /api/v1/portal/endpoints` | All registered routes with metadata. |
| `GET /api/v1/portal/audit/events` | Filtered audit events; pagination via `?limit=&offset=`. |
| `GET /api/v1/portal/audit/events/{id}` | Single event with payload row joined. |
| `GET /api/v1/portal/audit/timeseries` | Bucketed `count`, `errors`, `avg_duration_ms` for the dashboard. |
| `GET /api/v1/portal/audit/breakdown` | Group-by `tool`, `user`, `success`, `auth_type`. |
| `GET /api/v1/portal/audit/stats` | p50/p95 latency, error rate, totals. |
| `GET /api/v1/portal/audit/stream` | SSE feed of newly written events. |
| `GET /api/v1/portal/audit/export.ndjson` | Filtered events as NDJSON for bulk export. |
| `POST /api/v1/portal/audit/replay/{id}` | Re-issue a captured request through the local mux. |
| `POST /api/v1/portal/tryit/{group}/{route}` | Try-It dispatch into the local mux. |
| `GET /api/v1/portal/config` | Loaded config with secrets masked. |

The audit query filters mirror Go's `audit.QueryFilter` — `from`,
`to`, `method`, `path`, `route_name`, `user_subject`, `session_id`,
`status`, `success` — and every list endpoint paginates uniformly.

## Admin API

Mutation endpoints under `/api/v1/admin/`. Same auth requirements as
the portal API; intended for portal use plus operator scripts.

| Method + path | Effect |
| --- | --- |
| `GET    /api/v1/admin/api-keys` | List bcrypt-stored API keys (no plaintext). |
| `POST   /api/v1/admin/api-keys` | Mint a new key; response carries the plaintext **once**. |
| `DELETE /api/v1/admin/api-keys/{name}` | Revoke a key. |

## Browser auth (when `portal.enabled` and `oidc.enabled`)

| Method + path | Effect |
| --- | --- |
| `GET  /portal/auth/login` | Begin OIDC PKCE; 302 to the IdP. |
| `GET  /portal/auth/callback` | OIDC callback; sets the session cookie; 302 to `/portal/`. |
| `POST /portal/auth/logout` | Clear the cookie; 302 to `/portal/`. |

## SPA

```http
GET /portal/                 → SPA index.html
GET /portal/{any-other}      → SPA index.html (client-side router)
GET /portal/assets/{file}    → SPA static asset
```

The mux strips the `/portal` prefix and serves from the embedded
`internal/ui/dist` filesystem. SPA routes that aren't real files fall
back to `index.html` so the React Router handles them.

## CORS

The mux wraps every response in permissive CORS headers
(`Access-Control-Allow-Origin: *`). api-test is a test fixture; CORS
is the path of least resistance for browser-based test runners.
Production deployments behind a real reverse proxy can override.

## Mux ordering

Go 1.22+ `http.ServeMux` patterns:

1. `GET /healthz` — exact.
2. `GET /readyz` — exact.
3. `GET /.well-known/...` — exact.
4. `GET /openapi.{json,yaml}`, `GET /docs` — exact.
5. `/api/v1/portal/*`, `/api/v1/admin/*` — by prefix.
6. `/portal/...` — by prefix; serves SPA.
7. `<METHOD> /v1/...` — exact per (method, path), one per registered
   route.
8. `GET /` — root banner / 404 JSON fallback.

Anything outside the above pattern set returns the JSON 404 from the
root handler.
