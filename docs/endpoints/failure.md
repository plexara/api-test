---
title: Failure mode endpoints
description: /v1/status/{code}, /v1/slow?ms=N, /v1/flaky?fail_rate=&seed=&call_id= — controlled failure modes for retry and timeout policy testing.
---

# Failure modes

The `failure` group produces controlled, predictable failures. Use them
to exercise the gateway's retry policy, timeout enforcement, and
error-surfacing behavior without depending on real upstreams that
misbehave only occasionally.

Source: [`pkg/endpoints/failure`](https://github.com/plexara/api-test/tree/main/pkg/endpoints/failure).

## `status`

```http
GET /v1/status/{code}
```

Returns the supplied HTTP status code (httpbin-style). The body is a
small JSON envelope documenting what was returned.

Response:

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{ "status": 503, "message": "Service Unavailable" }
```

Bounds: `100 <= code <= 599`. Anything outside the valid HTTP status
range returns 400.

### What it proves

- Status code surfacing. The gateway's `api_invoke_endpoint` should
  pass `status: 503` through verbatim, not collapse it into a tool-level
  error.
- 4xx vs 5xx differentiation. A 401 from upstream is meaningfully
  different from a 503; the gateway audit row should reflect that.
- Audit `success` flag. api-test's audit middleware marks
  `success: status >= 200 && status < 400`, so a 503 row shows
  `success: false` while a 304 shows `success: true`.

### Curl

```bash
for code in 200 201 204 301 400 401 404 418 429 500 502 503 504; do
  echo -n "code=$code → "
  curl -s -o - -w "%{http_code}\n" -H "X-API-Key: $KEY" \
    http://localhost:8080/v1/status/$code | tail -1
done
```

## `slow`

```http
GET /v1/slow?ms=N
```

Sleeps for `N` milliseconds before responding. Honors context
cancellation (HTTP/2 RST_STREAM, client disconnect, gateway timeout
firing).

Response (200, after the wait):

```json
{ "slept_ms": 1003, "requested_ms": 1000 }
```

Cancelled response (499; non-standard "client closed" status):

```json
{ "slept_ms": 47, "cancelled": true, "requested_ms": 5000 }
```

Bounds:

- `ms <= 0` → 0 (immediate response).
- `ms > 60000` → capped at 60000 (60s).

### What it proves

- Gateway connect-timeout vs call-timeout enforcement. Plexara's per-
  connection `connect_timeout` (default 10s, TCP+TLS) should *not*
  fire on `?ms=8000` (the connection is already established); the
  per-connection `call_timeout` (default 60s) and the per-call
  `timeout_seconds` argument are what should fire.
- Context propagation. When the gateway aborts because its timer
  fires, api-test should observe `r.Context().Done()` and return
  promptly with `cancelled: true`.

### Curl

```bash
# 1.2s upstream
time curl -s "http://localhost:8080/v1/slow?ms=1200" -H "X-API-Key: $KEY" | jq

# Provoke client cancel: ^C after a moment.
curl -s --max-time 1 "http://localhost:8080/v1/slow?ms=10000" -H "X-API-Key: $KEY"
# api-test will return 499 with cancelled: true.
```

## `flaky`

```http
GET /v1/flaky?fail_rate=R&seed=S&call_id=N
```

Returns 200 or 503 based on a deterministic roll. Same `(seed, call_id)`
always produces the same outcome.

Response (200):

```json
{ "failed": false, "roll": 0.234, "fail_rate": 0.5 }
```

Response (503):

```json
{ "failed": true, "roll": 0.812, "fail_rate": 0.5 }
```

Inputs:

- `fail_rate` — clamped to `[0, 1]`. `0` always passes, `1` always
  fails.
- `seed` — string fed into a PCG generator (FNV-64 twice with different
  salts).
- `call_id` — integer combined with seed; pass `0..N` to walk a
  reproducible failure pattern.

When `seed` is empty, the PRNG seeds from non-deterministic state and
results vary per call. For test fixtures, *always* set a seed.

### What it proves

- Retry policy. Set `fail_rate=0.5, seed=demo, call_id=1` — that call
  has a fixed outcome. Replay it through the gateway and the same
  retry path fires every time.
- Reproducible failure rates over a sample. Walk `call_id=0..99` with
  a fixed seed and rate; the failure count is deterministic across
  runs (≈ rate × 100, depending on PRNG roll distribution).
- Error categorization. Whatever the gateway tags 503s as in its own
  metrics is exercisable here.

### Curl

```bash
# Always-fail (rate=1)
curl -s -o - -w "STATUS=%{http_code}\n" \
  "http://localhost:8080/v1/flaky?fail_rate=1&seed=demo&call_id=1" \
  -H "X-API-Key: $KEY"
# → 503

# Reproducibility
for i in {1..5}; do
  curl -s -o - -w " call_id=$i status=%{http_code}\n" \
    "http://localhost:8080/v1/flaky?fail_rate=0.4&seed=demo&call_id=$i" \
    -H "X-API-Key: $KEY" >/dev/null
done
# Re-run; identical statuses.
```
