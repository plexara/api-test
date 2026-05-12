---
title: Audit log
description: Postgres schema, retention, redaction, and the JSON shape returned by the portal API for api-test's HTTP audit log.
---

# Audit log

Every request that reaches an `/v1/*` endpoint produces one row in
`audit_events` and (when `audit.capture_payloads` is true, the
default) one row in `audit_payloads`. Health, readiness, well-known,
and portal-auth flows sit outside the middleware stack and don't
generate audit rows.

The pipeline is async: the request handler enqueues into a buffered
channel; a background goroutine drains into Postgres. A stalled DB
can never inflate request latency. On a full buffer the event is
*dropped* and counted (logged every 1000th drop). For lossless audit,
size the buffer for your peak rate. See
[Architecture › Audit pipeline](../reference/architecture.md#audit-pipeline)
for the data-flow diagram, and [Deployment › Logging](deployment.md#logging)
for how to correlate audit rows with the access log via `request_id`.

## Schema

Two tables, one row each, joined 1:1 on `audit_events.id =
audit_payloads.event_id`.

### `audit_events`

The indexable summary. One row per request.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | TEXT (UUID) | Primary key. Auto-generated when the inserter omits it. |
| `ts` | TIMESTAMPTZ | Request start, UTC. Indexed (`DESC`). |
| `duration_ms` | BIGINT | Total handler time including audit middleware overhead. |
| `request_id` | TEXT | `X-Request-Id` (preserved or generated). |
| `session_id` | TEXT | Reserved for the portal Try-It flow. |
| `user_subject` | TEXT | Resolved Identity.Subject. Indexed. |
| `user_email` | TEXT | OIDC only. |
| `auth_type` | TEXT | `anonymous` / `apikey` / `bearer` / `oauth2`. |
| `api_key_name` | TEXT | KeyName for apikey/bearer; client_id for OIDC. |
| `method` | TEXT | HTTP method. |
| `path` | TEXT | Path the request landed on. Indexed. |
| `route_name` | TEXT | The matched route's name (group-scoped). |
| `endpoint_group` | TEXT | The owning group: `identity`, `data`, etc. |
| `status` | INTEGER | Response status. Indexed. |
| `bytes_in` | INTEGER | Inbound body size (raw). |
| `bytes_out` | INTEGER | Outbound body size (full, including any portion truncated from capture). |
| `success` | BOOLEAN | `200 <= status < 400`. |
| `error_message` | TEXT | Reserved for handlers that surface errors. |
| `error_category` | TEXT | Operator-defined free text. |
| `remote_addr` | TEXT | `r.RemoteAddr`. |
| `user_agent` | TEXT | `User-Agent` header. |

Indexes: `(ts DESC)`, `(route_name, ts DESC)`, `(path, ts DESC)`,
`(user_subject, ts DESC)`, `(session_id, ts DESC)`,
`(status, ts DESC)`.

### `audit_payloads`

The detail row. Same shape as `Event.Payload` in Go.

| Column | Type | Notes |
| --- | --- | --- |
| `event_id` | TEXT | PK; FK to `audit_events.id` ON DELETE CASCADE. |
| `request_headers` | JSONB | Map[name]=values, with redaction applied. |
| `request_query` | JSONB | Map[name]=values, with redaction applied. |
| `request_content_type` | TEXT | Inbound `Content-Type`. |
| `request_body` | BYTEA | Up to `audit.max_payload_bytes`; longer bodies write the prefix and set `request_truncated`. |
| `request_size_bytes` | INTEGER | Captured prefix size (not the full inbound size). |
| `request_truncated` | BOOLEAN | True when the inbound body exceeded the cap. |
| `request_remote_addr` | TEXT | Mirror of summary; convenient for joins. |
| `response_headers` | JSONB | Same redaction. |
| `response_content_type` | TEXT | Outbound `Content-Type`. |
| `response_body` | BYTEA | Same cap rules as request side. |
| `response_size_bytes` | INTEGER | Captured prefix size. |
| `response_truncated` | BOOLEAN | True when the outbound body exceeded the cap. |
| `replayed_from` | TEXT | When this event was a replay of another, points back. |
| `captured_at` | TIMESTAMPTZ | Insert time of the payload row. |

Indexes: `(replayed_from)` partial, GIN on `request_headers` and
`response_headers` (jsonb_path_ops, used for portal filter queries).

## Redaction

The middleware runs every header name and query-param key through
`audit.SanitizeHeaders` / `audit.SanitizeQuery` before insert.
`audit.redact_keys` is a list of case-insensitive substrings; matches
have their values replaced by `[redacted]`.

Default list:

```
password, token, secret, authorization, api_key, api-key,
credentials, bearer, cookie, jwt, session_id, private_key, passwd
```

Both `api_key` and `api-key` are present so both `Authorization` /
`X-API-Key` headers (dashes) and `?api_key=...` query params
(underscore in the param name convention) get caught.

Bodies are *not* redacted. If a request body carries credentials in a
JSON field, store-and-forget it at your own risk; consider lowering
`audit.max_payload_bytes` or disabling `audit.capture_payloads`
entirely in privacy-sensitive deployments.

## Retention

```yaml
audit:
  retention_days: 7
```

Default is 7 days (lower than mcp-test's 30 because api-test responses
can be huge — export endpoints emit 100 MiB bodies). A periodic
cleanup job lands in a future migration; for now, prune manually:

```sql
DELETE FROM audit_events WHERE ts < now() - interval '7 days';
-- audit_payloads cascades.
```

## Backpressure

`AsyncLogger` wraps the inner store with a buffered channel (default
4096 events) and a per-call timeout (default 5s). On a full buffer the
event is dropped and the cumulative drop count is logged every 1000th
drop:

```
WARN audit buffer full; dropping events dropped_total=1
WARN audit buffer full; dropping events dropped_total=1001
```

If you see drops in steady-state load, raise `bufferSize` in
`internal/server.Build` or scale the database.

## Querying

The Go side exposes `audit.QueryFilter` with these predicates: time
range, method, path, route_name, user_subject, session_id, event_id,
status, success, search (ILIKE on path + error_message), limit,
offset. The Postgres store builds a parameterized SQL `SELECT ... FROM
audit_events WHERE … ORDER BY ts DESC, id ASC LIMIT … OFFSET …` from
the filter.

The portal API wraps these filters in HTTP query params and
returns paginated JSON. Direct SQL is also fine; the schema is
documented above and Postgres is the source of truth.

## What it proves about the gateway

The audit log is the assertion surface for "did the gateway forward
what we expected". Common assertions:

- The gateway forwarded the credential under the right transport
  (`auth_type` matches what the connection promised).
- The gateway did *not* forward a header you blocked
  (`request_headers` doesn't carry it).
- The gateway forwarded a custom tracing header
  (`request_headers["X-Trace-Id"]` is present).
- The gateway returned the upstream status verbatim (compare
  `status` here vs the gateway-side record).
- The gateway retried (multiple audit rows for one client call,
  visible by request_id correlation when the gateway preserves it).
