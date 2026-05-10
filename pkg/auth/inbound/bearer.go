package inbound

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/config"
)

// BearerAuthenticator validates `Authorization: Bearer <token>` against a
// fixed list of (name, token) pairs from config.
//
// OIDC-style bearer-JWT validation lives in oauth2.go (M3); both
// authenticators read the Authorization header but they're disjoint: the
// chain tries the OIDC validator first when configured, falling back to
// the static list, so a JWT from Keycloak doesn't accidentally match a
// static token.
type BearerAuthenticator struct {
	tokens []config.FileBearerToken
}

// NewBearer returns a BearerAuthenticator over the provided token list.
func NewBearer(tokens []config.FileBearerToken) *BearerAuthenticator {
	cp := make([]config.FileBearerToken, len(tokens))
	copy(cp, tokens)
	return &BearerAuthenticator{tokens: cp}
}

// Authenticate implements Authenticator.
func (b *BearerAuthenticator) Authenticate(_ context.Context, r *http.Request) (*Identity, error) {
	candidate := extractBearer(r.Header.Get("Authorization"))
	if candidate == "" {
		return nil, ErrNoCredential
	}
	cb := []byte(candidate)
	matched := ""
	for _, t := range b.tokens {
		if subtle.ConstantTimeCompare(cb, []byte(t.Token)) == 1 {
			matched = t.Name
		}
	}
	if matched == "" {
		return nil, ErrInvalidCredential
	}
	return &Identity{
		Subject:  matched,
		AuthType: "bearer",
		KeyName:  matched,
	}, nil
}

// extractBearer returns the token portion of an "Authorization: Bearer X"
// header value, or empty when the scheme isn't Bearer.
func extractBearer(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(authHeader) < len(prefix) {
		return ""
	}
	if !strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(authHeader[len(prefix):])
}
