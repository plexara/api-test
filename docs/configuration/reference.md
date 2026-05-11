---
title: YAML reference
description: Every api-test config key, what it does, the default value, and the environment variable that overrides it.
---

# YAML reference

api-test loads configuration from a single YAML file passed via
`--config`. Every key supports `${VAR}` and `${VAR:-default}`
interpolation; plain `$VAR` (no braces) is left untouched so Postgres
DSN-style strings round-trip unmodified.

The full reference document, with every knob and a comment per knob,
lives at [`configs/api-test.example.yaml`](https://github.com/plexara/api-test/blob/main/configs/api-test.example.yaml).
This page is the human-friendly tour.

## `server`

| Key | Default | Description |
| --- | --- | --- |
| `name` | `"api-test"` | Server display name; surfaces in the OpenAPI doc and the portal. |
| `address` | `":8080"` | Listener address. Standard Go `host:port`. |
| `base_url` | `"http://localhost:<port>"` | External base URL; used in generated OpenAPI servers list and any redirects. |
| `description` | (built-in) | Operator-facing prose served at `/` and in the OpenAPI `info.description`. |
| `read_header_timeout` | `10s` | `http.Server.ReadHeaderTimeout`. Mitigates slowloris. |
| `shutdown.grace_period` | `25s` | Time given to in-flight requests to drain after `Shutdown` is called. |
| `shutdown.pre_shutdown_delay` | `2s` | Pre-shutdown delay so load balancers see the readiness flip before connections start failing. |
| `tls.enabled` | `false` | If true, terminate TLS in-process. Most deployments terminate at the LB; leave off. |
| `tls.cert_file` / `tls.key_file` | `""` | PEM paths when `tls.enabled` is true. |

## `auth`

| Key | Default | Description |
| --- | --- | --- |
| `allow_anonymous` | `false` | Falls back to anonymous identity when no inbound credential matches. |
| `require_for_api` | `false` | **Reserved** for per-surface gating; the inbound chain is currently gated by `allow_anonymous` alone. The shipped `live.yaml` opts in to `true`. |
| `require_for_portal` | `false` | **Reserved**, same shape. The shipped `live.yaml` opts in to `true`. |

## `api_keys`

Inbound API-key authentication. Both the file source and the DB source
can be enabled; the chain consults file first, then DB.

| Key | Default | Description |
| --- | --- | --- |
| `header_name` | `"X-API-Key"` | Header the gateway sends the credential in. Customize per connection in Plexara if you use a different name. |
| `query_param_name` | `"api_key"` | Query param fallback for `auth_mode: api_key, placement: query`. |
| `file[]` | `[]` | Static list of `{name, key, description}` triples. Constant-time compare. |
| `db.enabled` | `false` | Enable the bcrypt-hashed Postgres-backed key store. Requires `database.url`. |

## `bearer`

Static bearer-token authentication for `auth_mode: bearer` connections.

| Key | Default | Description |
| --- | --- | --- |
| `tokens[]` | `[]` | List of `{name, token, description}` triples. Constant-time compare. |

## `oidc`

External OIDC IdP for JWT validation (`oauth2_client_credentials` and
`oauth2_authorization_code` Plexara connections; also the portal browser
login)..

| Key | Default | Description |
| --- | --- | --- |
| `enabled` | `false` | Turn on the OIDC validator. |
| `issuer` | `""` | Issuer URL; required when `enabled`. JWKS fetched from `${issuer}/.well-known/openid-configuration`. |
| `audience` | `""` | Expected `aud` claim. |
| `allowed_clients` | `[]` | Whitelist of `client_id` claim values. |
| `clock_skew_seconds` | `30` | Tolerance applied to `exp` and `nbf`. |
| `jwks_cache_ttl` | `1h` | How long to cache fetched JWKS. |
| `skip_signature_verification` | `false` | Disables signature checking. Requires `APITEST_INSECURE=1`; never use in production. |

## `database`

Postgres connection pool. Required when `audit.enabled` or
`api_keys.db.enabled` is true; otherwise optional.

| Key | Default | Description |
| --- | --- | --- |
| `url` | `""` | PostgreSQL DSN. Migrations run on boot via golang-migrate. |
| `max_open_conns` | `25` | `pgxpool.Config.MaxConns`. |
| `max_idle_conns` | `5` | `pgxpool.Config.MinConns`. |
| `conn_max_lifetime` | `1h` | `pgxpool.Config.MaxConnLifetime`. |

## `audit`

Per-request audit logging. The middleware writes one
`audit_events` row plus an optional `audit_payloads` sibling row per
inbound request (skipping `/healthz`, `/readyz`, well-known endpoints,
and the portal auth flow).

| Key | Default | Description |
| --- | --- | --- |
| `enabled` | `false` | Master switch. When false, the noop logger is used. |
| `retention_days` | `7` | Lower than mcp-test's 30 because api-test responses can be much larger; export endpoints emit 100 MiB bodies. |
| `redact_keys` | (sane default) | Substrings (case-insensitive) matched against header names and query keys; matches are written as `[redacted]`. |
| `capture_payloads` | `true` | Write the `audit_payloads` sibling row with bodies and headers. |
| `capture_headers` | `true` | Include redacted headers in the payload row. |
| `max_payload_bytes` | `1 MiB` | Per-side body capture cap. Larger bodies are truncated and the matching `_truncated` flag is set. |

Default `redact_keys`:

```yaml
- password
- token
- secret
- authorization
- api_key
- api-key
- credentials
- bearer
- cookie
- jwt
- session_id
- private_key
- passwd
```

## `portal`

The embedded React SPA + portal API..

| Key | Default | Description |
| --- | --- | --- |
| `enabled` | `false` | Mount the portal under `/portal/` and the portal API under `/api/v1/portal/*`. |
| `cookie_name` | `"api_test_session"` | Session cookie name. |
| `cookie_secret` | `""` | HMAC secret. Required when `enabled`. |
| `cookie_secure` | `false` | Set the `Secure` cookie attribute. Enable in TLS deployments. |
| `oidc_redirect_path` | `"/portal/auth/callback"` | OIDC PKCE callback path. |

## `endpoints`

Per-group toggles. Disabling a group removes its routes from the mux
and from the published OpenAPI doc.

| Key | Default |
| --- | --- |
| `identity.enabled` | `false` |
| `data.enabled` | `false` |
| `failure.enabled` | `false` |
| `echo.enabled` | `false` |
| `streaming.enabled` | `false` |
| `pagination.enabled` | `false` |
| `methods.enabled` | `false` |
| `security.enabled` | `false` |
| `export.enabled` | `false` |

## `plexara.register`

Optional self-registration with a running Plexara instance on startup.
Default off; for one-shot setup use the curl flow in
[Register with Plexara](../getting-started/register-with-plexara.md).

| Key | Default | Description |
| --- | --- | --- |
| `enabled` | `false` | POST a connection definition to `admin_url` on startup. |
| `admin_url` | `""` | Plexara admin API URL. Required when `enabled`. |
| `auth_header` | `""` | Authorization header value (e.g. `Bearer <token>`). |
| `connection_name` | `"api-test"` | Connection name to register under. |
