---
title: Testing a gateway
description: Patterns for asserting on the Plexara API gateway's auth-forwarding, redaction, pagination detection, error surfacing, and timeout enforcement using api-test as the upstream.
---

# Testing a gateway

api-test is built for the loop: change one thing on the gateway, hit
api-test through it, observe the diff. This page documents the
patterns each endpoint group makes possible.

The examples assume Plexara is reachable at
`https://plexara.example.com`, api-test is registered as a connection
named `api-test`, and the operator calls Plexara via an MCP client
(Claude Code, the SDK, raw JSON-RPC).

## Identity forwarding

**Question**: did the gateway forward the right credential under the
right transport?

```text
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/whoami")
  → { "subject": "...", "auth_type": "...", "key_name": "..." }
```

**Assertion**:

- `auth_type` matches the connection's registered `auth_mode`
  (`apikey`, `bearer`, `oauth2`).
- `subject` is the credential name api-test recognizes (the
  `name` field of the matching `api_keys.file` / `bearer.tokens` /
  the OIDC `sub` claim).

**Falsifies**:

- Mis-registered connection (auth_mode doesn't match what the gateway
  is actually sending).
- Stale credential (gateway is still using a rotated value).
- Auth-mode confusion (a misconfigured `oauth2` flow that falls back
  to a stale static token).

## Header pass-through

**Question**: which headers did the gateway forward, and which did it
strip or rewrite?

```text
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/headers",
                   headers={"X-Trace-Id": "abc", "X-Custom": "vendor"})
```

**Assertion** — open the api-test audit row:

- `request_headers["X-Trace-Id"] = ["abc"]` if the gateway forwards.
- `request_headers["X-Custom"] = ["vendor"]` if the gateway forwards.
- `request_headers["Authorization"]` is **not present** in the row
  (the gateway should set `Authorization` from the connection's
  registered credential, not pass through caller-supplied headers
  that override it).

**Falsifies**:

- Header injection (gateway lets caller-supplied `Authorization`
  through and api-test sees it instead of the registered credential).
- Header stripping (gateway drops a custom header you expected to
  forward).

## Status code surfacing

**Question**: does the gateway pass through upstream HTTP statuses
verbatim, or collapse them into tool-level errors?

```text
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/status/503")
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/status/418")
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/status/401")
```

**Assertion**:

- The gateway response envelope's `status` field carries the upstream
  status (503, 418, 401).
- The gateway does *not* mark the call as a tool-level error for
  upstream 4xx/5xx; that's an upstream HTTP response, not a transport
  failure.

**Falsifies**:

- Gateway collapses non-2xx to a tool error (loses information).
- Gateway maps 401 from upstream into "auth failed" at the gateway
  layer (misleading; the gateway's own auth is still fine).

## Timeout enforcement

**Question**: does the gateway respect its own `connect_timeout`,
`call_timeout`, and per-call `timeout_seconds`?

```text
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/slow?ms=8000",
                   timeout_seconds=2)
```

**Assertion**:

- The gateway returns `error: "timeout"` (or equivalent) within ~2s.
- api-test's audit row shows status 499 with `cancelled: true` (the
  gateway propagated the cancellation).

**Falsifies**:

- Gateway ignores `timeout_seconds` (waits the full 8s).
- Gateway times out but doesn't propagate cancellation (api-test
  finishes its 8s sleep and writes a 200 row anyway).

## Retry policy

**Question**: when does the gateway retry, and how many times?

```text
api_invoke_endpoint(connection="api-test", method="GET",
                   path="/v1/flaky?fail_rate=1&seed=demo&call_id=1")
```

**Assertion** — count audit rows for `path = "/v1/flaky"`:

- One row → no retry (gateway surfaces the failure immediately).
- N rows → gateway retried N-1 times.

**Falsifies**:

- Gateway retries on 503 when policy says it shouldn't.
- Gateway gives up after the wrong number of attempts.

## Redaction

**Question**: did the gateway log the credential anywhere?

Open the api-test audit drawer for a recent call. Confirm:

- `request_headers["Authorization"]` (or the gateway's chosen header)
  shows `[redacted]`, not the real value.
- `response_body` doesn't accidentally echo the credential back from
  somewhere upstream.

Then check Plexara's own audit log for the same call. The credential
should be `[redacted]` there too. If it's plaintext on either side,
the redaction policy isn't covering that key.

## Pagination detection

**Question**: did the gateway recognize the upstream's pagination
cursor?

api-test exposes one endpoint per cursor style the gateway's pagination
detector recognizes:

- `/v1/pagination/link` — RFC 5988 `Link: <…>; rel="next"` (also
  `first`, `prev`, `last`). Paged with `?page=&per_page=`.
- `/v1/pagination/odata` — OData v4 body field `@odata.nextLink` plus
  `@odata.count`. Paged with `?$top=&$skip=`.
- `/v1/pagination/cursor` — opaque base64 cursor in body field
  `next_cursor`. Paged with `?cursor=&limit=`.

All three slice the same deterministic synthetic dataset
(`hex(sha256(id)[:8])`), so the items returned for page 2 of the Link
endpoint should bit-match the items returned by walking the OData or
cursor endpoint to the same offset. See the
[Pagination endpoint reference](../endpoints/pagination.md) for the
full parameter table.

**Assertion**: gateway response envelope's `pagination` field:

- For each style, the cursor / next-page value should match what
  api-test put on the wire (no host rewrite, no re-encoding).
- Item bodies for the same `id` are byte-equal across all three styles.
- Requesting past the last page returns 400 from api-test; the gateway
  should surface that, not collapse it into a tool-level error.

## Snapshot fixtures

For deterministic regression suites, use the `data` group:

```text
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/fixed/regression-key-42")
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/lorem?words=20&seed=test")
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/sized?bytes=1024")
```

These bodies never change. Snapshot once; assert byte-equal forever.

## What lives where

| Concern | Where to look |
| --- | --- |
| What the client called Plexara with | Plexara's MCP audit log. |
| What Plexara forwarded to api-test | api-test's `audit_events` + `audit_payloads` rows. |
| What api-test sent back | Same payload row, response side. |
| What the client got back from Plexara | Plexara's MCP audit log, response section. |

The *diff* between "what the client sent" and "what api-test received"
is the gateway's contribution to the call. The diff between "what
api-test sent back" and "what the client got" is the gateway's
contribution to the response. Both directions are observable.
