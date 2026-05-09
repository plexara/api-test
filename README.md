# api-test

A controllable HTTP REST fixture used to exercise API gateways (Plexara's
in particular). Sister project to [mcp-test](../mcp-test), which plays the
same role for the MCP gateway.

## Why

Plexara MCP exposes two gateway capabilities:

- **MCP gateway** — registers upstream MCP servers as connections. Tested
  by `mcp-test`.
- **API gateway** — registers upstream HTTP APIs as connections; exposes
  three MCP tools (`api_invoke_endpoint`, `api_list_endpoints`,
  `api_export`). Tested by **`api-test`** — this project.

`api-test` is the upstream HTTP fixture the API gateway calls. Endpoints
are deliberately simple and deterministic; their job is not to compute
anything useful, it's to make the gateway's behavior observable. Every
request will (M2+) be recorded in a Postgres-backed audit log so you can
compare what a client sent through Plexara, what reached this server, and
what came back.

## Endpoint groups (M1)

- **identity** — `GET /v1/whoami`, `GET /v1/headers`. Verify the gateway
  forwards identity, args, and HTTP headers (with redaction).
- **data** — `GET /v1/fixed/{key}`, `GET /v1/sized?bytes=N`,
  `GET /v1/lorem?words=N&seed=S`. Deterministic outputs for testing
  enrichment dedup, response-size handling, and caching.
- **failure** — `GET /v1/status/{code}`, `GET /v1/slow?ms=N`,
  `GET /v1/flaky?fail_rate=&seed=&call_id=`. Controlled failure modes
  for retry/timeout policy testing.
- **echo** — `ANY /v1/echo`. Generic catch-all that returns the request
  verbatim (with auth headers redacted).

Coming in later milestones: streaming (chunked, SSE, NDJSON), pagination
(Link, OData, cursor variants), method matrix, security probes, export
(large/long-running targets for `api_export`), the OpenAPI document,
inbound auth (bearer/api_key/OAuth2), audit log, web portal, mkdocs
site, and CI/release tooling.

## Quickstart

```bash
go run ./cmd/api-test --config configs/api-test.dev.yaml
# in another shell:
curl -s http://localhost:8080/v1/whoami
curl -s 'http://localhost:8080/v1/sized?bytes=64'
curl -s http://localhost:8080/v1/status/418
curl -s -X POST http://localhost:8080/v1/echo -H 'Content-Type: application/json' -d '{"hi":1}'
```

`make dev-anon` does the same. `make build` produces `./bin/api-test`.

## Tests

```bash
go test ./...           # unit + in-memory tests; no Docker required
make test               # alias: go test -race -count=1 ./...
make verify             # CI-equivalent: fmt, vet, test, lint, security, coverage gate
```

Integration tests requiring testcontainers Postgres land in M2.

## Layout

```
cmd/api-test            # binary entry
internal/server         # composition root (config + endpoints + httpsrv)
pkg/build               # version metadata stamped at link time
pkg/config              # YAML loader + ${VAR:-default} env interpolation
pkg/endpoints           # Endpoints interface + registry
pkg/endpoints/{...}     # one package per group (identity, data, failure, echo)
pkg/httpsrv             # HTTP mux composition + health/readiness + CORS
configs/                # *.dev.yaml, *.live.yaml, *.example.yaml
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
