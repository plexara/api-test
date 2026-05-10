package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plexara/api-test/pkg/config"
)

func TestProtectedResourceMetadata_OIDCEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.BaseURL = "http://localhost:8080/"
	cfg.OIDC.Enabled = true
	cfg.OIDC.Issuer = "http://idp/realm"

	h := ProtectedResourceMetadata(cfg)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["resource"] != "http://localhost:8080/" {
		t.Errorf("resource = %v, want http://localhost:8080/", body["resource"])
	}
	servers, _ := body["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != "http://idp/realm" {
		t.Errorf("authorization_servers = %v, want [http://idp/realm]", servers)
	}
}

func TestProtectedResourceMetadata_OIDCDisabledHasEmptyAuthServers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.BaseURL = "http://localhost:8080"
	h := ProtectedResourceMetadata(cfg)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	servers, _ := body["authorization_servers"].([]any)
	if len(servers) != 0 {
		t.Errorf("authorization_servers = %v, want empty when oidc disabled", servers)
	}
}

func TestAuthorizationServerStub_PointsAtIssuer(t *testing.T) {
	cfg := &config.Config{}
	cfg.OIDC.Issuer = "http://idp/realm/"
	h := AuthorizationServerStub(cfg)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["issuer"] != "http://idp/realm/" {
		t.Errorf("issuer = %v, want http://idp/realm/", body["issuer"])
	}
	if body["openid_configuration_url"] != "http://idp/realm/.well-known/openid-configuration" {
		t.Errorf("openid_configuration_url = %v, want http://idp/realm/.well-known/openid-configuration", body["openid_configuration_url"])
	}
}

func TestProtectedResourceMetadataURL(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.BaseURL = "http://localhost:8080/"
	if got := ProtectedResourceMetadataURL(cfg); got != "http://localhost:8080/.well-known/oauth-protected-resource" {
		t.Errorf("URL = %q, want http://localhost:8080/.well-known/oauth-protected-resource", got)
	}
}
