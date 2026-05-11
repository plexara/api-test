---
title: Export
description: Large/long-running endpoints intended as targets for the Plexara API gateway's api_export tool. Exercise the gateway's body-size and slow-first-byte handling.
---

# Export

Three endpoints designed to stress the gateway differently:

| Method | Path | What it stresses |
| --- | --- | --- |
| `GET` | `/v1/export/big-body?size_kb=N&seed=S` | Large response body forwarding. |
| `GET` | `/v1/export/csv?rows=N&seed=S` | Non-JSON content type, large text. |
| `GET` | `/v1/export/long-running?duration_ms=N` | Slow first-byte. |

## Determinism

`big-body` and `csv` both fill their rows with
`hex(sha256(seed:index))[:16]`. Same `(seed, index)` produces the same
value across runs, across endpoints, and across builds. The same
fixture data shows up in the `pagination` group too, by design.

## Bounds

| Parameter | Default | Max |
| --- | --- | --- |
| `size_kb` | 64 | 10240 (10 MiB) |
| `rows` | 1000 | 250000 |
| `duration_ms` | 1000 | 60000 (60 s) |

Values outside the bounds return `400`. The caps are fixture-side
defense against runaway test invocations, not a contract — real
`api_export` traffic moves well above these in production.

## Context cancellation

Both `/v1/export/big-body` and `/v1/export/csv` check
`r.Context().Done()` between rows and stop writing when the client
disconnects. `/v1/export/long-running` uses a `select` on a timer
plus the context, so a 60-second wait aborts instantly on disconnect
instead of pinning a goroutine.

## Examples

```bash
# 64 KiB JSON array (default size, with a fixed seed).
curl -s 'http://localhost:8080/v1/export/big-body?seed=fixed' | wc -c

# 1000-row CSV.
curl -s 'http://localhost:8080/v1/export/csv?rows=1000&seed=fixed' | head -3
# index,value
# 0,<hex>
# 1,<hex>

# 5-second slow-first-byte. Useful for gateway request-timeout testing.
curl -s 'http://localhost:8080/v1/export/long-running?duration_ms=5000'
# {"slept_ms":5000}
```

## Why this exists

The Plexara gateway's `api_export` tool streams large upstream responses
to an asset store rather than buffering them in memory. A misconfigured
gateway can break this in two ways: by truncating the body when its
in-memory cap fires (`big-body` exposes this), or by timing out the
upstream call before the first byte arrives (`long-running` exposes
this). The `csv` endpoint stresses the content-type code path that
`api_export` clients use when they ask for non-JSON exports.
