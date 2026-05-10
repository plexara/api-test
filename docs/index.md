---
title: api-test
template: home.html
hide:
  - navigation
  - toc
  - footer
---

# api-test

A controllable HTTP REST fixture, built specifically as an upstream for
testing API gateways end-to-end.

The endpoints it exposes are intentionally boring; they return
predictable output for predictable input. The point isn't what they
*do*; the point is how they let you verify a gateway in front of them
is doing the right things: forwarding identity, redacting credentials,
detecting pagination, surfacing errors, enforcing timeouts, and so on.
Every request is captured in a Postgres-backed audit log, so a tester
can compare what the client sent through the gateway, what reached
this server, and what came back.

It is also an opinionated reference for building production-quality
HTTP test fixtures in Go: typed endpoint groups, an in-tree OpenAPI
generator, OIDC inbound auth, audit logging, embedded React portal.

[Get started](getting-started/quickstart.md){ .md-button .md-button--primary }
[Source on GitHub](https://github.com/plexara/api-test){ .md-button }

## What's inside

<div class="grid cards" markdown>

-   :material-api:{ .lg } **Endpoint groups by behavior**

    Identity (whoami, headers), data (deterministic fixtures: fixed,
    sized, lorem), failure modes (status, slow, flaky), and a generic
    echo. More groups (pagination styles, methods, security probes,
    export targets) land in upcoming releases.

-   :material-shield-key:{ .lg } **Real inbound auth, four ways**

    File-loaded API keys (header or query placement, constant-time
    compare), bcrypt-hashed Postgres-backed keys, static bearer tokens,
    and OIDC JWT validation against an external IdP. Matches every
    auth mode the Plexara API gateway forwards.

-   :material-database-search:{ .lg } **Audit log of every request**

    Postgres-backed timeline with sanitized headers, query params,
    request and response bodies, identity, latency, and status. Browse,
    filter, and chart it in the embedded React portal.

-   :material-file-document-outline:{ .lg } **Self-describing OpenAPI**

    Every route is published in an OpenAPI 3.x document at
    `/openapi.json`, generated in-tree from the same metadata the
    portal uses, so the gateway's `api_list_endpoints` tool sees an
    exact contract.

-   :material-page-next:{ .lg } **Pagination, the gateway recognizes**

    One endpoint per cursor style the gateway's pagination detector
    recognizes: RFC 5988 `Link` headers, OData `@odata.nextLink`, and
    the common cursor field variants. Negative tests included so
    detection failures are falsifiable.

-   :material-alpha-p-circle:{ .lg } **By Plexara**

    [Plexara](https://plexara.io) is a unified MCP + API gateway with
    configurable enrichment built in. api-test is what we use to
    verify Plexara's API-gateway behavior end-to-end; we ship it as
    OSS so anyone building API gateways can use the same fixture.

</div>

## Why a separate test fixture?

Validating an API gateway means changing one thing on the gateway (an
auth policy, a rate-limit rule, a header rewrite) and observing the
diff at the upstream. To do that observably, the upstream has to be
predictable. api-test gives you that:

- Endpoints that return the same body for the same input
  ([`/v1/fixed/{key}`](endpoints/data.md#fixed),
  [`/v1/lorem`](endpoints/data.md#lorem) with a seed).
- Endpoints that fail on demand with any HTTP status
  ([`/v1/status/{code}`](endpoints/failure.md#status)) and on a schedule
  ([`/v1/slow`](endpoints/failure.md#slow),
  [`/v1/flaky`](endpoints/failure.md#flaky) seeded for reproducibility).
- Endpoints that echo identity and headers so you can confirm what's
  being forwarded ([`/v1/whoami`](endpoints/identity.md#whoami),
  [`/v1/headers`](endpoints/identity.md#headers)).

Pair that with the audit log and you can write end-to-end assertions
about gateway behavior without running fragile real-data fixtures.

## Where to next

- New here? [Quickstart](getting-started/quickstart.md) gets you the
  binary running in under a minute (anonymous mode today; the full
  Postgres + Keycloak + portal stack lands with M3).
- Configuring a deployment? [YAML reference](configuration/reference.md)
  documents every key with its default and environment override.
- Wiring api-test into Plexara? [Register with Plexara](getting-started/register-with-plexara.md)
  walks each supported auth mode.
- Validating a gateway?
  [Testing a gateway](operations/gateway-testing.md) walks through
  what each endpoint group proves.
