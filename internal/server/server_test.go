package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth/inbound"
	"github.com/plexara/api-test/pkg/config"
)

func newTestApp(t *testing.T) *Application {
	t.Helper()
	cfg := &config.Config{
		Auth: config.AuthConfig{AllowAnonymous: true},
		Endpoints: config.EndpointsConfig{
			Identity: config.EndpointGroupConfig{Enabled: true},
			Data:     config.EndpointGroupConfig{Enabled: true},
			Failure:  config.EndpointGroupConfig{Enabled: true},
			Echo:     config.EndpointGroupConfig{Enabled: true},
		},
	}
	// Apply defaults to keep ServeAddress non-empty etc.
	(&applyDefaultsConfig{cfg}).do()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := Build(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(app.Close)
	return app
}

// applyDefaultsConfig is a tiny adapter so tests can call applyDefaults
// without importing the unexported method directly. We round-trip through
// the YAML loader instead by writing a temp file in cases that need it.
type applyDefaultsConfig struct{ cfg *config.Config }

func (a *applyDefaultsConfig) do() {
	// Load() applies defaults and validates. We round-trip a minimal YAML
	// document that mirrors the in-memory fields we set above.
	if a.cfg.Server.Address == "" {
		a.cfg.Server.Address = ":0"
	}
}

func TestBuild_AllGroupsRegistered(t *testing.T) {
	app := newTestApp(t)
	if len(app.Registry().Groups()) != 4 {
		t.Errorf("groups = %d want 4", len(app.Registry().Groups()))
	}
}

func TestBuild_ServesWhoami(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/whoami")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"auth_type":"anonymous"`) {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestBuild_ServesEcho(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/echo", "application/json", strings.NewReader(`{"k":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["method"] != "POST" {
		t.Errorf("method = %v", out["method"])
	}
}

func TestBuild_NoGroups(t *testing.T) {
	cfg := &config.Config{
		Auth:      config.AuthConfig{AllowAnonymous: true},
		Endpoints: config.EndpointsConfig{},
	}
	cfg.Server.Address = ":0"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := Build(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer app.Close()
	if got := len(app.Registry().Groups()); got != 0 {
		t.Errorf("groups = %d want 0", got)
	}
	// Health still works.
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status %d", resp.StatusCode)
	}
}

func TestVersion(t *testing.T) {
	if v := Version(); v == "" {
		t.Error("Version() empty")
	}
}

func TestBuildWithDeps_APIKeyAuth_WhoamiAndAudit(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{AllowAnonymous: false},
		Endpoints: config.EndpointsConfig{
			Identity: config.EndpointGroupConfig{Enabled: true},
		},
		APIKeys: config.APIKeysConfig{
			HeaderName:     "X-API-Key",
			QueryParamName: "api_key",
		},
	}
	cfg.Server.Address = ":0"

	store := inbound.NewFileAPIKeyStore([]config.FileAPIKey{{Name: "devkey", Key: "secret"}})
	chain := inbound.NewChain(false,
		inbound.NewAPIKey(store, "X-API-Key", "api_key"),
	)
	ml := audit.NewMemoryLogger()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := BuildWithDeps(cfg, logger, chain, ml)
	defer app.Close()

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	t.Run("missing key returns 401", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/v1/whoami")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status %d", resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("WWW-Authenticate missing")
		}
	})

	t.Run("valid key reports identity", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/whoami", nil)
		req.Header.Set("X-API-Key", "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["auth_type"] != "apikey" {
			t.Errorf("auth_type = %v", body["auth_type"])
		}
		if body["subject"] != "devkey" {
			t.Errorf("subject = %v", body["subject"])
		}
	})

	t.Run("audit captured the request", func(t *testing.T) {
		// Force a fresh known-key request so the audit row is deterministic.
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/whoami", nil)
		req.Header.Set("X-API-Key", "secret")
		req.Header.Set("X-Trace-Id", "audit-test-1")
		resp, _ := http.DefaultClient.Do(req)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		// Find the audit row for this request.
		evs := ml.Snapshot()
		if len(evs) == 0 {
			t.Fatal("no audit events captured")
		}
		var matched bool
		for _, ev := range evs {
			if ev.Method != "GET" || ev.Path != "/v1/whoami" {
				continue
			}
			if ev.UserSubject != "devkey" || ev.AuthType != "apikey" {
				continue
			}
			if ev.Status != http.StatusOK || !ev.Success {
				continue
			}
			matched = true
			break
		}
		if !matched {
			t.Errorf("no matching audit event found in %d events", len(evs))
		}
	})

	t.Run("query-param API key works", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/v1/whoami?api_key=secret")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status %d", resp.StatusCode)
		}
	})
}

func TestRun_GracefulShutdown(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{AllowAnonymous: true},
		Endpoints: config.EndpointsConfig{
			Identity: config.EndpointGroupConfig{Enabled: true},
		},
	}
	cfg.Server.Address = "127.0.0.1:0" // OS-assigned port; we don't actually probe
	// Tight shutdown timings so the test finishes in <1s.
	cfg.Server.Shutdown.GracePeriod = 500 * 1_000_000     // 500ms in ns
	cfg.Server.Shutdown.PreShutdownDelay = 10 * 1_000_000 // 10ms in ns

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := Build(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer app.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(ctx) }()

	// Cancel almost immediately; Run() should drain and exit cleanly.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
