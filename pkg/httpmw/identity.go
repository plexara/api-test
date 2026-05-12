package httpmw

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/auth/inbound"
)

// hasInboundCredential reports whether the request carries a credential
// the inbound chain could resolve: an X-API-Key header, an ?api_key=
// query param, or an Authorization: Bearer token. Used to decide
// whether the pre-identity bypass should yield to a wire-level
// credential.
//
// TODO: this hardcodes the default api-key header/query names. If a
// deployment customizes APIKeysConfig.HeaderName / QueryParamName, an
// operator-typed credential using the custom name will silently slip
// past the predicate and the pre-identity bypass will win instead of
// the chain. The api-test-server.plexara.io deployment runs the
// defaults so this is not a live regression; revisit when wiring
// Identity() to know about the configured names (or when the chain
// gains a HasCredential method that authenticators implement).
func hasInboundCredential(r *http.Request) bool {
	if r.Header.Get("X-API-Key") != "" {
		return true
	}
	if r.URL != nil && r.URL.Query().Get("api_key") != "" {
		return true
	}
	a := r.Header.Get("Authorization")
	return a != "" && strings.HasPrefix(strings.ToLower(a), "bearer ")
}

// Identity returns middleware that runs the inbound auth chain and
// either:
//   - attaches the resolved Identity to the context and proceeds, or
//   - responds 401 with a JSON error envelope when the chain rejects.
//
// Anonymous fallback is the chain's responsibility (controlled by
// auth.allow_anonymous in config); this middleware just enforces the
// chain's verdict.
//
// The "401 includes WWW-Authenticate" RFC 6750 convention is honored:
// requests that came in without a credential get a Bearer challenge.
//
// When an inbound.Identity is already present on the request context AND
// no credential is on the wire, the chain is bypassed. This is the
// Try-It / audit-replay dispatch path: the portal handler has already
// auth'd the operator via the portal session, so re-running the inbound
// chain (which only knows about API keys / bearer tokens on the wire)
// would 401 a request the portal already accepted. If the dispatched
// request DOES carry a credential header (e.g. an operator typed
// X-API-Key into the Try-It headers field to test as a different
// principal), the chain runs and wins — that's the documented way to
// "test as someone else" through Try-It.
func Identity(chain *inbound.Chain, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if pre := inbound.FromContext(r.Context()); pre != nil && !hasInboundCredential(r) {
				recordIdentity(r.Context(), pre)
				next.ServeHTTP(w, r)
				return
			}
			id, err := chain.Authenticate(r.Context(), r)
			if err != nil {
				if errors.Is(err, inbound.ErrNoCredential) {
					w.Header().Set("WWW-Authenticate", `Bearer realm="api-test"`)
					writeJSONError(w, http.StatusUnauthorized, "missing credential")
					return
				}
				if errors.Is(err, inbound.ErrInvalidCredential) {
					w.Header().Set("WWW-Authenticate", `Bearer realm="api-test", error="invalid_token"`)
					writeJSONError(w, http.StatusUnauthorized, "invalid credential")
					return
				}
				logger.Warn("auth chain error", "err", err, "path", r.URL.Path)
				writeJSONError(w, http.StatusInternalServerError, "auth error")
				return
			}
			ctx := inbound.WithIdentity(r.Context(), id)
			// Mirror into the per-request holder seeded by RequestID so
			// AccessLog (which wraps the mux from outside) can read the
			// resolved identity after the inner handler returns.
			recordIdentity(ctx, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
