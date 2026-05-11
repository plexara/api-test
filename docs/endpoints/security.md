---
title: Security probes
description: Probe endpoints shaped to LOOK like dangerous gateway targets so the gateway can pattern-match them and refuse to forward.
---

# Security probes

Five endpoints designed to look like things a gateway should refuse to
forward. The handlers themselves are **inert** — they never fetch a
URL, never escalate privileges, never emit smuggling-shaped responses.
Their value is the *shape* they present so a gateway URL-filter,
path-filter, or response-header limiter can pattern-match and refuse.

A correctly-configured gateway never forwards these requests; the
api-test server only sees them if the gateway is mis-configured or
missing a rule.

| Method | Path | Probe shape | Gateway should... |
| --- | --- | --- | --- |
| `GET`  | `/v1/security/admin/secret`     | privileged-looking path | refuse on path-filter. |
| `GET`  | `/v1/security/fetch?url=...`    | SSRF-shape query parameter | refuse when `url` points at localhost / link-local / non-allowlist. |
| `GET`  | `/v1/security/big-headers`      | ~32 KiB of response headers | reject or rewrite per RFC 7230 §3.2.5. |
| `POST` | `/v1/security/redirect-to?url=` | open-redirect shape (status 200 + custom `X-Would-Redirect-To` header) | refuse when `url` is unrestricted. |
| `GET`  | `/v1/security/control-chars?q=` | control bytes in query | sanitize, strip, or pass through observably. |

## Why the redirect probe returns 200 with a custom header

Returning a 3xx with a caller-controlled `Location` is a literal open
redirect, even on a test fixture. We avoid both: status is 200 (no
auto-follow) and the URL lands in `X-Would-Redirect-To`, not
`Location`. Gateway URL-filters that scan response headers can still
pattern-match the `X-Would-Redirect-To` shape; gateways that only
inspect `Location` will see nothing, which is itself a useful finding
about that gateway's coverage.

CodeQL's `go/unvalidated-url-redirection` rule does not trace
non-Location headers, so this design also keeps the static analyzer
clean without a per-file suppression.

## What "WouldHaveFetched" tells you

`/v1/security/fetch` always returns `{"asked_for": "...", "would_have_fetched": false}`.
The field is a contract: a gateway running this probe should observe
that the upstream **does not** fetch, and that any "the upstream made
a callback" signal in your monitoring is therefore a gateway bug
(unexpected egress).

## Example

```bash
# Privileged-path probe — gateway path-filter should refuse.
curl -is http://localhost:8080/v1/security/admin/secret | head -1

# SSRF-shape probe — gateway SSRF heuristics should refuse this URL.
curl -s 'http://localhost:8080/v1/security/fetch?url=http://169.254.169.254/latest/meta-data/'
# {"asked_for":"http://169.254.169.254/latest/meta-data/","would_have_fetched":false}

# Big-headers probe — gateway header-size limit should reject.
curl -is http://localhost:8080/v1/security/big-headers | grep -c '^X-Big-Probe-'
# 64
```
