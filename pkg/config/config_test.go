package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_AnonymousMinimal(t *testing.T) {
	// Anonymous + audit off + DB-keys off: should pass without database.url.
	cfg, err := Load(writeTemp(t, `
auth:
  allow_anonymous: true
endpoints:
  identity: { enabled: true }
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Name != "api-test" {
		t.Errorf("default name not applied: %q", cfg.Server.Name)
	}
	if cfg.Server.Address != ":8080" {
		t.Errorf("default address not applied: %q", cfg.Server.Address)
	}
	if cfg.Audit.RetentionDays != 7 {
		t.Errorf("audit retention default: got %d want 7", cfg.Audit.RetentionDays)
	}
	if cfg.APIKeys.HeaderName != "X-API-Key" {
		t.Errorf("apikeys header default: %q", cfg.APIKeys.HeaderName)
	}
	if !cfg.Audit.CapturePayloadsEnabled() {
		t.Error("CapturePayloadsEnabled default should be true")
	}
}

func TestLoad_RequiresAuth(t *testing.T) {
	_, err := Load(writeTemp(t, `
auth:
  allow_anonymous: false
endpoints:
  identity: { enabled: true }
`))
	if err == nil {
		t.Fatal("expected validation error for missing auth source")
	}
	if !strings.Contains(err.Error(), "no inbound auth") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestLoad_AuditRequiresDB(t *testing.T) {
	_, err := Load(writeTemp(t, `
auth:
  allow_anonymous: true
audit:
  enabled: true
endpoints:
  identity: { enabled: true }
`))
	if err == nil || !strings.Contains(err.Error(), "database.url is required") {
		t.Fatalf("expected db.url error, got %v", err)
	}
}

func TestLoad_PortalRequiresCookieSecret(t *testing.T) {
	_, err := Load(writeTemp(t, `
auth:
  allow_anonymous: true
portal:
  enabled: true
endpoints:
  identity: { enabled: true }
`))
	if err == nil || !strings.Contains(err.Error(), "portal.cookie_secret") {
		t.Fatalf("expected cookie_secret error, got %v", err)
	}
}

func TestLoad_PlexaraRegisterRequiresAdminURL(t *testing.T) {
	_, err := Load(writeTemp(t, `
auth:
  allow_anonymous: true
plexara:
  register:
    enabled: true
endpoints:
  identity: { enabled: true }
`))
	if err == nil || !strings.Contains(err.Error(), "plexara.register.admin_url") {
		t.Fatalf("expected admin_url error, got %v", err)
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("APITEST_TEST_VAR", "hello")
	got := expandEnv("a=${APITEST_TEST_VAR} b=${APITEST_NOT_SET:-fallback} c=${APITEST_NOT_SET}")
	want := "a=hello b=fallback c="
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestExpandEnv_PreservesPlainDollar(t *testing.T) {
	// Plain $VAR (no braces) must stay untouched; Postgres connection
	// strings rely on this.
	in := "host=$HOST_NOT_SET sslmode=disable"
	if got := expandEnv(in); got != in {
		t.Errorf("got %q want %q", got, in)
	}
}

func TestCaptureHeadersEnabled_ExplicitFalse(t *testing.T) {
	f := false
	cfg := AuditConfig{CaptureHeaders: &f}
	if cfg.CaptureHeadersEnabled() {
		t.Error("explicit false should be respected")
	}
}

func TestPortFromAddr(t *testing.T) {
	for in, want := range map[string]string{
		":8080":         ":8080",
		"0.0.0.0:8080":  ":8080",
		"127.0.0.1:443": ":443",
		"":              ":8080",
		"oddvalue":      ":8080",
	} {
		if got := portFromAddr(in); got != want {
			t.Errorf("portFromAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
