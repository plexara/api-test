---
title: Overview
description: What api-test is, who should use it, and why a separate HTTP test fixture matters when you're validating an API gateway.
---

# Overview

api-test is an HTTP REST server you point an API gateway at as a
controllable upstream. Its endpoints are intentionally simple and
deterministic. Their job is not to compute anything useful; their job
is to make a gateway's behavior observable.

The Plexara API gateway exposes three MCP tools to its callers
(`api_invoke_endpoint`, `api_list_endpoints`, `api_export`) and runs
each call through a registered HTTP connection. api-test is the upstream
behind that connection — the thing the gateway actually talks to.

## Who should use it

- **Plexara API gateway operators** validating connection registration,
  auth-mode handling, redaction, pagination detection, error surfacing,
  and timeout enforcement before hooking up production upstreams.
- **API gateway developers** (any vendor) needing a controllable
  upstream that doesn't fight back: every endpoint is documented, every
  body is reproducible, every failure mode is opt-in.
- **Anyone building HTTP API integrations** who wants a reference for
  audit logging, multi-mode inbound auth, in-tree OpenAPI generation,
  embedded portal patterns. The code is small and Apache 2.0.

## What's in the box

- **Endpoint groups** for identity (whoami, headers), deterministic
  data (fixed/sized/lorem), failure modes (status/slow/flaky), echo, and
  — landing in later releases — pagination styles, method matrix,
  security probes, large/streamed responses for `api_export`.
- **Inbound auth chain**: file API keys (header or query placement),
  bcrypt-hashed Postgres keys, static bearer tokens, and OIDC JWT
  validation. Matches every credential the Plexara gateway forwards.
- **Audit log**: every inbound request lands in Postgres with sanitized
  headers and bodies, the resolved identity, the response status and
  size, and the duration.
- **Portal**: React 19 SPA embedded in the binary; Dashboard,
  Endpoints with Try-It, Audit, API Keys, Config, Discovery (native
  OpenAPI 3.1 reference rendered from `/openapi.json`, with `/docs`
  Redoc available as a sibling).
- **OpenAPI document** at `/openapi.{json,yaml}`, generated in-tree
  from the registered endpoint metadata so it can't drift from the
  served routes.
- **Operational ergonomics**: `--healthcheck` self-probe, graceful
  shutdown with a configurable drain window, `${VAR:-default}` env
  interpolation, distroless container image.

## Why a separate fixture

You can't reliably test an API gateway against real upstreams. They
mutate. They rate-limit. Their pagination cursors expire. Their auth
tokens rotate. Their failure modes are accidental and rare.

api-test gives you the inverse of all of that:

- Same input → same output. Forever. Cache hits, dedup logic,
  bitwise comparison all work.
- Failures on demand. Want a 503? Hit `/v1/status/503`. Want a 1.2s
  upstream? Hit `/v1/slow?ms=1200`. Want intermittent failure at a
  reproducible rate? Hit `/v1/flaky?fail_rate=0.5&seed=demo&call_id=N`.
- Pagination on tap. One endpoint per cursor style the gateway
  recognizes. Negative tests included.
- Identity and header echoes. Confirm exactly what the gateway forwarded
  (and what it stripped) without consulting upstream logs you don't
  control.

## Sister project: mcp-test

[mcp-test](https://mcp-test.plexara.io/) is the same idea for the
**MCP gateway** half of Plexara: a controllable MCP server fixture with
twelve test tools, three auth methods, the same audit log surface, the
same portal shell. Use api-test to validate API-gateway behavior;
use mcp-test to validate MCP-gateway behavior.

## Next

- [Installation](installation.md) — download the binary or pull the
  container image.
- [Quickstart](quickstart.md) — `make dev` brings up the full
  Postgres + Keycloak + portal stack; `make dev-anon` skips both for
  fastest iteration.
- [Register with Plexara](register-with-plexara.md) — wire api-test in
  as a connection.
