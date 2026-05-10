package httpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/auth/inbound"
)

// stubKeyStore implements inbound.APIKeyStore for fallback-credential tests.
type stubKeyStore struct {
	plaintext, name string
}

func (s stubKeyStore) LookupAPIKey(_ context.Context, presented string) (string, error) {
	if presented == s.plaintext {
		return s.name, nil
	}
	return "", inbound.ErrInvalidCredential
}

func TestPortalAuth_RejectsAnonymous(t *testing.T) {
	store, _ := NewSessionStore("c", testSecret, false, time.Hour)
	pa := NewPortalAuth(store, nil)
	called := false
	h := pa.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/portal/api/whoami", nil))
	if called {
		t.Error("anonymous request reached handler; should be 401")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate challenge on 401")
	}
}

func TestPortalAuth_AcceptsValidSessionCookie(t *testing.T) {
	store, _ := NewSessionStore("c", testSecret, false, time.Hour)
	pa := NewPortalAuth(store, nil)

	cookieRec := httptest.NewRecorder()
	want := &auth.Identity{Subject: "alice", AuthType: "oidc"}
	if err := store.Issue(cookieRec, want); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := cookieRec.Result().Cookies()[0]

	var seen *auth.Identity
	h := pa.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = auth.GetIdentity(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/portal/api/whoami", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if seen == nil || seen.Subject != "alice" {
		t.Errorf("identity in ctx = %+v, want subject=alice", seen)
	}
}

func TestPortalAuth_FallsBackToAPIKey(t *testing.T) {
	store, _ := NewSessionStore("c", testSecret, false, time.Hour)
	keyStore := stubKeyStore{plaintext: "raw", name: "ci"}
	chain := inbound.NewChain(false, inbound.NewAPIKey(keyStore, "X-API-Key", "api_key"))
	pa := NewPortalAuth(store, chain)

	var seen *auth.Identity
	h := pa.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = auth.GetIdentity(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/portal/api/whoami", nil)
	r.Header.Set("X-API-Key", "raw")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if seen == nil || seen.AuthType != "apikey" || seen.APIKeyID != "ci" {
		t.Errorf("identity = %+v, want authtype=apikey api_key_id=ci", seen)
	}
}

func TestPortalAuth_RejectsAnonymousChainEvenWithBadKey(t *testing.T) {
	store, _ := NewSessionStore("c", testSecret, false, time.Hour)
	keyStore := stubKeyStore{plaintext: "good", name: "ci"}
	chain := inbound.NewChain(true, inbound.NewAPIKey(keyStore, "X-API-Key", "api_key")) // chain itself permits anonymous

	pa := NewPortalAuth(store, chain)
	called := false
	h := pa.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "/portal/api/whoami", nil)
	r.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if called {
		t.Error("portal auth admitted anonymous identity; must require non-anonymous")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAdaptInboundIdentity(t *testing.T) {
	if got := adaptInboundIdentity(nil); got != nil {
		t.Errorf("adapt(nil) = %+v, want nil", got)
	}
	in := &inbound.Identity{
		Subject:  "ci",
		Email:    "ci@example.com",
		AuthType: "apikey",
		KeyName:  "ci",
		Claims:   map[string]any{"role": "admin"},
	}
	got := adaptInboundIdentity(in)
	if got.Subject != "ci" || got.AuthType != "apikey" || got.APIKeyID != "ci" || got.Email != "ci@example.com" || got.Name != "ci" {
		t.Errorf("adapt = %+v", got)
	}
	if got.Claims["role"] != "admin" {
		t.Errorf("claims not preserved: %+v", got.Claims)
	}
}
