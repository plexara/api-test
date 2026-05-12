---
title: Deployment
description: Docker, Kubernetes, distroless image, healthcheck, graceful shutdown.
---

# Deployment

api-test is a single static Go binary plus a Postgres dependency. The
operational surface is small on purpose; treat it like any standard
HTTP service.

## Container image

`ghcr.io/plexara/api-test:latest` is a `gcr.io/distroless/static-debian12:nonroot`
base with the binary at `/usr/local/bin/api-test`. The default
entrypoint runs the binary against `/etc/api-test/api-test.yaml`; mount
your config there.

Multi-arch tags: linux/amd64, linux/arm64. Image is signed via cosign
on tag.

```bash
docker run --rm -p 8080:8080 \
  -v $(pwd)/configs/api-test.live.yaml:/etc/api-test/api-test.yaml:ro \
  -e APITEST_DEV_KEY=... \
  -e APITEST_DB_URL=postgres://api:api@postgres:5432/apitest?sslmode=disable \
  ghcr.io/plexara/api-test:vX.Y.Z
```

## Healthcheck

The binary doubles as its own healthcheck so the distroless image
doesn't need curl/wget.

```bash
api-test --healthcheck
echo $?  # 0 on 200 from /healthz, non-zero otherwise
```

The Dockerfile wires this in:

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/api-test", "--healthcheck"]
```

Override the probe URL via `APITEST_HEALTHCHECK_URL` when the binary
listens on a non-default port.

## Graceful shutdown

On SIGINT or SIGTERM:

1. Flip `/readyz` to 503 (load balancer should drain).
2. Sleep `server.shutdown.pre_shutdown_delay` (default 2s) so LB
   notices.
3. Call `http.Server.Shutdown` with a `server.shutdown.grace_period`
   timeout (default 25s); in-flight requests get to finish.
4. Close the audit `AsyncLogger` (drains the buffer to Postgres).
5. Close the database pool.

A second SIGINT short-circuits the pre-shutdown delay so an impatient
operator can force-quit.

## Liveness vs readiness

| Probe | What it checks | Status |
| --- | --- | --- |
| `/healthz` | Process is alive. | 200 always. |
| `/readyz` | Server is accepting traffic. | 200 normally; 503 during shutdown drain. |

For Kubernetes:

- **Liveness probe**: `/healthz`. Restart on failure.
- **Readiness probe**: `/readyz`. Pull from service endpoints on
  failure.
- **Startup probe**: `/healthz`, with a generous `failureThreshold`,
  so migrations have time to run on first boot.

## Kubernetes example

A self-contained example manifest set lives at
[`examples/kubernetes/`](https://github.com/plexara/api-test/tree/main/examples/kubernetes)
(landing in M5). It deploys api-test plus Postgres, configures an
nginx ingress with cert-manager, and seeds a single API key from a
Secret.

```bash
kubectl apply -f examples/kubernetes/
kubectl -n api-test get pods
```

For production deployments, replace the embedded Postgres with a
managed instance (RDS, Cloud SQL, Crunchy Bridge) and pin a stable
container image tag.

## Resource sizing

api-test has a small, predictable footprint:

- ~30 MiB RSS at idle.
- ~60–80 MiB RSS under sustained 1 krps load with payload capture on.
- ~1 ms middleware overhead per request (RequestID + AccessLog +
  Identity + Audit) on a 2024-class CPU. Audit DB write is async; the
  request path doesn't wait for it.

Sized appropriately for a 0.1–0.5 vCPU / 128–256 MiB request, with
limits at 1 vCPU / 512 MiB to absorb burst.

## Logging

Structured JSON via [slog](https://pkg.go.dev/log/slog), written to
stderr. Override the level via
`LOG_LEVEL=debug|info|warn|error`. Two line shapes you'll see most:

**Access log** (one per inbound request, emitted by `AccessLog`
middleware):

```json
{
  "time": "2026-05-11T22:18:03.421Z",
  "level": "INFO",
  "msg": "request",
  "method": "GET",
  "path": "/v1/whoami",
  "status": 200,
  "bytes": 142,
  "duration_ms": 3,
  "request_id": "01HXYZ7Q8N5F0VTA9KM3B2P0WJ",
  "auth_type": "apikey",
  "subject": "demo-key"
}
```

Field reference:

| Field | When present | Source |
| --- | --- | --- |
| `time`, `level`, `msg` | Always | `slog` core. |
| `method`, `path`, `status`, `bytes`, `duration_ms` | Always | `pkg/httpmw.AccessLog`. |
| `request_id` | Always | Preserved from `X-Request-Id` if the caller set one, otherwise a fresh UUID; echoed back on the response. |
| `auth_type`, `subject` | Only on routes that ran the per-route auth chain (`/v1/*` and the portal API) | Resolved identity holder seeded by `RequestID` and written by `Identity`. Health probes, well-known, and the SPA path are intentionally skipped. |

**Audit-pipeline lines** are emitted by the `AsyncLogger` worker, not
the request path:

- `audit write failed` (WARN) — a DB write returned an error. Includes
  `method`, `path`, `err`.
- `audit buffer full; dropping events` (WARN) — emitted at the 1st,
  1001st, 2001st, … drop with the cumulative `dropped_total`. If you
  see this regularly, raise the buffer size or scale Postgres.

### Correlating one request across systems

`request_id` is the join key:

1. Caller sends `X-Request-Id: <id>` (or doesn't — api-test will mint
   one and put it on the response).
2. api-test echoes `X-Request-Id` on the response.
3. The access-log line carries the same `request_id`.
4. The `audit_events` row stores it in column `request_id`.

In Plexara's own audit log, look up the same `request_id` to see what
the gateway forwarded vs. what the upstream received.

```sql
-- Look up one request end-to-end
SELECT ts, method, path, status, duration_ms, auth_type, subject
FROM audit_events
WHERE request_id = '01HXYZ7Q8N5F0VTA9KM3B2P0WJ';
```

## Metrics

api-test does not expose a `/metrics` endpoint today, and there are no
current plans to add one — the audit table is the canonical
observability surface, and the structured access log covers what
Prometheus would. Derive request-rate / latency / error-rate metrics
from either source:

- **Access log** — pipe the JSON lines into your log pipeline and
  aggregate on `path`, `status`, `duration_ms`, and `auth_type`.
- **Audit table** — richer (full headers, payload sizes, identity),
  cheap to query for ad-hoc analysis:

```sql
-- p50/p95 latency, last hour, by endpoint group
SELECT endpoint_group,
       percentile_cont(0.5)  WITHIN GROUP (ORDER BY duration_ms) AS p50,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95,
       count(*)
FROM audit_events
WHERE ts > now() - interval '1 hour'
GROUP BY endpoint_group;
```
