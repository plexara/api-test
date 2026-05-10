// Package config loads YAML configuration with ${VAR:-default} env interpolation.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration document.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	OIDC      OIDCConfig      `yaml:"oidc"`
	APIKeys   APIKeysConfig   `yaml:"api_keys"`
	Bearer    BearerConfig    `yaml:"bearer"`
	Auth      AuthConfig      `yaml:"auth"`
	Database  DatabaseConfig  `yaml:"database"`
	Audit     AuditConfig     `yaml:"audit"`
	Portal    PortalConfig    `yaml:"portal"`
	Endpoints EndpointsConfig `yaml:"endpoints"`
	Plexara   PlexaraConfig   `yaml:"plexara"`
}

// ServerConfig holds the HTTP listener and lifecycle settings.
type ServerConfig struct {
	Name              string         `yaml:"name"`
	Address           string         `yaml:"address"`
	BaseURL           string         `yaml:"base_url"`
	Description       string         `yaml:"description"`
	ReadHeaderTimeout time.Duration  `yaml:"read_header_timeout"`
	Shutdown          ShutdownConfig `yaml:"shutdown"`
	TLS               TLSConfig      `yaml:"tls"`
}

// DefaultServerDescription is the operator-facing description rendered in the
// OpenAPI document and the portal About page.
const DefaultServerDescription = `api-test is a controllable HTTP REST fixture used to exercise API
gateways (Plexara's in particular) end-to-end.

Endpoints are deliberately simple and deterministic. Their job is not to
compute anything useful; their job is to make a gateway's behavior
observable. Every request is recorded in a Postgres-backed audit log,
so a tester can compare what a client sent, what reached this server,
and what came back.

Endpoints are grouped by what they help you test:
  - identity:   whoami, headers, echo - verify the gateway forwards
                identity, args, and HTTP headers, with redaction.
  - data:       fixed, sized, lorem - deterministic outputs for
                testing dedup, response-size handling, and caching.
  - failure:    status, slow, flaky - controlled failure modes
                (errors, latency, probabilistic flakiness) for retry
                and timeout policy testing.
  - streaming:  chunked, sse, ndjson - long-running responses.
  - pagination: link, odata, cursor variants - one endpoint per cursor
                style the gateway recognizes.
  - methods:    GET/POST/PUT/PATCH/DELETE/HEAD on /v1/method/echo.
  - security:   probe targets the gateway should refuse to forward.
  - export:     large/long-running targets exercising api_export.
  - echo:       catch-all that returns the request verbatim.

This server is not a data source. Do not call it for real information.`

// ShutdownConfig tunes graceful-drain behavior.
type ShutdownConfig struct {
	GracePeriod      time.Duration `yaml:"grace_period"`
	PreShutdownDelay time.Duration `yaml:"pre_shutdown_delay"`
}

// TLSConfig configures optional in-process TLS.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// OIDCConfig configures OAuth2 bearer-token validation against an external IdP
// (Keycloak in dev). Used by the inbound auth chain to validate JWTs the
// Plexara gateway sends after exchanging client_credentials or auth_code with
// the IdP, AND by the portal's browser-login flow.
type OIDCConfig struct {
	Enabled                   bool          `yaml:"enabled"`
	Issuer                    string        `yaml:"issuer"`
	Audience                  string        `yaml:"audience"`
	ClientID                  string        `yaml:"client_id"`
	ClientSecret              string        `yaml:"client_secret"`
	AllowedClients            []string      `yaml:"allowed_clients"`
	ClockSkewSeconds          int           `yaml:"clock_skew_seconds"`
	JWKSCacheTTL              time.Duration `yaml:"jwks_cache_ttl"`
	SkipSignatureVerification bool          `yaml:"skip_signature_verification"`
}

// APIKeysConfig groups file and DB API key sources.
type APIKeysConfig struct {
	File           []FileAPIKey    `yaml:"file"`
	DB             APIKeysDBConfig `yaml:"db"`
	HeaderName     string          `yaml:"header_name"`
	QueryParamName string          `yaml:"query_param_name"`
}

// FileAPIKey is a single plaintext key loaded from config.
type FileAPIKey struct {
	Name        string `yaml:"name"`
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
}

// APIKeysDBConfig toggles the bcrypt-hashed Postgres key store.
type APIKeysDBConfig struct {
	Enabled bool `yaml:"enabled"`
}

// BearerConfig holds static bearer tokens accepted by the inbound auth chain.
// These let a Plexara connection use auth_mode=bearer without needing a JWT.
type BearerConfig struct {
	Tokens []FileBearerToken `yaml:"tokens"`
}

// FileBearerToken is a single plaintext bearer token loaded from config.
type FileBearerToken struct {
	Name        string `yaml:"name"`
	Token       string `yaml:"token"`
	Description string `yaml:"description"`
}

