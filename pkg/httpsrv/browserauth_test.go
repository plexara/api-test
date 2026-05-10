package httpsrv

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/config"
)

// ----- pure helpers (no fixture needed) -----

func TestSanitizeReturnPath(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"/portal/":     "/portal/",
		"/portal/keys": "/portal/keys",
		"//evil.com":   "",
		`/\evil`:       "",
		"http://x":     "",
		"portal/keys":  "",
		"/":            "/",
	}
	for in, want := range cases {
		if got := sanitizeReturnPath(in); got != want {
			t.Errorf("sanitizeReturnPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPKCEChallengeMatchesSpec(t *testing.T) {
	// RFC 7636 §4.2: code_challenge = BASE64URL-ENCODE(SHA256(verifier)).
	// Verify the helper produces exactly that, computed live so the test
	// can't drift from the spec via a stale pre-computed constant.
	verifier := "M25iVXpKU3puUjFaYWg3T1NDTDQtcW1ROUY5YXlwalNoc0hhakxifmZHag"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := pkceChallenge(verifier); got != want {
		t.Errorf("pkceChallenge = %q, want %q", got, want)
	}
	// Determinism: same verifier always produces the same challenge.
	if pkceChallenge(verifier) != pkceChallenge(verifier) {
		t.Error("pkceChallenge non-deterministic")
	}
}

func TestRandomString_LengthAndUniqueness(t *testing.T) {
	a, err := randomString(16)
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	if len(a) == 0 {
		t.Error("randomString returned empty")
	}
	b, _ := randomString(16)
	if a == b {
		t.Error("randomString produced colliding values; vanishingly unlikely")
	}
}

func TestDerivePKCESecret_Stable(t *testing.T) {
	s1 := derivePKCESecret("0123456789abcdef")
	s2 := derivePKCESecret("0123456789abcdef")
	s3 := derivePKCESecret("different-cookie-secret")
	if s1 != s2 {
		t.Error("derivePKCESecret should be deterministic")
	}
	if s1 == s3 {
		t.Error("derivePKCESecret with different cookie secret should not match")
	}
}

func TestConsumeNonce_OneShot(t *testing.T) {
	b := &BrowserAuth{usedNonces: map[string]time.Time{}}
	if !b.consumeNonce("n1") {
		t.Error("first consume should succeed")
	}
	if b.consumeNonce("n1") {
		t.Error("second consume should fail (replay)")
	}
	if b.consumeNonce("") {
		t.Error("empty nonce should fail")
	}
}

func TestConsumeNonce_GCStaleEntries(t *testing.T) {
	b := &BrowserAuth{usedNonces: map[string]time.Time{
		"old": time.Now().Add(-30 * time.Minute),
	}}
	if !b.consumeNonce("new") {
		t.Error("should accept new nonce")
	}
	b.usedNoncesMu.Lock()
	_, stillThere := b.usedNonces["old"]
	b.usedNoncesMu.Unlock()
	if stillThere {
		t.Error("stale nonce should have been GC'd")
	}
}

// ----- fixture: a minimal IdP that supports PKCE callback -----

type pkceTestIdP struct {
	srv  *httptest.Server
	priv *rsa.PrivateKey
	kid  string

	mu        sync.Mutex
	codeStore map[string]string // code -> issued id_token
}

func newPKCEIdP(t *testing.T) *pkceTestIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	idp := &pkceTestIdP{priv: priv, kid: "k1", codeStore: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.srv.URL,
			"jwks_uri":                              idp.srv.URL + "/jwks",
			"authorization_endpoint":                idp.srv.URL + "/authorize",
			"token_endpoint":                        idp.srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{
				{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": idp.kid,
					"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
					"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())},
			},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.Form.Get("code")
		idp.mu.Lock()
		tok, ok := idp.codeStore[code]
		delete(idp.codeStore, code)
		idp.mu.Unlock()
		if !ok {
			http.Error(w, "unknown code", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": tok, "access_token": "ignored"})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *pkceTestIdP) issueCode(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tk := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tk.Header["kid"] = i.kid
	signed, err := tk.SignedString(i.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	code := "code-" + signed[:8]
	i.mu.Lock()
	i.codeStore[code] = signed
	i.mu.Unlock()
	return code
}

func newBrowserAuthForTest(t *testing.T) (*BrowserAuth, *pkceTestIdP, *SessionStore) {
	t.Helper()
	idp := newPKCEIdP(t)
	cfg := &config.Config{}
	cfg.Server.BaseURL = "http://localhost:8080"
	cfg.Portal.CookieSecret = "0123456789abcdef-cs"
	cfg.Portal.OIDCRedirectPath = "/portal/auth/callback"
	cfg.OIDC.Enabled = true
	cfg.OIDC.Issuer = idp.srv.URL
	cfg.OIDC.ClientID = "web"
	validator, err := auth.NewOIDC(context.Background(), cfg.OIDC)
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	sessions, err := NewSessionStore("c", cfg.Portal.CookieSecret, false, time.Hour)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	ba, err := NewBrowserAuth(context.Background(), cfg, validator, sessions, slog.Default())
	if err != nil {
		t.Fatalf("NewBrowserAuth: %v", err)
	}
	return ba, idp, sessions
}

func TestBrowserAuth_LoginRedirectsToIdPWithPKCEParams(t *testing.T) {
	ba, idp, _ := newBrowserAuthForTest(t)
	mux := http.NewServeMux()
	ba.Mount(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/portal/auth/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	if !strings.HasPrefix(loc, idp.srv.URL+"/authorize") {
		t.Errorf("Location host = %q, want %s/authorize prefix", loc, idp.srv.URL)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Errorf("PKCE params missing: %v", q)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login should set the PKCE cookie")
	}
}

func TestBrowserAuth_CallbackHappyPath(t *testing.T) {
	ba, idp, sessions := newBrowserAuthForTest(t)
	mux := http.NewServeMux()
	ba.Mount(mux)

	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/portal/auth/login?return=/portal/keys", nil))
	loc := loginRec.Header().Get("Location")
	authzURL, _ := url.Parse(loc)
	state := authzURL.Query().Get("state")
	pkceCookie := loginRec.Result().Cookies()[0]

	code := idp.issueCode(t, jwt.MapClaims{
		"iss":   idp.srv.URL,
		"sub":   "alice",
		"email": "alice@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})

	cb := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?state="+state+"&code="+code, nil)
	cb.AddCookie(pkceCookie)
	cbRec := httptest.NewRecorder()
	mux.ServeHTTP(cbRec, cb)

	if cbRec.Code != http.StatusFound {
		body, _ := io.ReadAll(cbRec.Body)
		t.Fatalf("callback status = %d, body=%s", cbRec.Code, body)
	}
	if got := cbRec.Header().Get("Location"); got != "/portal/keys" {
		t.Errorf("post-callback Location = %q, want /portal/keys (sanitized return)", got)
	}
	// And the session cookie should now read back as alice.
	sessCookie := findCookie(cbRec.Result().Cookies(), sessions.CookieName())
	if sessCookie == nil {
		t.Fatal("session cookie not set")
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(sessCookie)
	id := sessions.Read(r)
	if id == nil || id.Subject != "alice" {
		t.Errorf("session identity = %+v, want subject=alice", id)
	}
}

func TestBrowserAuth_CallbackRejectsStateMismatch(t *testing.T) {
	ba, _, _ := newBrowserAuthForTest(t)
	mux := http.NewServeMux()
	ba.Mount(mux)

	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, httptest.NewRequest(http.MethodGet, "/portal/auth/login", nil))
	pkceCookie := loginRec.Result().Cookies()[0]

	cb := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?state=wrong&code=x", nil)
	cb.AddCookie(pkceCookie)
	cbRec := httptest.NewRecorder()
	mux.ServeHTTP(cbRec, cb)
	if cbRec.Code != http.StatusBadRequest {
		t.Errorf("state mismatch status = %d, want 400", cbRec.Code)
	}
}

func TestBrowserAuth_CallbackMissingCookie(t *testing.T) {
	ba, _, _ := newBrowserAuthForTest(t)
	mux := http.NewServeMux()
	ba.Mount(mux)

	cb := httptest.NewRequest(http.MethodGet, "/portal/auth/callback?state=x&code=x", nil)
	cbRec := httptest.NewRecorder()
	mux.ServeHTTP(cbRec, cb)
	if cbRec.Code != http.StatusBadRequest {
		t.Errorf("no cookie status = %d, want 400", cbRec.Code)
	}
}

func TestBrowserAuth_Logout(t *testing.T) {
	ba, _, _ := newBrowserAuthForTest(t)
	mux := http.NewServeMux()
	ba.Mount(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/portal/auth/logout", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("logout status = %d, want 204", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 || cookies[0].MaxAge >= 0 {
		t.Errorf("logout did not clear session cookie: %+v", cookies)
	}
}

func TestNewBrowserAuth_RejectsDisabledOIDC(t *testing.T) {
	cfg := &config.Config{}
	cfg.Portal.CookieSecret = "0123456789abcdef-x"
	if _, err := NewBrowserAuth(context.Background(), cfg, nil, nil, slog.Default()); err == nil {
		t.Error("NewBrowserAuth with oidc disabled: want error")
	}
}

func findCookie(cs []*http.Cookie, name string) *http.Cookie {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return nil
}
