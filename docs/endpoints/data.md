---
title: Data endpoints
description: /v1/fixed/{key}, /v1/sized?bytes=N, /v1/lorem?words=N&seed=S — deterministic outputs for testing dedup, response-size handling, and caching boundaries.
---

# Data

The `data` group returns deterministic bodies. Same input → same output,
forever. Useful for cache hits, dedup logic, bitwise equality
assertions, and gateway response-size handling.

Source: [`pkg/endpoints/data`](https://github.com/plexara/api-test/tree/main/pkg/endpoints/data).

## `fixed`

```http
GET /v1/fixed/{key}
```

Returns a body deterministically derived from the `{key}` path
parameter. Same key → same body, every time.

Response (200):

```json
{
  "key": "hello",
  "hash": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
  "body": "fixed[hello]: 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
}
```

`hash` is `sha256(key)` rendered hex; `body` is a human-readable
restatement. Use either for assertions.

### What it proves

- Cache hit/miss behavior in the gateway. If the gateway caches by
  `(method, path)`, two calls to `/v1/fixed/hello` should produce one
  upstream call (the second a cache hit).
- Dedup logic. If the gateway dedups concurrent identical requests,
  the audit log shows one upstream call for N concurrent client calls.
- Response equality across runs. Snapshots remain valid forever.

### Curl

```bash
curl -s http://localhost:8080/v1/fixed/hello -H "X-API-Key: $KEY" | jq
curl -s http://localhost:8080/v1/fixed/world -H "X-API-Key: $KEY" | jq
```

## `sized`

```http
GET /v1/sized?bytes=N
```

Returns exactly `N` bytes in the `body` field of a JSON envelope. The
content is the lowercase ASCII alphabet repeated; not random.

Response (200):

```json
{ "bytes": 64, "body": "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl" }
```

Bounds:

- `0 <= bytes <= 32 MiB` (32×1024×1024). Larger sizes belong on the
  export endpoint group (M4+), which streams to the asset store
  instead of allocating in memory.
- `bytes < 0` or non-integer → 400.

### What it proves

- Gateway response-size limits. Set `max_response_bytes` on the Plexara
  connection to 1 MiB and call `?bytes=2097152` — the gateway should
  surface the truncation hint without crashing.
- Streaming buffer behavior. Large bodies are written via streaming
  `Write` calls, not buffered up front, so the gateway's reader has to
  cope with multi-chunk reads.
- Audit truncation. With `audit.max_payload_bytes: 1048576`, a 2 MiB
  response should write `response_truncated: true`.

### Curl

```bash
# Exactly 1 KiB
curl -s "http://localhost:8080/v1/sized?bytes=1024" -H "X-API-Key: $KEY" | jq -r '.body | length'
# → 1024

# Force a 400
curl -s -o - -w "STATUS=%{http_code}\n" "http://localhost:8080/v1/sized?bytes=abc" -H "X-API-Key: $KEY"
```

## `lorem`

```http
GET /v1/lorem?words=N&seed=S
```

Returns `N` words of seeded fake-Latin prose. Same seed → same body.
Without a seed, every call differs (PRNG seeded from
non-deterministic state).

Response (200):

```json
{ "words": 5, "body": "Ad excepteur anim sint laborum." }
```

Defaults and caps:

- `words <= 0` (omitted) → defaults to 50.
- `words > 5000` → capped at 5000.
- `seed` is hashed (FNV-64) twice with different salts to seed a
  PCG generator, so different seeds give independent streams.

### What it proves

- Reproducible "real-looking" content fixtures. Useful for snapshots
  that depend on natural-language byte distributions (compression
  testing, text-content gateway middlewares).
- Determinism with a non-trivial body. Unlike `fixed`, the body
  contains spaces and punctuation, so byte-equal assertions exercise
  more of the response path.

### Curl

```bash
curl -s "http://localhost:8080/v1/lorem?words=20&seed=cat" -H "X-API-Key: $KEY" | jq
curl -s "http://localhost:8080/v1/lorem?words=20&seed=cat" -H "X-API-Key: $KEY" | jq
# Same output both times.

curl -s "http://localhost:8080/v1/lorem?words=20&seed=dog" -H "X-API-Key: $KEY" | jq
# Different output (different seed).
```
