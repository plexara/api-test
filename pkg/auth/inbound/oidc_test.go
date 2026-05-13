package inbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/config"
)

// stubValidator is a hand-rolled OIDCValidator: tokens whose payload
// segment decodes to the configured "iss" / "aud" pair succeed; anything
// else returns the configured error. The point is to exercise the
// adapter's chain semantics, not the real JWT verifier (which has its own
// tests in pkg/auth).
type stubValidator struct {
	wantIss string
	wantAud string
}

func (s stubValidator) ValidateBearer(_ context.Context, token string) (*auth.Identity, error) {
	parts := splitDots(token)
	if len(parts) != 3 {
		return nil, errors.New("not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}
	if iss, _ := claims["iss"].(string); iss != s.wantIss {
		return nil, fmt.Errorf("issuer mismatch: got %q want %q", iss, s.wantIss)
	}
	if aud, _ := claims["aud"].(string); aud != s.wantAud {
		return nil, fmt.Errorf("audience mismatch: got %q want %q", aud, s.wantAud)
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	return &auth.Identity{
		Subject:  sub,
		Email:    email,
		AuthType: "oidc",
		Claims:   claims,
	}, nil
}

func splitDots(s string) []string {
	out := []string{""}
	for _, c := range s {
		if c == '.' {
			out = append(out, "")
			continue
		}
		out[len(out)-1] += string(c)
	}
	return out
}

// makeJWT crafts a structurally-valid JWT-shaped string (header.payload.sig)
// from the supplied claims. The signature is opaque since stubValidator
// does not verify it.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("sig"))
}

func TestLooksLikeJWT(t *testing.T) {
	t.Run("valid shape", func(t *testing.T) {
		tok := makeJWT(t, map[string]any{"sub": "x"})
		if !looksLikeJWT(tok) {
			t.Error("crafted jwt should be detected")
		}
	})
	t.Run("static dev token", func(t *testing.T) {
		if looksLikeJWT("abc123") {
			t.Error("plain bearer token is not a jwt")
		}
	})
	t.Run("two segments", func(t *testing.T) {
		if looksLikeJWT("a.b") {
			t.Error("two-segment value is not a jwt")
		}
	})
	t.Run("header missing alg", func(t *testing.T) {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT"}`))
		tok := header + ".aGVsbG8.c2ln"
		if looksLikeJWT(tok) {
			t.Error("header without alg is not a usable jwt")
		}
	})
	t.Run("non-base64 header", func(t *testing.T) {
		if looksLikeJWT("@@@.b.c") {
			t.Error("non-base64 header is not a jwt")
		}
	})
}

func TestOIDCBearer_NoCredentialPaths(t *testing.T) {
	a := NewOIDCBearer(stubValidator{wantIss: "https://idp", wantAud: "api-test"})

	t.Run("no authorization header", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		if _, err := a.Authenticate(context.Background(), r); !errors.Is(err, ErrNoCredential) {
			t.Errorf("err=%v want ErrNoCredential", err)
		}
	})
	t.Run("non-bearer scheme", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		if _, err := a.Authenticate(context.Background(), r); !errors.Is(err, ErrNoCredential) {
			t.Errorf("err=%v want ErrNoCredential", err)
		}
	})
	t.Run("bearer but not a jwt (static dev token)", func(t *testing.T) {
		// This is the regression the ticket calls out: when a static dev
		// token reaches the OIDC adapter it must yield to the next
		// authenticator instead of failing the chain.
		r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
		r.Header.Set("Authorization", "Bearer abc123")
		if _, err := a.Authenticate(context.Background(), r); !errors.Is(err, ErrNoCredential) {
			t.Errorf("err=%v want ErrNoCredential", err)
		}
	})
}

func TestOIDCBearer_ValidJWT(t *testing.T) {
	a := NewOIDCBearer(stubValidator{wantIss: "https://idp", wantAud: "api-test"})
	tok := makeJWT(t, map[string]any{
		"iss":   "https://idp",
		"aud":   "api-test",
		"sub":   "alice",
		"email": "alice@example.com",
	})
	r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	id, err := a.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.Subject != "alice" || id.Email != "alice@example.com" || id.AuthType != "oidc" || id.KeyName != "alice" {
		t.Errorf("identity = %+v", id)
	}
	if id.Claims["iss"] != "https://idp" {
		t.Errorf("claims = %+v want iss=https://idp", id.Claims)
	}
}

