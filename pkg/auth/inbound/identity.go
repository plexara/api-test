// Package inbound validates the credentials a Plexara API gateway connection
// presents to api-test. The chain implements the Authenticator interface
// over multiple credential sources (file API keys, DB API keys, static
// bearer tokens, OIDC bearer tokens) and resolves them into an Identity
// that downstream handlers and the audit middleware can read off the
// request context.
//
// OIDC validation lands in M3 (Keycloak); M2 ships file/DB API key + bearer.
package inbound

import (
	"context"
)

// Identity is the resolved caller identity for an inbound HTTP request.
// Populated by the inbound auth middleware (pkg/httpmw) and stashed in
// the request context for handlers and the audit middleware.
type Identity struct {
	// Subject is the canonical principal identifier: API key name, bearer
	// token name, or OIDC subject claim.
	Subject string

	// Email is best-effort; populated for OIDC tokens that carry the
	// claim. Empty for API-key / static-bearer auth.
	Email string

	// AuthType is the high-level auth scheme: "anonymous", "apikey",
	// "bearer", "oauth2". Used by the audit row.
	AuthType string

	// KeyName is the credential's display name (e.g. "devkey"). For OIDC
	// it's the validated client_id. Empty for anonymous.
	KeyName string

	// Scopes is the list of OAuth2 scopes (or roles) granted.
	Scopes []string

	// Claims is the raw OIDC claim map for debugging/portal display.
	// Nil for non-OIDC auth.
	Claims map[string]any
}

// Anonymous returns an Identity for unauthenticated requests when the
// server is configured with auth.allow_anonymous=true.
func Anonymous() *Identity {
	return &Identity{Subject: "", AuthType: "anonymous"}
}

// ctxKey is the package-private context key type so other packages can't
// stomp on our value.
type ctxKey struct{}

// WithIdentity returns ctx with id attached. Used by the inbound auth
// middleware (pkg/httpmw/identity.go) right before invoking the handler.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the Identity attached to ctx, or nil if absent.
// Handlers should treat nil as "auth chain hasn't run yet" — distinct
// from anonymous, which has a non-nil Identity with AuthType="anonymous".
func FromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(ctxKey{}).(*Identity)
	return id
}
