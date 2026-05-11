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

Structured JSON via slog, written to stderr. Override the level via
`LOG_LEVEL=debug|info|warn|error`. Every line carries:

- `time` (RFC 3339 nano).
- `level`, `msg`.
- `method`, `path`, `status`, `bytes`, `duration_ms` for request lines.
- `request_id` for traceability (generated or preserved from `X-Request-Id`).
- `auth_type`, `subject` when the identity middleware ran.

## Metrics

Prometheus metrics endpoint lands in. Until then, derive metrics
from the structured access log or query the audit table:

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
