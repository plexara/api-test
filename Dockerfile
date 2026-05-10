# syntax=docker/dockerfile:1
#
# api-test runtime image. Goreleaser supplies the pre-built binary in
# the build context (one per linux/<arch>); we just bundle it with CA
# certs and run as a non-root user.

FROM alpine:3.23 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch

# TLS root certs so OIDC discovery (HTTPS to the IdP) works.
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Goreleaser sets TARGETARCH for the platform-specific binary path.
ARG TARGETARCH
COPY linux/${TARGETARCH}/api-test /usr/local/bin/api-test

# No config is baked in. Operators mount one (or use env vars):
#
#   docker run --rm \
#     -v $(pwd)/api-test.yaml:/app/configs/api-test.yaml:ro \
#     ghcr.io/plexara/api-test:latest
#
# A starter config to copy from lives in the source tree at
# configs/api-test.example.yaml on the GitHub repo.

# Non-root (scratch has no /etc/passwd; numeric IDs only).
USER 1000:1000

EXPOSE 8080

# The binary doubles as its own healthcheck via `--healthcheck`, which
# probes 127.0.0.1:8080/healthz. No curl/wget needed in the runtime image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/api-test", "--healthcheck"]

ENTRYPOINT ["/usr/local/bin/api-test"]
CMD ["--config", "/app/configs/api-test.yaml"]
