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

The same handler serves every verb. The supported matrix is fixed at
seven verbs — the gateway-testing patterns these probe are mostly
about the seven common-case verbs surviving proxy traversal.

## Response shape

```json
{
  "method": "PATCH",
  "path": "/v1/method/echo",
  "query": { "foo": ["1", "2"] }
}
```

| Field | Notes |
| --- | --- |
| `method` | The verb the server observed. Should equal the verb the client sent — that's the assertion. |
| `path` | Always `/v1/method/echo`. Confirms the gateway didn't rewrite the path along with the method. |
| `query` | The parsed query string. `omitempty` — absent on requests with no query. Use this to assert the gateway preserved query params under unusual verbs (e.g., a `DELETE` with a `?reason=...` parameter). |

Two verbs have special-case behavior:

- **`HEAD`** — returns headers only; the body is suppressed at the HTTP
  layer (Go's `http.ResponseWriter` automatically discards the body on
  `HEAD`). The response is byte-equivalent to the `GET` headers
  otherwise. Use `curl -I` or `curl -is | head -1` to inspect.
- **`OPTIONS`** — returns the body plus an `Allow` header listing every
  supported verb:

  ```http
  HTTP/1.1 200 OK
  Allow: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS
  Content-Type: application/json
  ```

  Useful to confirm the gateway forwards `OPTIONS` rather than
  intercepting it for CORS handling.

## Unregistered verbs

`CONNECT`, `TRACE`, `LINK`, `UNLINK`, `PROPFIND`, and other less-common
verbs are not registered. Go's `http.ServeMux` answers them with
`405 Method Not Allowed` because *other* verbs are registered for the
same path. The 405 itself is informative — it tells you the gateway
forwarded the request, just to a path that doesn't accept that verb.

```bash
curl -is -X CONNECT http://localhost:8080/v1/method/echo | head -1
# HTTP/1.1 405 Method Not Allowed
```

If the gateway *blocks* `CONNECT`/`TRACE` upstream (most should), you
won't see a 405 — you'll see whatever the gateway returns for a
blocked verb. That's also a useful signal.

## Examples

```bash
# Verb preservation
curl -s -X PATCH http://localhost:8080/v1/method/echo
# {"method":"PATCH","path":"/v1/method/echo"}

# HEAD: headers only, no body
curl -is -X HEAD http://localhost:8080/v1/method/echo | head -1
# HTTP/1.1 200 OK

# OPTIONS: body + Allow header
curl -is -X OPTIONS http://localhost:8080/v1/method/echo
# HTTP/1.1 200 OK
# Allow: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS
# Content-Type: application/json
# ...
# {"method":"OPTIONS","path":"/v1/method/echo"}

# Query preservation under non-GET
curl -s -X DELETE 'http://localhost:8080/v1/method/echo?reason=cleanup&id=42'
# {"method":"DELETE","path":"/v1/method/echo","query":{"id":["42"],"reason":["cleanup"]}}

# Unregistered verb
curl -is -X CONNECT http://localhost:8080/v1/method/echo | head -1
# HTTP/1.1 405 Method Not Allowed
```

## What to assert

For a gateway proxying api-test:

| Assertion | Means |
| --- | --- |
| Response `method` equals the client's verb | Gateway preserved the verb verbatim. |
| Response `query` matches the client's query string | Gateway didn't strip or reorder query params under this verb. |
| `OPTIONS` returns 200 with `Allow` header | Gateway didn't swallow the response inside a CORS pre-flight handler. |
| `HEAD` returns 200 with no body | Gateway didn't substitute a `GET` body on a `HEAD` response. |

## Audit-log perspective

Each verb registers as its own `EndpointMeta` (`method_get`,
`method_post`, …). The shared handler means the same Go code services
all seven, but the audit row's `route_name` carries the verb-specific
name, so you can `GROUP BY route_name` to count calls per verb:

```sql
SELECT route_name, count(*)
FROM audit_events
WHERE endpoint_group = 'methods'
  AND ts > now() - interval '1 hour'
GROUP BY route_name
ORDER BY 2 DESC;
```

If you expected the client to send 50 `PATCH`es through the gateway and
the count comes back showing 50 `POST`es, the gateway is rewriting the
verb — that's exactly the kind of finding this group is built to make
visible.

## Why this exists

Gateway proxies sometimes break verbs in subtle ways: rewriting `PATCH`
to `POST` to fit a stricter client library, swallowing `OPTIONS`
pre-flight responses inside a CORS layer, refusing `HEAD` because the
upstream handler doesn't register it explicitly, or stripping query
strings on verbs that "shouldn't have a body so probably shouldn't have
query either." This endpoint exposes every verb at one path so a
tester can spot any of those rewrites with a single curl loop.
