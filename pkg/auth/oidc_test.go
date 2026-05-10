package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ----- pure helpers -----

func TestTrimRightSlash(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"/":               "",
		"http://x":        "http://x",
		"http://x/":       "http://x",
		"http://x///":     "http://x",
		"http://x/realm/": "http://x/realm",
	}
	for in, want := range cases {
		if got := trimRightSlash(in); got != want {
			t.Errorf("trimRightSlash(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAudienceMatches(t *testing.T) {
	if !audienceMatches("api-test", "api-test") {
		t.Error("string aud equal: should match")
	}
	if audienceMatches("other", "api-test") {
		t.Error("string aud differ: should not match")
	}
	if !audienceMatches([]any{"a", "api-test", "b"}, "api-test") {
		t.Error("slice aud containing want: should match")
	}
	if audienceMatches([]any{"a", "b"}, "api-test") {
		t.Error("slice aud not containing want: should not match")
	}
	if audienceMatches(42, "api-test") {
		t.Error("non-string-non-slice aud: should not match")
	}
	if audienceMatches([]any{42, true}, "api-test") {
		t.Error("slice with no string elements: should not match")
	}
}

func TestClientAllowed(t *testing.T) {
	allowed := []string{"web", "cli"}
	if !clientAllowed(jwt.MapClaims{"azp": "web"}, allowed) {
		t.Error("azp=web should be allowed")
	}
	if !clientAllowed(jwt.MapClaims{"client_id": "cli"}, allowed) {
		t.Error("client_id=cli should be allowed")
	}
	if !clientAllowed(jwt.MapClaims{"appid": "web"}, allowed) {
		t.Error("appid=web should be allowed")
	}
	if clientAllowed(jwt.MapClaims{"azp": "ghost"}, allowed) {
		t.Error("azp=ghost should not be allowed")
	}
	if clientAllowed(jwt.MapClaims{}, allowed) {
		t.Error("missing client claim: should not be allowed")
	}
}

func TestDecodeRSA_RoundTrip(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pub := priv.Public().(*rsa.PublicKey)
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())

	got, err := decodeRSA(n, e)
	if err != nil {
		t.Fatalf("decodeRSA: %v", err)
	}
	if got.N.Cmp(pub.N) != 0 || got.E != pub.E {
		t.Errorf("decoded key does not match: got E=%d N=%d, want E=%d N=%d", got.E, got.N, pub.E, pub.N)
	}
}

func TestDecodeRSA_InvalidInputs(t *testing.T) {
	if _, err := decodeRSA("@@@", "AQAB"); err == nil {
		t.Error("invalid n base64: want error")
	}
	if _, err := decodeRSA("AA", "@@@"); err == nil {
		t.Error("invalid e base64: want error")
	}
	tooBigE := base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3, 4, 5})
	if _, err := decodeRSA("AA", tooBigE); err == nil {
		t.Error("oversize e: want error")
	}
}

// ----- ValidateBearer with a fake IdP -----

// fakeIdP serves /.well-known/openid-configuration + /jwks pointing at a
// transient RSA key so we can mint tokens it will validate.
type fakeIdP struct {
	srv  *httptest.Server
	priv *rsa.PrivateKey
	kid  string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	idp := &fakeIdP{priv: priv, kid: "k1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.srv.URL,
			"jwks_uri":                              idp.srv.URL + "/jwks",
			"authorization_endpoint":                idp.srv.URL + "/authorize",
			"token_endpoint":                        idp.srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"use": "sig",
					"alg": "RS256",
					"kid": idp.kid,
					"n":   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
				},
			},
		})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (f *fakeIdP) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.kid
	s, err := tok.SignedString(f.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestNewOIDC_RejectsEmptyIssuer(t *testing.T) {
	_, err := NewOIDC(context.Background(), OIDCConfig{Enabled: true})
	if err == nil {
		t.Fatal("NewOIDC with empty issuer: want error")
	}
}

func TestNewOIDC_FailsOnDiscoveryError(t *testing.T) {
	_, err := NewOIDC(context.Background(), OIDCConfig{Enabled: true, Issuer: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("NewOIDC against unreachable issuer: want error")
	}
}

func TestValidateBearer_HappyPath(t *testing.T) {
	idp := newFakeIdP(t)
	v, err := NewOIDC(context.Background(), OIDCConfig{
		Enabled:        true,
		Issuer:         idp.srv.URL,
		Audience:       "api-test",
		AllowedClients: []string{"web"},
	})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	tok := idp.sign(t, jwt.MapClaims{
		"iss":   idp.srv.URL,
		"sub":   "alice",
		"email": "alice@example.com",
		"name":  "Alice",
		"aud":   "api-test",
		"azp":   "web",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})
	id, err := v.ValidateBearer(context.Background(), tok)
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "alice" || id.Email != "alice@example.com" || id.Name != "Alice" || id.AuthType != "oidc" {
		t.Errorf("identity = %+v, want subject=alice email=alice@example.com name=Alice authtype=oidc", id)
	}
}

func TestValidateBearer_AudienceMismatch(t *testing.T) {
	idp := newFakeIdP(t)
	v, err := NewOIDC(context.Background(), OIDCConfig{Enabled: true, Issuer: idp.srv.URL, Audience: "expected"})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	tok := idp.sign(t, jwt.MapClaims{
		"iss": idp.srv.URL,
		"sub": "alice",
		"aud": "different",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	if _, err := v.ValidateBearer(context.Background(), tok); err == nil {
		t.Fatal("audience mismatch: want error")
	}
}

func TestValidateBearer_DisallowedClient(t *testing.T) {
	idp := newFakeIdP(t)
	v, err := NewOIDC(context.Background(), OIDCConfig{
		Enabled:        true,
		Issuer:         idp.srv.URL,
		AllowedClients: []string{"web"},
	})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	tok := idp.sign(t, jwt.MapClaims{
		"iss": idp.srv.URL,
		"sub": "alice",
		"azp": "ghost",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	if _, err := v.ValidateBearer(context.Background(), tok); err == nil {
		t.Fatal("disallowed client: want error")
	}
}

func TestValidateBearer_ExpiredToken(t *testing.T) {
	idp := newFakeIdP(t)
	v, err := NewOIDC(context.Background(), OIDCConfig{Enabled: true, Issuer: idp.srv.URL})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	tok := idp.sign(t, jwt.MapClaims{
		"iss": idp.srv.URL,
		"sub": "alice",
		"exp": time.Now().Add(-time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	if _, err := v.ValidateBearer(context.Background(), tok); err == nil {
		t.Fatal("expired token: want error")
	}
}

func TestValidateBearer_EmptyToken(t *testing.T) {
	idp := newFakeIdP(t)
	v, err := NewOIDC(context.Background(), OIDCConfig{Enabled: true, Issuer: idp.srv.URL})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	if _, err := v.ValidateBearer(context.Background(), ""); err == nil {
		t.Fatal("empty token: want error")
	}
}

func TestValidateBearer_FallbackSubjectFromPreferredUsername(t *testing.T) {
	idp := newFakeIdP(t)
	v, err := NewOIDC(context.Background(), OIDCConfig{Enabled: true, Issuer: idp.srv.URL})
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	tok := idp.sign(t, jwt.MapClaims{
		"iss":                idp.srv.URL,
		"preferred_username": "bob",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
	})
	id, err := v.ValidateBearer(context.Background(), tok)
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "bob" {
		t.Errorf("subject fallback = %q, want bob", id.Subject)
	}
}
