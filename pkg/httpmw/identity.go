package httpmw

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/plexara/api-test/pkg/auth/inbound"
)

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
func Identity(chain *inbound.Chain, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
