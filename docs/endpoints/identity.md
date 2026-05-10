---
title: Identity endpoints
description: /v1/whoami and /v1/headers — verify the gateway forwards identity and headers (with redaction) the way you expect.
---

# Identity

The `identity` group surfaces what the inbound auth chain resolved and
what HTTP headers reached api-test. The bread-and-butter for verifying
gateway pass-through behavior.

Source: [`pkg/endpoints/identity`](https://github.com/plexara/api-test/tree/main/pkg/endpoints/identity).

## `whoami`

Returns the resolved inbound identity for the calling request.

```http
GET /v1/whoami
```

Response (200):

```json
{
  "subject": "devkey",
  "auth_type": "apikey",
  "key_name": "devkey"
}
```

Fields:

| Field | Description |
| --- | --- |
| `subject` | Canonical principal id: API key name, bearer token name, or OIDC `sub` claim. Empty for anonymous. |
| `email` | Best-effort, OIDC only. |
| `auth_type` | `anonymous`, `apikey`, `bearer`, or `oauth2`. |
| `key_name` | Display name of the matched credential. |
| `scopes` | OAuth2 scopes (or roles), if any. |
| `claims` | Raw OIDC claim map for debugging. |

### What it proves

- The gateway forwarded the right credential under the right transport
  (Bearer vs X-API-Key vs query param).
- The credential matched the expected api-test entry.
- `auth_type` is what the connection registration promised; a typo'd
  `oauth2_authorization_code` connection that comes through as `apikey`
  is a misconfiguration the response surfaces immediately.

### Curl

```bash
KEY=$APITEST_DEV_KEY

# X-API-Key header
curl -s http://localhost:8080/v1/whoami -H "X-API-Key: $KEY" | jq

# Query placement
curl -s "http://localhost:8080/v1/whoami?api_key=$KEY" | jq

# Bearer
curl -s http://localhost:8080/v1/whoami \
  -H "Authorization: Bearer $APITEST_DEV_BEARER" | jq

# Anonymous (only when auth.allow_anonymous: true)
curl -s http://localhost:8080/v1/whoami | jq
```

## `headers`

Returns every inbound HTTP header the request carried, with redaction
applied to anything matching `audit.redact_keys`.

```http
GET /v1/headers
```

Response (200):

```json
{
  "headers": {
    "Accept": ["*/*"],
    "X-Request-Id": ["8c5b...3f7a"],
    "X-Trace-Id": ["custom-trace-1"],
    "Authorization": ["[redacted]"],
    "X-Api-Key": ["[redacted]"]
  },
  "count": 5
}
```

Header names are normalized to canonical Go form (`X-API-Key` →
`X-Api-Key`); values are returned as arrays so multi-value headers
(`Accept-Language: en, fr`) round-trip faithfully.

### What it proves

- The gateway forwarded the headers you expected (custom tracing
  headers, content-type, accept).
- The gateway *added* headers you expected (`X-Request-Id`,
  `X-Forwarded-For`).
- The gateway *stripped* headers you expected stripped (depends on
  policy — Plexara's behavior is documented separately).
- Sensitive headers are redacted before they land in api-test's audit
  log; the response body is the same redacted form, so a screenshot
  doesn't leak the credential.

### Curl

```bash
curl -s http://localhost:8080/v1/headers \
  -H "X-API-Key: $APITEST_DEV_KEY" \
  -H "X-Trace-Id: trace-from-test" \
  -H "X-Custom-Vendor: anything" | jq
```

### Pairing with the audit log

The `audit_payloads.request_headers` JSONB column carries the same
redacted view. Cross-check that what the response shows matches what
the audit log stored — they're produced by the same `SanitizeHeaders`
helper, so any drift is a bug.
