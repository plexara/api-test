---
title: Register with Plexara
description: Wire api-test in as a connection in a running Plexara API gateway, with one example per supported auth mode.
---

# Register with Plexara

To use api-test as the upstream for your Plexara API gateway, register
it as a connection. Plexara stores the connection definition (base URL,
auth mode, optional OpenAPI spec) and exposes it to its callers via
`api_invoke_endpoint`, `api_list_endpoints`, and `api_export`.

This page documents how to register api-test under every inbound auth
mode it supports. The Plexara admin API surface used here is described
in Plexara's own docs; api-test ships an example payload at
[`examples/plexara-connection.yaml`](https://github.com/plexara/api-test/blob/main/examples/plexara-connection.yaml).

The examples below assume:

- api-test is reachable at `http://api-test.local:8080` (substitute your
  hostname).
- The Plexara admin API lives at `https://plexara.example.com/api/v1/admin/api-gateway/connections`.
- You have an admin token in `$PLEXARA_ADMIN_TOKEN`.

## Auth mode: `none`

The simplest case. No credential is sent; api-test must run with
`auth.allow_anonymous: true`. Only useful for development.

```bash
curl -s -X POST "$PLEXARA_ADMIN/connections" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "api-test-anon",
    "base_url": "http://api-test.local:8080",
    "auth_mode": "none"
  }'
```

## Auth mode: `bearer`

Plexara forwards `Authorization: Bearer <token>`. api-test validates
against the `bearer.tokens` list in its config (or against an OIDC
issuer if `oidc.enabled` is true; see below).

```bash
curl -s -X POST "$PLEXARA_ADMIN/connections" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "api-test-bearer",
    "base_url": "http://api-test.local:8080",
    "auth_mode": "bearer",
    "credential": "'"$APITEST_DEV_BEARER"'"
  }'
```

api-test config:

```yaml
bearer:
  tokens:
    - { name: "devbearer", token: "${APITEST_DEV_BEARER}" }
```

## Auth mode: `api_key` (header)

Plexara forwards the credential in a header (default `X-API-Key`,
customizable per connection). api-test validates against the
`api_keys.file` list and/or the bcrypt-hashed Postgres store.

```bash
curl -s -X POST "$PLEXARA_ADMIN/connections" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "api-test-apikey-header",
    "base_url": "http://api-test.local:8080",
    "auth_mode": "api_key",
    "api_key_placement": "header",
    "api_key_header": "X-API-Key",
    "credential": "'"$APITEST_DEV_KEY"'"
  }'
```

## Auth mode: `api_key` (query)

Plexara appends `?api_key=...` (or another configured query param) to
every request. api-test reads the same param.

```bash
curl -s -X POST "$PLEXARA_ADMIN/connections" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "api-test-apikey-query",
    "base_url": "http://api-test.local:8080",
    "auth_mode": "api_key",
    "api_key_placement": "query",
    "api_key_param": "api_key",
    "credential": "'"$APITEST_DEV_KEY"'"
  }'
```

## Auth mode: `oauth2_client_credentials`

Plexara performs the OAuth2 client-credentials exchange against the
configured token endpoint, then forwards the resulting JWT to api-test
as `Authorization: Bearer <jwt>`. api-test validates the JWT against
the IdP's JWKS (configured via `oidc.issuer`).

```bash
curl -s -X POST "$PLEXARA_ADMIN/connections" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "api-test-oauth-cc",
    "base_url": "http://api-test.local:8080",
    "auth_mode": "oauth2_client_credentials",
    "oauth2_token_url": "http://keycloak.local:8081/realms/api-test/protocol/openid-connect/token",
    "oauth2_client_id": "plexara-cc",
    "oauth2_client_secret": "...",
    "oauth2_scopes": "api-test"
  }'
```

api-test config:

```yaml
oidc:
  enabled: true
  issuer: "http://keycloak.local:8081/realms/api-test"
  audience: "api-test"
  allowed_clients: ["plexara-cc"]
```

## Auth mode: `oauth2_authorization_code`

Plexara performs an interactive auth-code flow against the IdP, persists
the resulting refresh token, and uses the access token to call api-test.
The api-test side is identical to client-credentials — JWT validation
against JWKS — but the gateway client is allowlisted as
`plexara-ac` instead of `plexara-cc`.

```bash
# Step 1: initiate the OAuth flow (returns an authorization URL the
# operator opens in a browser).
curl -s -X POST "$PLEXARA_ADMIN/connections/api-test-oauth-ac/oauth-start" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN"
# → { "authorization_url": "https://.../authorize?...", "state": "..." }

# Step 2: the operator opens the URL, completes login, and Keycloak
# redirects to Plexara's /api/v1/admin/api-gateway/oauth/callback
# endpoint. Plexara stores the resulting refresh token internally.
```

Update api-test's `oidc.allowed_clients` to include `plexara-ac`.

## OpenAPI spec

api-test publishes its OpenAPI 3.x document at
`/openapi.json` and `/openapi.yaml`. Pass the inline document into the
connection definition so the gateway's `api_list_endpoints` tool can
discover routes:

```bash
SPEC=$(curl -s http://api-test.local:8080/openapi.json | jq -c .)

curl -s -X POST "$PLEXARA_ADMIN/connections" \
  -H "Authorization: Bearer $PLEXARA_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"api-test\",
    \"base_url\": \"http://api-test.local:8080\",
    \"auth_mode\": \"api_key\",
    \"api_key_placement\": \"header\",
    \"credential\": \"$APITEST_DEV_KEY\",
    \"openapi_spec\": $SPEC
  }"
```

## Optional: self-registration

api-test can register itself with Plexara on startup. Default off; flip
the switch in your config:

```yaml
plexara:
  register:
    enabled: true
    admin_url: "${PLEXARA_ADMIN_URL}"
    auth_header: "Bearer ${PLEXARA_ADMIN_TOKEN}"
    connection_name: "api-test"
```

This couples the fixture to the gateway, so leave it off for production
deployments and use the curl flow above for one-shot setup.

## Verify

Once registered, exercise api-test through Plexara from any MCP client:

```text
api_invoke_endpoint(connection="api-test", method="GET", path="/v1/whoami")
  → { "subject": "...", "auth_type": "...", "key_name": "..." }

api_list_endpoints(connection="api-test", query="status")
  → operations matching "status" (e.g. /v1/status/{code})

api_invoke_endpoint(connection="api-test", method="GET", path="/v1/status/503")
  → status=503, body={"status":503,"message":"Service Unavailable"}
```

Cross-check by opening the api-test portal and watching the Audit page:
every Plexara call shows up with the resolved identity, method, path,
status, and latency.
