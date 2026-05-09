# Multi-stage build for the api-test fixture binary.
#
# Stage 1: build the static linux binary with version metadata stamped in.
# Stage 2: distroless base; the binary doubles as its own healthcheck via
# `--healthcheck` so we don't need curl/wget in the runtime image.

FROM golang:1.26 AS build

ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags "-s -w \
            -X github.com/plexara/api-test/pkg/build.Version=${VERSION} \
            -X github.com/plexara/api-test/pkg/build.Commit=${COMMIT} \
            -X github.com/plexara/api-test/pkg/build.Date=${BUILD_DATE}" \
        -o /out/api-test \
        ./cmd/api-test

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api-test /usr/local/bin/api-test
COPY --chown=nonroot:nonroot configs/api-test.dev.yaml /etc/api-test/api-test.yaml

EXPOSE 8080
USER nonroot:nonroot

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/api-test", "--healthcheck"]

ENTRYPOINT ["/usr/local/bin/api-test"]
CMD ["--config", "/etc/api-test/api-test.yaml"]
