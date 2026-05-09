---
title: Portal
description: The embedded React SPA for browsing the audit log, calling endpoints from a Try-It form, managing API keys, and viewing the OpenAPI document.
---

# Portal

The portal is a React 19 + Vite + Tailwind 4 SPA, compiled into the
binary via `go:embed`. It mounts at `/portal/` when `portal.enabled` is
true; the portal API lives at `/api/v1/portal/*` and is gated by an
operator session cookie established via OIDC PKCE.

!!! note "Lands in M3"
    The portal binary support is in place (the Go side mounts
    `internal/ui/dist` if it has an `index.html`); the SPA itself
    arrives in M3. Until then, point `portal.enabled` to `false` and
    use the curl examples in [Quickstart](../getting-started/quickstart.md)
    or hit Postgres directly for audit queries.

## Pages

| Page | Purpose |
| --- | --- |
| **Dashboard** | Requests/min, p50/p95 latency, status-code histogram, top routes by error rate. |
| **Endpoints** | Catalog of every registered route (method/path/group/auth filter) with a Try-It panel that POSTs through the portal API to the local mux. |
| **Audit** | Filterable, paginated event view; click a row for the full request/response drawer with redaction overlays. |
| **API Keys** | Create / revoke Postgres-backed bcrypt keys. Plaintext shown once. |
| **Config** | Read-only YAML of the running server, with secrets masked. |
| **Discovery** | Redoc/Swagger UI iframe over `/openapi.json`; click-to-copy connection-registration YAML for the Plexara admin API. |
| **About** | Build info + "test against Plexara" cheat sheet. |

## Authentication

Two paths reach portal data:

- **Browser session** — operator hits `/portal/`, gets redirected to
  the IdP, completes OIDC PKCE, returns with a session cookie.
  Subsequent portal API calls use the cookie.
- **API key** — paste an `X-API-Key` value into the portal login screen
  for headless operators (CI dashboards, kiosks). The portal API
  accepts both schemes.

The portal session is *separate* from the inbound auth chain that
gates `/v1/*`. An operator can have a portal session without any of
the gateway's connection credentials.

## Try-It

Click any endpoint in the catalog to open a per-route Try-It panel:

- Form fields generated from the route's input schema (path params,
  query params, headers, body).
- The form POSTs to a `/api/v1/portal/tryit/{group}/{route}` endpoint
  that proxies the call into the local mux. The result lands in the
  portal's audit feed exactly like an external call would, tagged
  `source: portal-tryit`.
- Errors and redactions in the response render the same way they would
  for a Plexara-forwarded call, so what the operator sees in the portal
  matches what the gateway sees over the wire.

## Inspection drawer

Clicking an audit row opens a side panel:

- **Overview** — identity, status, route, durations.
- **Request** — full URL with query, headers, body. Sensitive headers
  show with an "[redacted]" overlay; clicking the overlay reveals the
  stored value (which is `[redacted]` literally — the secret was never
  stored).
- **Response** — same shape for the outbound side.
- **Replay** — re-issue the same request through the portal API,
  preserving the original event id in the new row's `replayed_from`
  column for traceability.

## SSE live tail

The Audit page subscribes to a server-sent-events stream of newly
written audit events, surfaced as a fixed-cap buffer above the
historical filter view. The historical table stays still so the live
read doesn't blow away your filtered context.
