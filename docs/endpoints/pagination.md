---
title: Pagination
description: Three deterministic paginated endpoints, one per cursor style the Plexara API gateway recognizes.
---

# Pagination

Three endpoints, one per pagination style commonly seen in REST APIs.
Each one slices the same synthetic dataset of `(id, value)` pairs, so
a gateway client can assert "the items I got from `$skip=20` match the
items I got from `?cursor=...` after walking the cursor 20 steps."

| Method | Path | Style | Pagination signal |
| --- | --- | --- | --- |
| `GET` | `/v1/pagination/link` | RFC 5988 Link header | `Link: <url>; rel="next"` (also first, prev, last). |
| `GET` | `/v1/pagination/odata` | OData v4 | `@odata.nextLink` in the body. |
| `GET` | `/v1/pagination/cursor` | Opaque cursor | Base64 `next_cursor` field; clients pass it back verbatim. |

## Shared item shape

Every endpoint returns items of this shape:

```json
{ "id": 7, "value": "7902699be42c8a8e" }
```

`value` is `hex(sha256(id)[:8])` — a stable function of `id` only. The
same id produces the same value across styles, builds, and process
restarts. This makes cross-style assertions falsifiable.

## Synthetic dataset

The dataset is just the integers `0` through `total-1` paired with
their `deterministicValue`. There is no real storage; the handlers
compute items on the fly. Default `total=100`; max `10000`.

## `/v1/pagination/link`

| Parameter | Default | Bounds | Description |
| --- | --- | --- | --- |
| `page` | `1` | `≥ 1` | 1-indexed page. |
| `per_page` | `10` | `1` to `1000` | Items per page. |
| `total` | `100` | `1` to `10000` | Synthetic dataset size. |

Response body:

```json
{
  "items": [...],
  "page": 2,
  "per_page": 10,
  "total": 100
}
```

`Link` header carries `first`, `last`, and conditionally `prev` and
`next` URLs (no `prev` on page 1; no `next` on the last page).
Requesting `page` past the end returns `400`.

## `/v1/pagination/odata`

| Parameter | Default | Bounds | Description |
| --- | --- | --- | --- |
| `$top` | `10` | `1` to `1000` | Items in this page. |
| `$skip` | `0` | `≥ 0` | Items to skip before this page. |
| `total` | `100` | `1` to `10000` | Synthetic dataset size. |

Response body (OData v4 shape):

```json
{
  "value": [...],
  "@odata.count": 100,
  "@odata.nextLink": "http://localhost:8080/v1/pagination/odata?$top=10&$skip=10&total=100"
}
```

`@odata.nextLink` is omitted on the last page. Requesting `$skip` past
the end returns `400`.

## `/v1/pagination/cursor`

| Parameter | Default | Bounds | Description |
| --- | --- | --- | --- |
| `cursor` | (empty) | base64 string | Opaque cursor from a prior response. |
| `limit` | `10` | `1` to `1000` | Items per page. |
| `total` | `100` | `1` to `10000` | Synthetic dataset size. |

Response body:

```json
{
  "items": [...],
  "next_cursor": "MTA"
}
```

`next_cursor` is omitted on the last page. Clients should treat the
cursor as opaque — its current implementation is `base64url(offset)`,
but that shape is not part of the contract and may change.

Malformed cursors return `400`.

## Why this exists

Gateway proxies can corrupt pagination three ways: by rewriting the
`Link` header host (breaking links that point back at the upstream),
by stripping unknown body fields like `@odata.nextLink` (breaking OData
clients), or by treating cursor values as opaque strings that need
re-encoding (breaking opaque cursors). These endpoints let you assert
end-to-end that all three signals survive the proxy hop bit-for-bit.
