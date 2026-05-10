---
title: Authentication
description: Inbound auth modes (file API keys with header or query placement, Postgres-backed bcrypt keys, static bearer tokens, OIDC JWT validation) matching what the Plexara API gateway sends.
---

# Authentication

api-test validates *inbound* credentials — the credentials the Plexara
gateway forwards from its caller. The chain is:

1. **API key** — header (default `X-API-Key`) or query param (default
   `api_key`). Validated against the `api_keys.file` static list and,
   if enabled, the bcrypt-hashed Postgres store.
2. **Static bearer** — `Authorization: Bearer <token>` matched against
   the `bearer.tokens` static list.
3. **OIDC JWT** (M3+) — `Authorization: Bearer <jwt>` validated against
   the configured IdP's JWKS.
4. **Anonymous fallback** — when `auth.allow_anonymous: true` and no
   credential matched, requests proceed with an anonymous identity.

The first authenticator that finds its credential type in the request
decides the outcome. A bad credential **does not** fall through; the
chain returns 401 immediately. This prevents accidental cross-mode
matches (a typo'd JWT shouldn't accidentally pass the static-bearer
list).

## File API keys

Simplest, no DB required.

```yaml
auth:
  allow_anonymous: false

api_keys:
  header_name: "X-API-Key"
  query_param_name: "api_key"
  file:
    - { name: "devkey",  key: "${APITEST_DEV_KEY}",  description: "default dev key" }
    - { name: "ci",      key: "${APITEST_CI_KEY}",   description: "CI smoke tests" }
```

Lookup is constant-time (`subtle.ConstantTimeCompare` per row) so a
timing-side-channel attacker can't fingerprint which entry matched.

## Postgres-backed API keys

Bcrypt-hashed; CRUD'd via the portal API. Slow per row (bcrypt is
intentionally slow), so list scans cap at ~thousands of keys before
they stop being practical. For a test fixture, that's fine.

```yaml
api_keys:
  db:
    enabled: true

database:
  url: "${APITEST_DB_URL}"
```

The bcrypt store layers under the file store: file keys win, DB keys
are consulted on miss. To create a key, use the portal (M3+) or call
the admin API directly:

```bash
curl -s -X POST http://localhost:8080/api/v1/admin/api-keys \
  -H "X-API-Key: $APITEST_DEV_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"plexara-prod","description":"plexara production"}'
# → { "key": { "id": "...", "name": "plexara-prod", ... },
#     "plaintext": "at_..." }
```

The `plaintext` value is shown once and never persisted. Capture it,
hand it to the gateway, and don't ask for it again.

## Static bearer tokens

Mirror image of the API-key file store, against `Authorization: Bearer`.

```yaml
bearer:
  tokens:
    - { name: "devbearer", token: "${APITEST_DEV_BEARER}", description: "default dev bearer" }
```

Used when a Plexara connection is configured with `auth_mode: bearer`
and `credential: <token>`.

## OIDC JWT (M3+)

When the Plexara gateway uses `oauth2_client_credentials` or
`oauth2_authorization_code`, it exchanges with the IdP and forwards the
resulting access token to api-test. api-test validates the JWT against
the configured IdP's JWKS.

```yaml
oidc:
  enabled: true
  issuer: "http://keycloak.local:8081/realms/api-test"
  audience: "api-test"
  allowed_clients: ["plexara-cc", "plexara-ac"]
  clock_skew_seconds: 30
  jwks_cache_ttl: 1h
```

JWKS is cached in-process for `jwks_cache_ttl`. Validation checks:

- Signature against a key from the cached JWKS.
- `iss` matches `oidc.issuer`.
- `aud` contains `oidc.audience`.
- `azp` (or `client_id` claim, depending on IdP) is in `allowed_clients`.
- `exp` and `nbf` allow a `clock_skew_seconds` tolerance.

The Keycloak realm `dev/keycloak/api-test-realm.json` (M3) pre-seeds
two confidential clients (`plexara-cc` for client-credentials,
`plexara-ac` for auth-code) and a portal user (`dev` / `dev`).

## 401 responses

Every 401 carries an RFC 6750 `WWW-Authenticate` header so an HTTP
client can discover the auth scheme:

```http
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="api-test"
Content-Type: application/json

{"error":"missing credential"}
```

When the credential was supplied but invalid:

```http
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="api-test", error="invalid_token"
Content-Type: application/json

{"error":"invalid credential"}
```

## Anonymous mode

```yaml
auth:
  allow_anonymous: true
```

The chain still runs every authenticator; only when none match does it
fall back to anonymous. So a bad credential still returns 401 — only
*absent* credentials get the anonymous identity. This makes the fixture
safe to run with anonymous + a few static keys: clients that send a
valid key get their identity, clients that send nothing get anonymous,
clients that send a bad key get 401.

## Portal browser login (M3+)

The portal uses a standard OIDC PKCE flow: hit `/portal/`, redirect to
the IdP, callback at `portal.oidc_redirect_path`, set a session cookie.
The portal API checks the cookie; everything else still uses the
inbound auth chain.
