---
title: Echo endpoint
description: ANY /v1/echo — generic catch-all that returns the request verbatim with auth headers redacted.
---

# Echo

A generic catch-all that returns the inbound request verbatim. Useful
for ad-hoc Try-It from the portal, debugging a Plexara connection
mid-flight without touching api-test code, or as a smoke test for any
HTTP behavior the more specific endpoint groups don't cover yet.

Source: [`pkg/endpoints/echo`](https://github.com/plexara/api-test/tree/main/pkg/endpoints/echo).

## Routes

```http
GET    /v1/echo
POST   /v1/echo
PUT    /v1/echo
PATCH  /v1/echo
DELETE /v1/echo
HEAD   /v1/echo
```

The same handler is mounted under every method so the OpenAPI doc lists
each verb separately (which lets the Plexara `api_list_endpoints` tool
see them as discoverable operations).

## Response

```json
{
  "method": "POST",
  "path": "/v1/echo",
  "query": { "foo": ["1", "2"], "bar": ["baz"] },
  "headers": {
    "Accept": ["*/*"],
    "Content-Type": ["application/json"],
    "X-Trace-Id": ["custom-1"],
    "Authorization": ["[redacted]"]
  },
  "body": { "hello": "world", "n": 42 },
  "body_size": 24
}
```

Field-by-field:

- `method` — verbatim.
- `path` — verbatim. (`{key}` would appear as `{key}` literally if you
  hit `/v1/echo/{key}`; this endpoint is mounted at the literal `/v1/echo`.)
- `query` — multi-valued; `?foo=1&foo=2` round-trips as `["1","2"]`.
- `headers` — canonical-cased, multi-valued, with redaction applied
  per `audit.redact_keys`.
- `body` — parsed as JSON if the body decodes; falls through to
  `body_raw_text` for non-JSON bodies.
- `body_size` — raw byte length, before parsing.

`HEAD /v1/echo` returns 200 with no body, as required by RFC 9110.

## What it proves

- Method support. Every method round-trips; if the gateway has
  per-method policy (e.g. block `DELETE`), you can verify the gateway
  correctly refuses without us seeing the request.
- Body forwarding. `Content-Type: application/json` bodies are parsed
  and re-rendered; the response is byte-identical to the gateway's
  serialization of the parsed object.
- Header pass-through. Same as `/v1/headers` but with the request body
  also visible.
- Query encoding. Multi-valued query params, URL-encoded special
  characters, empty values — all visible.

## Curl

```bash
KEY=$APITEST_DEV_KEY

# GET with query
curl -s "http://localhost:8080/v1/echo?foo=1&foo=2&bar=baz" \
  -H "X-API-Key: $KEY" -H "X-Trace-Id: t1" | jq

# POST JSON
curl -s -X POST http://localhost:8080/v1/echo \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"hello":"world","nested":{"a":[1,2,3]}}' | jq

# PUT plain text
curl -s -X PUT http://localhost:8080/v1/echo \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: text/plain" \
  --data 'not json: just bytes' | jq

# DELETE
curl -s -X DELETE http://localhost:8080/v1/echo -H "X-API-Key: $KEY" | jq

# HEAD
curl -s -I http://localhost:8080/v1/echo -H "X-API-Key: $KEY"
```

## Body cap

The handler reads up to 1 MiB of inbound body. Larger bodies are
truncated; `body_size` reports the captured prefix length. For
testing the gateway's handling of >1 MiB bodies, use the export
endpoint group (M4+).