// AuthConfig controls server-wide auth requirements.
type AuthConfig struct {
	AllowAnonymous   bool `yaml:"allow_anonymous"`
	RequireForAPI    bool `yaml:"require_for_api"`
	RequireForPortal bool `yaml:"require_for_portal"`
}

// DatabaseConfig configures the pgx connection pool. Empty URL is allowed in
// M1 (audit disabled, no DB-backed keys); validate enforces presence only when
// audit or DB-backed features need it.
type DatabaseConfig struct {
	URL             string        `yaml:"url"`
	MaxOpenConns    int32         `yaml:"max_open_conns"`
	MaxIdleConns    int32         `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

// AuditConfig controls audit log behavior and parameter redaction.
//
// Payload capture (request/response envelope, headers) is on by default
// because api-test is a test fixture and full visibility is the entire
// point. Operators can flip CapturePayloads off to keep only the indexable
// summary.
type AuditConfig struct {
	Enabled       bool     `yaml:"enabled"`
	RetentionDays int      `yaml:"retention_days"`
	RedactKeys    []string `yaml:"redact_keys"`

	// CapturePayloads enables writing the audit_payloads sibling row
	// alongside each summary. Default true (nil pointer = unset = on).
	CapturePayloads *bool `yaml:"capture_payloads"`

	// CaptureHeaders includes the redacted HTTP headers in the payload row.
	// Default true; set false in deployments where headers carry data the
	// operator doesn't want stored even after redaction.
	CaptureHeaders *bool `yaml:"capture_headers"`

	// MaxPayloadBytes caps per-side (request, response) payload size.
	// Anything beyond is dropped and the matching truncated flag is set.
	// Default 1 MiB (api-test responses can be much larger than mcp-test).
	MaxPayloadBytes int `yaml:"max_payload_bytes"`
}

// CapturePayloadsEnabled returns true unless the operator explicitly set
// CapturePayloads to false.
func (a AuditConfig) CapturePayloadsEnabled() bool {
	if a.CapturePayloads == nil {
		return true
	}
	return *a.CapturePayloads
}

// CaptureHeadersEnabled returns true unless the operator explicitly set
// CaptureHeaders to false.
func (a AuditConfig) CaptureHeadersEnabled() bool {
	if a.CaptureHeaders == nil {
		return true
	}
	return *a.CaptureHeaders
}

// PortalConfig configures the embedded React portal and its session cookie.
type PortalConfig struct {
	Enabled          bool   `yaml:"enabled"`
	CookieName       string `yaml:"cookie_name"`
	CookieSecret     string `yaml:"cookie_secret"`
	CookieSecure     bool   `yaml:"cookie_secure"`
	OIDCRedirectPath string `yaml:"oidc_redirect_path"`
}

// EndpointsConfig toggles each endpoint group on or off.
type EndpointsConfig struct {
	Identity   EndpointGroupConfig `yaml:"identity"`
	Data       EndpointGroupConfig `yaml:"data"`
	Failure    EndpointGroupConfig `yaml:"failure"`
	Streaming  EndpointGroupConfig `yaml:"streaming"`
	Pagination EndpointGroupConfig `yaml:"pagination"`
	Methods    EndpointGroupConfig `yaml:"methods"`
	Security   EndpointGroupConfig `yaml:"security"`
	Export     EndpointGroupConfig `yaml:"export"`
	Echo       EndpointGroupConfig `yaml:"echo"`
}

// EndpointGroupConfig is the per-group toggle.
type EndpointGroupConfig struct {
	Enabled bool `yaml:"enabled"`
}

// PlexaraConfig optionally registers api-test as a connection in a running
// Plexara instance on startup. Default off; ship example YAML for manual use.
type PlexaraConfig struct {
	Register PlexaraRegisterConfig `yaml:"register"`
}

// PlexaraRegisterConfig holds the admin API target for self-registration.
type PlexaraRegisterConfig struct {
	Enabled    bool   `yaml:"enabled"`
	AdminURL   string `yaml:"admin_url"`
	AuthHeader string `yaml:"auth_header"`
	ConnName   string `yaml:"connection_name"`
}

// Load reads, env-expands, and validates a YAML config file.
func Load(path string) (*Config, error) {
	// #nosec G304 -- path comes from the operator's --config flag; this is
	// the intended entry point and the binary trusts its CLI args.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	expanded := expandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults fills empty fields with reasonable defaults.
func (c *Config) applyDefaults() {
	if c.Server.Name == "" {
		c.Server.Name = "api-test"
	}
	if c.Server.Address == "" {
		c.Server.Address = ":8080"
	}
	if c.Server.BaseURL == "" {
		c.Server.BaseURL = "http://localhost" + portFromAddr(c.Server.Address)
	}
	if c.Server.Description == "" {
		c.Server.Description = DefaultServerDescription
	}
	if c.Server.ReadHeaderTimeout == 0 {
		c.Server.ReadHeaderTimeout = 10 * time.Second
	}
	if c.Server.Shutdown.GracePeriod == 0 {
		c.Server.Shutdown.GracePeriod = 25 * time.Second
	}
	if c.Server.Shutdown.PreShutdownDelay == 0 {
		c.Server.Shutdown.PreShutdownDelay = 2 * time.Second
	}
	if c.OIDC.ClockSkewSeconds == 0 {
		c.OIDC.ClockSkewSeconds = 30
	}
	if c.OIDC.JWKSCacheTTL == 0 {
		c.OIDC.JWKSCacheTTL = time.Hour
	}
	if c.APIKeys.HeaderName == "" {
		c.APIKeys.HeaderName = "X-API-Key"
	}
	if c.APIKeys.QueryParamName == "" {
		c.APIKeys.QueryParamName = "api_key"
	}
	if c.Database.MaxOpenConns == 0 {
		c.Database.MaxOpenConns = 25
	}
	if c.Database.MaxIdleConns == 0 {
		c.Database.MaxIdleConns = 5
	}
	if c.Database.ConnMaxLifetime == 0 {
		c.Database.ConnMaxLifetime = time.Hour
	}
	if c.Audit.RetentionDays == 0 {
		// Lower than mcp-test's 30 because api-test responses can be much
		// larger; export endpoints emit 100 MiB bodies that inflate the
		// audit_payloads table fast.
		c.Audit.RetentionDays = 7
	}
	if c.Audit.MaxPayloadBytes == 0 {
		c.Audit.MaxPayloadBytes = 1 << 20 // 1 MiB
	}
	if len(c.Audit.RedactKeys) == 0 {
		// Matched as case-insensitive substrings against parameter keys
		// and header names. Operators should extend this for
		// domain-specific secret naming.
		c.Audit.RedactKeys = []string{
			"password", "token", "secret", "authorization", "api_key",
			"api-key", "credentials", "bearer", "cookie", "jwt",
			"session_id", "private_key", "passwd",
		}
	}
	if c.Portal.CookieName == "" {
		c.Portal.CookieName = "api_test_session"
	}
	if c.Portal.OIDCRedirectPath == "" {
		c.Portal.OIDCRedirectPath = "/portal/auth/callback"
	}
	if c.Plexara.Register.ConnName == "" {
		c.Plexara.Register.ConnName = "api-test"
	}
}

// Validate fails fast on impossible or insecure configurations.
func (c *Config) Validate() error {
	var errs []string
	// Database is only required when audit is enabled or DB-backed
	// features are turned on. M1 deployments can run anonymous + no audit.
	if (c.Audit.Enabled || c.APIKeys.DB.Enabled) && c.Database.URL == "" {
		errs = append(errs, "database.url is required when audit.enabled or api_keys.db.enabled")
	}
	if c.Portal.Enabled && c.Portal.CookieSecret == "" {
		errs = append(errs, "portal.cookie_secret is required when portal.enabled=true")
	}
	if c.OIDC.Enabled && c.OIDC.Issuer == "" {
		errs = append(errs, "oidc.issuer is required when oidc.enabled=true")
	}
	if c.OIDC.SkipSignatureVerification && os.Getenv("APITEST_INSECURE") != "1" {
		errs = append(errs, "oidc.skip_signature_verification requires APITEST_INSECURE=1")
	}
	hasInboundAuth := c.OIDC.Enabled ||
		len(c.APIKeys.File) > 0 ||
		c.APIKeys.DB.Enabled ||
		len(c.Bearer.Tokens) > 0
	if !hasInboundAuth && !c.Auth.AllowAnonymous {
		errs = append(errs, "no inbound auth method enabled: configure oidc, api_keys, bearer.tokens, or auth.allow_anonymous")
	}
	if c.Plexara.Register.Enabled && c.Plexara.Register.AdminURL == "" {
		errs = append(errs, "plexara.register.admin_url is required when plexara.register.enabled=true")
	}
	if len(errs) > 0 {
		return errors.New("invalid config: " + strings.Join(errs, "; "))
	}
	return nil
}

// expandEnv expands ${VAR} and ${VAR:-default} forms in s using os.LookupEnv.
//
// Plain $VAR is intentionally left untouched; config values often contain
// shell-like syntax (e.g. Postgres connection strings) that we don't want
// to rewrite.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

func expandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := envPattern.FindStringSubmatch(match)
		if len(groups) == 0 {
			return match
		}
		name, def := groups[1], groups[2]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return def
	})
}

// portFromAddr returns the :port suffix from an address like ":8080" or "0.0.0.0:8080".
func portFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":8080"
}
