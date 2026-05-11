---
title: Streaming
description: Chunked, Server-Sent Events, and NDJSON endpoints for verifying gateway behavior on streaming responses.
---

# Streaming

Three endpoints, one per common streaming wire format. Each one's body
is deterministic from `(count, seed)` so a gateway client can replay a
stream and bit-compare what came back.

| Method | Path | Content-Type | Purpose |
| --- | --- | --- | --- |
| `GET` | `/v1/streaming/chunked` | `text/plain; charset=utf-8` | Transfer-Encoding: chunked, N text lines, one chunk per line. |
| `GET` | `/v1/streaming/sse` | `text/event-stream` | N SSE events with `id:` and `data:` fields; data is JSON. |
| `GET` | `/v1/streaming/ndjson` | `application/x-ndjson` | N newline-delimited JSON objects. |

All three share the same query parameters.

## Query parameters

| Parameter | Default | Bounds | Description |
| --- | --- | --- | --- |
| `count` | `5` | `0` to `1000` | Number of items to emit. `0` produces an empty body but still returns `200`. |
| `delay_ms` | `0` | `0` to `5000` | Server-side delay between items, for exercising client read timeouts. Does not affect content. |
| `seed` | `""` | any string | Seed for the deterministic word picker. Same `(count, seed)` reproduces the same body. |

Invalid values (non-integer, negative, over the bound) return `400`.

## Determinism

Each emitted item carries an index (`0`-based) and a word from a fixed
26-word dictionary. The word picked at index `i` is a stable function of
`(seed, i)` — re-seeded per item, so requesting index 7 of a 100-item
stream returns the same word it would in a 10-item stream.

Two requests with the same `(count, seed)` produce bit-identical bodies.
Different seeds produce different bodies.

## Examples

```bash
# Chunked: 3 lines.
curl -s 'http://localhost:8080/v1/streaming/chunked?count=3&seed=fixed'
# chunk 0: uniform
# chunk 1: whiskey
# chunk 2: kilo

# SSE: 2 events.
curl -s 'http://localhost:8080/v1/streaming/sse?count=2&seed=fixed'
# id: 0
# data: {"id":0,"word":"uniform"}
#
# id: 1
# data: {"id":1,"word":"whiskey"}

# NDJSON: 2 lines, with 200ms delay between them.
curl -Ns 'http://localhost:8080/v1/streaming/ndjson?count=2&seed=fixed&delay_ms=200'
# {"index":0,"word":"uniform"}
# {"index":1,"word":"whiskey"}
```

## Why this exists

A gateway proxy can quietly break streaming responses in three ways: by
buffering the entire body before flushing, by stripping `Transfer-Encoding:
chunked`, or by rewriting `text/event-stream` so the EventSource API
client gives up. These endpoints let you assert end-to-end that the proxy
preserves the wire format, the flush boundaries, and the content type.
