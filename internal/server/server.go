// Package server composes the HTTP server, endpoint registry, audit log,
// inbound auth chain, and lifecycle.
//
// M2 surface: full DB + audit + non-OAuth inbound auth (file/DB API keys
// + static bearer tokens). OIDC, the portal, and the SPA arrive in M3.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/plexara/api-test/internal/ui"
	"github.com/plexara/api-test/pkg/apikeys"
	"github.com/plexara/api-test/pkg/audit"
	auditpg "github.com/plexara/api-test/pkg/audit/postgres"
	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/auth/inbound"
	"github.com/plexara/api-test/pkg/build"
	"github.com/plexara/api-test/pkg/config"
	"github.com/plexara/api-test/pkg/database"
	"github.com/plexara/api-test/pkg/database/migrate"
	"github.com/plexara/api-test/pkg/endpoints"
	"github.com/plexara/api-test/pkg/endpoints/data"
	"github.com/plexara/api-test/pkg/endpoints/echo"
	"github.com/plexara/api-test/pkg/endpoints/failure"
	"github.com/plexara/api-test/pkg/endpoints/identity"
	"github.com/plexara/api-test/pkg/endpoints/methods"
	"github.com/plexara/api-test/pkg/endpoints/pagination"
	"github.com/plexara/api-test/pkg/endpoints/streaming"
	"github.com/plexara/api-test/pkg/httpmw"
	"github.com/plexara/api-test/pkg/httpsrv"
	"github.com/plexara/api-test/pkg/oapi"
)

// Application is the wired-up server, ready to be started with Run.
type Application struct {
	cfg        *config.Config
	logger     *slog.Logger
	pool       *pgxpool.Pool
	registry   *endpoints.Registry
	auditLog   audit.Logger
	asyncAudit *audit.AsyncLogger
	chain      *inbound.Chain
	dbKeys     *apikeys.Store
	readiness  *httpsrv.Readiness
	mux        http.Handler
}

// Build constructs an Application from a config. M2 wiring:
//   - opens Postgres pool + runs migrations when database.url is set
//   - constructs AsyncLogger over either the Postgres store or NoopLogger
//   - composes the inbound auth chain (file keys + DB keys + bearer)
//   - mounts the endpoint registry through the audit/identity middleware
func Build(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Application, error) {
	app := &Application{cfg: cfg, logger: logger}

	// --- Database (optional in M2) ---
	if cfg.Database.URL != "" {
		if err := migrate.Up(cfg.Database.URL); err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
		pool, err := database.Open(ctx, cfg.Database)
		if err != nil {
			return nil, fmt.Errorf("database: %w", err)
		}
		app.pool = pool
	}

	// --- Audit logger ---
	app.auditLog = audit.NoopLogger{}
	if cfg.Audit.Enabled {
		if app.pool == nil {
			return nil, errors.New("audit.enabled requires database.url")
		}
		app.asyncAudit = audit.NewAsyncLogger(auditpg.New(app.pool), 4096, 5*time.Second, logger)
		app.auditLog = app.asyncAudit
	} else {
		logger.Info("audit disabled by config")
	}

	// --- Inbound auth chain ---
	fileStore := inbound.NewFileAPIKeyStore(cfg.APIKeys.File)
	var keyStore inbound.APIKeyStore = fileStore
	if cfg.APIKeys.DB.Enabled {
		if app.pool == nil {
			return nil, errors.New("api_keys.db.enabled requires database.url")
		}
		app.dbKeys = apikeys.New(app.pool)
		keyStore = inbound.CombineAPIKeyStores(fileStore, app.dbKeys.AsInboundStore())
	}
	apikeyAuth := inbound.NewAPIKey(keyStore, cfg.APIKeys.HeaderName, cfg.APIKeys.QueryParamName)
	bearerAuth := inbound.NewBearer(cfg.Bearer.Tokens)
	app.chain = inbound.NewChain(cfg.Auth.AllowAnonymous, apikeyAuth, bearerAuth)

	// --- Endpoint registry ---
	app.registry = buildRegistry(cfg)

	// --- Middleware stack ---
	identityMW := httpmw.Identity(app.chain, logger)
	auditMW := httpmw.Audit(app.auditLog, app.registry, logger, httpmw.AuditOptions{
		CapturePayloads: cfg.Audit.CapturePayloadsEnabled() && cfg.Audit.Enabled,
		CaptureHeaders:  cfg.Audit.CaptureHeadersEnabled(),
		MaxPayloadBytes: cfg.Audit.MaxPayloadBytes,
		RedactKeys:      cfg.Audit.RedactKeys,
	})
	endpointMW := func(next http.Handler) http.Handler {
		return identityMW(auditMW(next))
	}

	app.readiness = httpsrv.NewReadiness()

	// --- Portal (M3+) ---
	portalDeps, err := buildPortal(ctx, cfg, app.chain, app.auditLog, app.registry, app.dbKeys, logger)
	if err != nil {
		return nil, fmt.Errorf("portal: %w", err)
	}

	oapiDoc := buildOpenAPI(cfg, app.registry)
	if err := oapi.SelfCheck(oapiDoc, app.registry); err != nil {
		return nil, fmt.Errorf("openapi self-check: %w", err)
	}
	core, err := httpsrv.BuildMux(app.registry, app.readiness, endpointMW, portalDeps, &oapiDoc)
	if err != nil {
		return nil, fmt.Errorf("build mux: %w", err)
	}
	// AccessLog + RequestID wrap the entire mux so health probes also get
	// request ids; identity/audit only run on endpoint group routes (via
	// endpointMW above).
	app.mux = httpmw.RequestID(httpmw.AccessLog(logger)(core))
	return app, nil
}

