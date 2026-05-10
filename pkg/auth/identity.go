// Package auth holds identity types and authenticators for portal browser
// sessions. It is distinct from pkg/auth/inbound, which handles the
// credentials Plexara API gateway connections present to api-test.
//
// The two packages are intentionally split:
//   - pkg/auth/inbound resolves "what creds did the upstream gateway send"
//     and produces an inbound.Identity attached to /v1/* request contexts.
//   - pkg/auth (this package) resolves "who's signed into the portal in
//     this browser" and produces an auth.Identity stored in the session
//     cookie and returned by /portal/api/whoami.
//
// They share concepts but not types; mixing them invites bugs where a
// portal-only field (e.g. browser session cookie) gets used as if it
// were a gateway-presented credential.
package auth

// Identity describes the authenticated portal user for a browser session.
// Populated by the OIDC PKCE callback handler (pkg/httpsrv/browserauth.go)
// from the validated ID token's claims, encoded into the session cookie
// by pkg/httpsrv/session.go.
type Identity struct {
	Subject  string         `json:"subject"`
	Email    string         `json:"email,omitempty"`
	Name     string         `json:"name,omitempty"`
	AuthType string         `json:"auth_type"` // "oidc" | "apikey" | "anonymous"
	Claims   map[string]any `json:"claims,omitempty"`
	APIKeyID string         `json:"api_key_id,omitempty"`
}

// Anonymous returns the identity used when allow_anonymous is true and no
// credentials are presented. Mirrors inbound.Anonymous but is not the same
// type; the two domains are kept separate.
func Anonymous() *Identity {
	return &Identity{Subject: "anonymous", AuthType: "anonymous"}
}