func TestOIDCBearer_ForeignIssuer(t *testing.T) {
	// A real JWT that didn't come from our IdP must 401, not fall through
	// to BearerAuthenticator — otherwise a foreign-issued JWT could be
	// silently retried as a static bearer.
	a := NewOIDCBearer(stubValidator{wantIss: "https://idp", wantAud: "api-test"})
	tok := makeJWT(t, map[string]any{
		"iss": "https://attacker.example",
		"aud": "api-test",
		"sub": "mallory",
	})
	r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if _, err := a.Authenticate(context.Background(), r); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("err=%v want ErrInvalidCredential", err)
	}
}

func TestOIDCBearer_NilValidator(t *testing.T) {
	// Allow callers to register the adapter unconditionally even when OIDC
	// is disabled. Treat nil as "no opinion" rather than panicking.
	a := NewOIDCBearer(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	r.Header.Set("Authorization", "Bearer "+makeJWT(t, map[string]any{"sub": "x"}))
	if _, err := a.Authenticate(context.Background(), r); !errors.Is(err, ErrNoCredential) {
		t.Errorf("err=%v want ErrNoCredential", err)
	}
}

// TestChain_OIDCBeforeBearer is the end-to-end composition test the ticket
// asks for: register apikey + oidc + bearer and assert each credential type
// resolves to the right authenticator without the others stealing it.
func TestChain_OIDCBeforeBearer(t *testing.T) {
	apikeys := NewFileAPIKeyStore([]config.FileAPIKey{{Name: "ops", Key: "K"}})
	apikeyAuth := NewAPIKey(apikeys, "X-API-Key", "api_key")
	oidcAuth := NewOIDCBearer(stubValidator{wantIss: "https://idp", wantAud: "api-test"})
	bearerAuth := NewBearer([]config.FileBearerToken{{Name: "dev", Token: "DEVTOKEN"}})

	chain := NewChain(false, apikeyAuth, oidcAuth, bearerAuth)

	cases := []struct {
		name        string
		req         func() *http.Request
		wantSubject string
		wantType    string
		wantErr     error
	}{
		{
			name: "valid jwt resolves via oidc",
			req: func() *http.Request {
				tok := makeJWT(t, map[string]any{
					"iss": "https://idp",
					"aud": "api-test",
					"sub": "alice",
				})
				r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
				r.Header.Set("Authorization", "Bearer "+tok)
				return r
			},
			wantSubject: "alice",
			wantType:    "oidc",
		},
		{
			name: "foreign-issuer jwt aborts at oidc (no bearer fallthrough)",
			req: func() *http.Request {
				tok := makeJWT(t, map[string]any{
					"iss": "https://attacker.example",
					"aud": "api-test",
					"sub": "mallory",
				})
				r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
				r.Header.Set("Authorization", "Bearer "+tok)
				return r
			},
			wantErr: ErrInvalidCredential,
		},
		{
			name: "static dev bearer falls through oidc to bearer",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
				r.Header.Set("Authorization", "Bearer DEVTOKEN")
				return r
			},
			wantSubject: "dev",
			wantType:    "bearer",
		},
		{
			name: "api key still works alongside oidc",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
				r.Header.Set("X-API-Key", "K")
				return r
			},
			wantSubject: "ops",
			wantType:    "apikey",
		},
		{
			name: "no credential and anonymous disabled",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
			},
			wantErr: ErrNoCredential,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := chain.Authenticate(context.Background(), tc.req())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			if id.Subject != tc.wantSubject {
				t.Errorf("subject=%q want %q", id.Subject, tc.wantSubject)
			}
			if id.AuthType != tc.wantType {
				t.Errorf("authType=%q want %q", id.AuthType, tc.wantType)
			}
		})
	}
}
