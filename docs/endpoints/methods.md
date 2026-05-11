---
title: Methods
description: Method-matrix endpoint that accepts every common HTTP verb at a single path and echoes the verb the server observed.
---

# Methods

A single path, every common verb. Lets a gateway test assert that the
HTTP method survives the proxy hop unchanged.

| Method | Path | Returns |
| --- | --- | --- |
| `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS` | `/v1/method/echo` | `{ "method": "POST", "path": "/v1/method/echo", "query": {...} }` |

`HEAD` returns headers only (per RFC 7231). `OPTIONS` returns the body
plus an `Allow` header listing every supported verb.

`CONNECT` and `TRACE` are not registered; Go's `http.ServeMux` answers
them with `405 Method Not Allowed` because other verbs are registered
for the same path.

## Examples

```bash
curl -s -X PATCH http://localhost:8080/v1/method/echo
# {"method":"PATCH","path":"/v1/method/echo"}

curl -is -X HEAD http://localhost:8080/v1/method/echo | head -1
# HTTP/1.1 200 OK

curl -is -X OPTIONS http://localhost:8080/v1/method/echo
# HTTP/1.1 200 OK
# Allow: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS

curl -s -X CONNECT http://localhost:8080/v1/method/echo
# (405 Method Not Allowed)
```

## Why this exists

Gateway proxies sometimes break verbs in subtle ways: rewriting `PATCH`
to `POST` to fit a stricter client library, swallowing `OPTIONS`
pre-flight responses inside a CORS layer, or refusing `HEAD` because
the upstream handler doesn't register it explicitly. This endpoint
exposes every verb at one path so a tester can spot any of those
rewrites with a single curl loop.