// buildOpenAPI assembles the served OpenAPI document from the loaded config
// and the registered endpoint groups. The result is rendered once at boot
// inside BuildMux.
func buildOpenAPI(cfg *config.Config, registry *endpoints.Registry) oapi.Document {
	opts := oapi.BuildOptions{
		Info: oapi.Info{
			Title:       cfg.Server.Name,
			Version:     build.Version,
			Description: cfg.Server.Description,
		},
		Servers:       []oapi.Server{{URL: cfg.Server.BaseURL}},
		APIKeyHeader:  cfg.APIKeys.HeaderName,
		BearerEnabled: len(cfg.Bearer.Tokens) > 0 || cfg.OIDC.Enabled,
	}
	return oapi.Build(registry, opts)
}

// buildPortal returns the portal handler bundle when cfg.Portal.Enabled is
// true. Returns (nil, nil) when the portal is disabled — the mux falls back
// to the bare /v1/* + /healthz surface.
//
// The OIDC validator + BrowserAuth construction will hit the configured
// issuer's discovery URL at startup; misconfiguration (wrong issuer, IdP
// down) fails Build() rather than the first portal request.
func buildPortal(
	ctx context.Context,
	cfg *config.Config,
	chain *inbound.Chain,
	auditLog audit.Logger,
	registry *endpoints.Registry,
	keys *apikeys.Store,
	logger *slog.Logger,
) (*httpsrv.PortalDeps, error) {
	if !cfg.Portal.Enabled {
		return nil, nil
	}
	sessions, err := httpsrv.NewSessionStore(
		cfg.Portal.CookieName,
		cfg.Portal.CookieSecret,
		cfg.Portal.CookieSecure,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	deps := &httpsrv.PortalDeps{
		Cfg:        cfg,
		PortalAuth: httpsrv.NewPortalAuth(sessions, chain),
		PortalAPI:  httpsrv.NewPortalAPI(cfg, registry, auditLog, keys),
	}
	if cfg.OIDC.Enabled {
		validator, err := auth.NewOIDC(ctx, cfg.OIDC)
		if err != nil {
			return nil, fmt.Errorf("oidc validator: %w", err)
		}
		ba, err := httpsrv.NewBrowserAuth(ctx, cfg, validator, sessions, logger)
		if err != nil {
			return nil, fmt.Errorf("browser auth: %w", err)
		}
		deps.BrowserAuth = ba
	} else {
		logger.Info("portal: oidc disabled, login will not work")
	}
	if ui.Available() {
		spa, err := ui.FS()
		if err != nil {
			return nil, fmt.Errorf("ui fs: %w", err)
		}
		deps.SPA = spa
	} else {
		logger.Warn("portal: ui dist is empty (run `make ui`); /portal/ will return 503")
	}
	return deps, nil
}

// BuildWithDeps assembles an Application from supplied dependencies,
// skipping database setup. Used by tests that inject in-memory loggers
// and stub auth.
func BuildWithDeps(cfg *config.Config, logger *slog.Logger, chain *inbound.Chain, auditLog audit.Logger) *Application {
	if auditLog == nil {
		auditLog = audit.NoopLogger{}
	}
	if chain == nil {
		chain = inbound.NewChain(true)
	}
	registry := buildRegistry(cfg)
	identityMW := httpmw.Identity(chain, logger)
	auditMW := httpmw.Audit(auditLog, registry, logger, httpmw.AuditOptions{
		CapturePayloads: cfg.Audit.CapturePayloadsEnabled(),
		CaptureHeaders:  cfg.Audit.CaptureHeadersEnabled(),
		MaxPayloadBytes: cfg.Audit.MaxPayloadBytes,
		RedactKeys:      cfg.Audit.RedactKeys,
	})
	endpointMW := func(next http.Handler) http.Handler {
		return identityMW(auditMW(next))
	}
	readiness := httpsrv.NewReadiness()
	oapiDoc := buildOpenAPI(cfg, registry)
	core, err := httpsrv.BuildMux(registry, readiness, endpointMW, nil, &oapiDoc)
	if err != nil {
		// BuildWithDeps is a test/dev convenience; surface the error via
		// panic so tests fail loudly rather than silently dropping the
		// OpenAPI surface.
		panic(fmt.Sprintf("BuildWithDeps: build mux: %v", err))
	}
	mux := httpmw.RequestID(httpmw.AccessLog(logger)(core))
	return &Application{
		cfg:       cfg,
		logger:    logger,
		registry:  registry,
		auditLog:  auditLog,
		chain:     chain,
		readiness: readiness,
		mux:       mux,
	}
}

// buildRegistry wires up the endpoint groups enabled by config.
func buildRegistry(cfg *config.Config) *endpoints.Registry {
	r := endpoints.NewRegistry()
	if cfg.Endpoints.Identity.Enabled {
		r.Add(identity.New(cfg.Audit.RedactKeys))
	}
	if cfg.Endpoints.Data.Enabled {
		r.Add(data.New())
	}
	if cfg.Endpoints.Failure.Enabled {
		r.Add(failure.New())
	}
	if cfg.Endpoints.Echo.Enabled {
		r.Add(echo.New(cfg.Audit.RedactKeys))
	}
	if cfg.Endpoints.Streaming.Enabled {
		r.Add(streaming.New())
	}
	if cfg.Endpoints.Pagination.Enabled {
		r.Add(pagination.New())
	}
	if cfg.Endpoints.Methods.Enabled {
		r.Add(methods.New())
	}
	return r
}

// Close releases held resources: drains the async audit queue, then
// closes the database pool.
func (a *Application) Close() {
	if a.asyncAudit != nil {
		a.asyncAudit.Close()
	}
	if a.pool != nil {
		a.pool.Close()
	}
}

// Handler returns the wrapped HTTP handler.
func (a *Application) Handler() http.Handler { return a.mux }

// Registry exposes the endpoint registry.
func (a *Application) Registry() *endpoints.Registry { return a.registry }

// AuditLog exposes the audit logger so tests can assert on captured events.
func (a *Application) AuditLog() audit.Logger { return a.auditLog }

// Run blocks listening on cfg.Server.Address until ctx is cancelled.
// Graceful drain identical to mcp-test.
func (a *Application) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.cfg.Server.Address,
		Handler:           a.mux,
		ReadHeaderTimeout: a.cfg.Server.ReadHeaderTimeout,
	}
	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("listening", "address", a.cfg.Server.Address)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	a.logger.Info("shutdown requested, draining")
	a.readiness.SetReady(false)

	if d := a.cfg.Server.Shutdown.PreShutdownDelay; d > 0 {
		select {
		case <-time.After(d):
		case err := <-errCh:
			a.logger.Warn("listener exited during pre-shutdown delay", "err", err)
		case <-ctx.Done():
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.Server.Shutdown.GracePeriod)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err, ok := <-errCh; ok && err != nil {
		a.logger.Warn("listener post-shutdown error", "err", err)
	}
	a.logger.Info("shutdown complete")
	return nil
}

// Version returns the build metadata as a one-line string.
func Version() string {
	return strings.Join([]string{build.Version, build.Commit, build.Date}, " ")
}
