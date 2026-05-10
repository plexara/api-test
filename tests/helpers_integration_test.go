//go:build integration

// Package tests / integration covers the full HTTP + Postgres stack
// end-to-end. Requires Docker on the host; run with
// `go test -tags integration ./tests/...`.
package tests

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/plexara/api-test/internal/server"
	"github.com/plexara/api-test/pkg/config"
)

func slogDiscard(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// startPostgres boots a postgres:16-alpine container via testcontainers
// and returns its connection URL with sslmode=disable.
func startPostgres(ctx context.Context, t *testing.T) string {
	t.Helper()
	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("apitest"),
		tcpostgres.WithUsername("api"),
		tcpostgres.WithPassword("api"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })

	url, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	return url
}

// boot constructs the full Application against the supplied Postgres URL
// with the api-test fixture endpoints enabled, audit on, and a known set
// of API keys / bearer tokens.
//
// Returns the live httptest.Server URL and the Application (so tests can
// reach into AuditLog for assertions). Cleanup is registered with t.
func boot(t *testing.T, pgURL string) (string, *server.Application) {
	t.Helper()
	cfg := &config.Config{
		Auth: config.AuthConfig{AllowAnonymous: false},
		APIKeys: config.APIKeysConfig{
			HeaderName:     "X-API-Key",
			QueryParamName: "api_key",
			File: []config.FileAPIKey{
				{Name: "intkey", Key: TestAPIKey, Description: "integration"},
			},
		},
		Bearer: config.BearerConfig{
			Tokens: []config.FileBearerToken{
				{Name: "intbearer", Token: TestBearerToken, Description: "integration"},
			},
		},
		Database: config.DatabaseConfig{URL: pgURL},
		Audit: config.AuditConfig{
			Enabled:         true,
			RetentionDays:   1,
			MaxPayloadBytes: 64 * 1024,
		},
		Endpoints: config.EndpointsConfig{
			Identity: config.EndpointGroupConfig{Enabled: true},
			Data:     config.EndpointGroupConfig{Enabled: true},
			Failure:  config.EndpointGroupConfig{Enabled: true},
			Echo:     config.EndpointGroupConfig{Enabled: true},
		},
	}
	cfg.Server.Address = ":0"
	cfg.Server.Shutdown.GracePeriod = 5 * time.Second
	cfg.Server.ReadHeaderTimeout = 5 * time.Second
	// Apply defaults that Load() would normally apply.
	if len(cfg.Audit.RedactKeys) == 0 {
		cfg.Audit.RedactKeys = []string{
			"password", "token", "secret", "authorization", "api_key",
			"api-key", "bearer", "cookie",
		}
	}

	logger := slogDiscard(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	app, err := server.Build(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("server.Build: %v", err)
	}
	t.Cleanup(app.Close)

	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)

	return ts.URL, app
}

// TestAPIKey and TestBearerToken are the credentials boot() seeds. Used
// by every integration test so they don't have to redeclare them.
const (
	TestAPIKey      = "integration-api-key-secret"
	TestBearerToken = "integration-bearer-token-secret"
)

// authenticatedClient returns an HTTP client whose RoundTripper injects
// the standard X-API-Key header on every request.
func authenticatedClient() *http.Client {
	return &http.Client{Transport: withHeader(http.DefaultTransport, "X-API-Key", TestAPIKey)}
}

// withHeader wraps rt to add a static header on every request.
func withHeader(rt http.RoundTripper, key, value string) http.RoundTripper {
	return &headerInjector{rt: rt, key: key, value: value}
}

type headerInjector struct {
	rt         http.RoundTripper
	key, value string
}

func (h *headerInjector) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set(h.key, h.value)
	return h.rt.RoundTrip(clone)
}
