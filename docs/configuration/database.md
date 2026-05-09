---
title: Database & migrations
description: PostgreSQL connection settings; migrations run on boot via golang-migrate.
---

# Database & migrations

api-test uses PostgreSQL 14+ for the audit log and (when enabled) the
bcrypt-backed API key store. Migrations live in
[`pkg/database/migrate/migrations`](https://github.com/plexara/api-test/tree/main/pkg/database/migrate/migrations)
and are embedded into the binary via `go:embed`. They run on every
startup via [golang-migrate](https://github.com/golang-migrate/migrate);
already-applied migrations are skipped.

The database is *optional* when both `audit.enabled` and
`api_keys.db.enabled` are false; api-test runs without a DB in
anonymous + file-only-keys mode (the M1 happy path).

## Configuration

```yaml
database:
  url: "${APITEST_DB_URL:-postgres://api:api@localhost:5432/apitest?sslmode=disable}"
  max_open_conns: 25
  max_idle_conns: 5
  conn_max_lifetime: 1h
```

The DSN passes through to `pgxpool.ParseConfig`, so any
[pgx-supported form](https://pkg.go.dev/github.com/jackc/pgx/v5#ParseConfig)
works: `postgres://`, `postgresql://`, or libpq key=value strings.

## Schema

Three tables:

- **`api_keys`** — bcrypt-hashed API keys (when `api_keys.db.enabled`).
  Columns: `id`, `name`, `hash`, `description`, `created_by`,
  `created_at`, `expires_at`, `last_used_at`. Unique on `name`.
- **`audit_events`** — indexable summary of one inbound request.
  Columns: `id`, `ts`, `duration_ms`, `request_id`, `session_id`,
  `user_subject`, `user_email`, `auth_type`, `api_key_name`, `method`,
  `path`, `route_name`, `endpoint_group`, `status`, `bytes_in`,
  `bytes_out`, `success`, `error_message`, `error_category`,
  `remote_addr`, `user_agent`. Indexed on `(ts DESC)`,
  `(route_name, ts DESC)`, `(path, ts DESC)`,
  `(user_subject, ts DESC)`, `(session_id, ts DESC)`,
  `(status, ts DESC)`.
- **`audit_payloads`** — full request/response envelope joined 1:1 with
  `audit_events.id`. Columns: `event_id`, `request_headers`,
  `request_query`, `request_content_type`, `request_body`,
  `request_size_bytes`, `request_truncated`, `request_remote_addr`,
  `response_headers`, `response_content_type`, `response_body`,
  `response_size_bytes`, `response_truncated`, `replayed_from`,
  `captured_at`. JSONB GIN indexes on `request_headers` and
  `response_headers` for portal filtering.

The two-table layout keeps the summary row free of multi-KB JSONB so
time/route/identity queries stay fast; the payload join only runs when
an operator drills into a single event in the portal.

`ON DELETE CASCADE` from `audit_payloads.event_id` to `audit_events.id`
keeps retention cleanup atomic.

## Retention

```yaml
audit:
  retention_days: 7
```

Default is 7 days (lower than mcp-test's 30 — api-test responses can be
much larger; export endpoints emit 100 MiB bodies that inflate the
payload table fast). A future migration adds a periodic cleanup job;
for now, prune manually:

```sql
DELETE FROM audit_events WHERE ts < now() - interval '7 days';
-- audit_payloads cascades.
```

## Body capture caps

Bodies that exceed `audit.max_payload_bytes` (default 1 MiB) are
truncated and the matching `request_truncated` / `response_truncated`
flag is set. The captured prefix is what's stored. Operators in
privacy-sensitive deployments can set:

```yaml
audit:
  capture_payloads: false   # only the audit_events summary row
  # or
  capture_payloads: true
  capture_headers: false    # bodies but no headers
```

## Operations

- **Pre-flight smoke** before pointing at production:
  ```bash
  pg_isready -h <host> -U api -d apitest
  ```
- **Migration history** lives in `schema_migrations` (golang-migrate
  default).
- **Down migrations** ship for every up; rollback works via `migrate -path … -database … down 1`. The binary itself only runs `up`.
- **Connection pool tuning**: defaults (25 open, 5 idle, 1h lifetime)
  are conservative. For high-throughput deployments, raise
  `max_open_conns` and lower `conn_max_lifetime` to rotate connections
  faster.
