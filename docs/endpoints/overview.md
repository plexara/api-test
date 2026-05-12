---
title: Endpoints overview
description: Catalog of every api-test endpoint group, the gateway behavior each one exercises, and the determinism contract.
---

# Endpoints overview

api-test groups its routes by what gateway behavior they exercise. Each
group is a small Go package under
[`pkg/endpoints`](https://github.com/plexara/api-test/tree/main/pkg/endpoints)
implementing a tiny `Endpoints` interface; the registry composes them
into the mux and into the published OpenAPI document.

## Determinism contract

Same input → same output. Forever.

- Endpoints that take a key, path parameter, or seed are pure functions
  of those inputs. The body never changes.
- Endpoints that take a status code or duration produce exactly the
  requested behavior; the gateway sees what it asked for.
- Endpoints that fail "randomly" take a `(seed, call_id)` pair so the
  same inputs always produce the same outcome.

This makes assertions falsifiable. If the gateway forwarded the right
call, the body it got back is bit-for-bit predictable.

## Groups

| Group | Purpose |
| --- | --- |
| [Identity](identity.md) | Verify identity / header pass-through. |
| [Data](data.md) | Deterministic bodies for caching / dedup / size handling. |
| [Failure](failure.md) | Controlled error codes, latency, seeded flake. |
| [Echo](echo.md) | Generic catch-all that returns the request verbatim. |
| [Streaming](streaming.md) | Chunked, SSE, NDJSON responses. |
| [Pagination](pagination.md) | One endpoint per cursor style the gateway recognizes. |
| [Methods](methods.md) | Method matrix on `/v1/method/echo`. |
| [Security](security.md) | Probe targets the gateway should refuse to forward. |
| [Export](export.md) | Large/long-running targets exercising `api_export`. |

## Toggling groups

Every group is gated by `endpoints.<group>.enabled` in config. Disable
a group and its routes vanish from the mux *and* from the OpenAPI
document.

```yaml
endpoints:
  identity: { enabled: true }
  data:     { enabled: true }
  failure:  { enabled: false }   # off
  echo:     { enabled: true }
```

This is useful for narrowing what a gateway connection sees — e.g.,
register two api-test connections, one with only `failure` enabled (for
chaos testing) and one with only `data` (for stable cache fixtures).

## OpenAPI exposure

Every enabled route is published in `/openapi.json` and `/openapi.yaml`
. The Plexara gateway's `api_list_endpoints` tool reads this
document, so registering api-test with its OpenAPI spec inline gives
gateway callers a discoverable catalog.

A boot-time self-check (`pkg/oapi/selfcheck.go`) verifies every route
the registry declares actually has a mux handler, and vice-versa, so
the doc can't drift from the served routes.

## Audit

Every request to a `/v1/*` endpoint lands in the audit log with the
resolved identity, method, path, status, durations, and (configurably)
the redacted headers and bodies. Health and well-known endpoints sit
outside the audit middleware and don't generate rows.

See [Audit log](../operations/audit.md) for schema and retention.
