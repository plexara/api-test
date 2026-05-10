package httpsrv

import (
	"net/http"

	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/auth/inbound"
)

// PortalAuth resolves the caller's identity for /portal/api/* routes from
// either:
//  1. A signed session cookie (browser flow), or
//  2. An X-API-Key header / Authorization: Bearer header (script clients).
//
// On success the *auth.Identity is attached to the request context. On failure
// a 401 is served; anonymous access is intentionally NOT honored on portal
// routes even when auth.allow_anonymous is true, because the portal exposes
// audit data and admin actions.
type PortalAuth struct {
	sessions *SessionStore
	chain    *inbound.Chain
}

// NewPortalAuth returns the middleware factory. chain may be nil if no
// API-key fallback is desired.
func NewPortalAuth(sessions *SessionStore, chain *inbound.Chain) *PortalAuth {
	return &PortalAuth{sessions: sessions, chain: chain}
}

// Middleware returns an http.Handler middleware that requires authentication.
func (p *PortalAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// 1. Cookie path.
		if p.sessions != nil {
			if id := p.sessions.Read(r); id != nil {
				ctx = auth.WithIdentity(ctx, id)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 2. API-key / Bearer fallback for script clients. Reuse the inbound
		// auth chain so portal scripting and gateway calls share credentials.
		if hasCredential(r) && p.chain != nil {
			id, err := p.chain.Authenticate(ctx, r)
			if err == nil && id != nil && id.AuthType != "anonymous" {
				ctx = auth.WithIdentity(ctx, adaptInboundIdentity(id))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		w.Header().Set("WWW-Authenticate", `Bearer realm="api-test-portal"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})
}

// adaptInboundIdentity translates an inbound.Identity (gateway-presented
// credential) into an auth.Identity (portal session). The two types are
// kept separate by design (see pkg/auth doc); this function is the single
// approved bridge between them.
func adaptInboundIdentity(in *inbound.Identity) *auth.Identity {
	if in == nil {
		return nil
	}
	return &auth.Identity{
		Subject:  in.Subject,
		Email:    in.Email,
		Name:     in.KeyName,
		AuthType: in.AuthType,
		APIKeyID: in.KeyName,
		Claims:   in.Claims,
	}
}
