package inbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/auth"
)

// OIDCValidator is the subset of pkg/auth.OIDCAuthenticator the inbound
// adapter needs. Defined as an interface so tests can stub validation
// without standing up a fake IdP.
type OIDCValidator interface {
	ValidateBearer(ctx context.Context, token string) (*auth.Identity, error)
}

// OIDCBearerAuthenticator adapts an OIDC JWT validator to the inbound
// chain. It guards the /v1/* surface so callers can authenticate with
// `Authorization: Bearer <jwt>` issued by the configured IdP.
//
// Chain semantics:
//   - No Authorization header / non-Bearer scheme → ErrNoCredential.
//   - Bearer value that is not a structurally-valid JWT → ErrNoCredential.
//     This lets a static dev token fall through to BearerAuthenticator
//     when both are registered in the chain.
//   - Structurally-valid JWT that fails signature/issuer/audience/expiry
//     checks → ErrInvalidCredential (no fallthrough — a real JWT that
//     fails verification must 401, not be retried as a static bearer).
type OIDCBearerAuthenticator struct {
	validator OIDCValidator
}

// NewOIDCBearer returns an OIDCBearerAuthenticator wrapping v. A nil
// validator yields an authenticator that always returns ErrNoCredential,
// so callers can register the adapter unconditionally.
func NewOIDCBearer(v OIDCValidator) *OIDCBearerAuthenticator {
	return &OIDCBearerAuthenticator{validator: v}
}

// Authenticate implements Authenticator.
func (a *OIDCBearerAuthenticator) Authenticate(ctx context.Context, r *http.Request) (*Identity, error) {
	if a == nil || a.validator == nil {
		return nil, ErrNoCredential
	}
	token := extractBearer(r.Header.Get("Authorization"))
	if token == "" {
		return nil, ErrNoCredential
	}
	if !looksLikeJWT(token) {
		return nil, ErrNoCredential
	}
	aid, err := a.validator.ValidateBearer(ctx, token)
	if err != nil {
		// Wrap both the sentinel (so callers can errors.Is for chain
		// semantics) and the underlying verifier error (so operators see
		// why the JWT was rejected in logs).
		return nil, fmt.Errorf("oidc: %w: %w", ErrInvalidCredential, err)
	}
	subject := aid.Subject
	if subject == "" {
		// ValidateBearer already falls back to preferred_username; this is
		// just a safety net so an audit row never has an empty subject.
		subject = "oidc-subject-missing"
	}
	return &Identity{
		Subject:  subject,
		Email:    aid.Email,
		AuthType: "oidc",
		KeyName:  subject,
		Claims:   aid.Claims,
	}, nil
}

// looksLikeJWT is a cheap structural check: three dot-separated segments
// where the first segment base64url-decodes to JSON carrying a non-empty
// "alg" header. We gate the IdP-aware validator behind this so a static
// dev token like "abc123" returns ErrNoCredential and the chain falls
// through to BearerAuthenticator.
func looksLikeJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(header, &h); err != nil {
		return false
	}
	return h.Alg != ""
}
