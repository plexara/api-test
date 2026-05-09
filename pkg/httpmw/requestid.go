// Package httpmw provides request-scoped HTTP middleware: request-id
// propagation, identity resolution from the inbound auth chain, structured
// access logging, and the audit middleware that writes Event/Payload rows
// for each request.
//
// Composition this package expects:
//
//	RequestID -> AccessLog -> mux -> [per-route: Identity -> Audit] -> handler
//
// RequestID and AccessLog wrap the entire mux so health probes get
// request ids and access logs. Identity and Audit are per-route; the
// resolved Identity is stashed into a sharedIdentityRef set up by
// RequestID so AccessLog (which sees the request *before* Identity runs)
// can still read the resolved identity *after* the inner handler returns.
// Without that holder, AccessLog would always see nil because
// `r.WithContext(ctx)` inside Identity only flows downward.
package httpmw

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/plexara/api-test/pkg/auth/inbound"
)

// HeaderRequestID is the canonical header name; mirrors what most reverse
// proxies emit.
const HeaderRequestID = "X-Request-Id"

type (
	requestIDKey      struct{}
	identityHolderKey struct{}
)

// identityHolder is a request-scoped, single-goroutine-mutated container
// for the resolved inbound.Identity. Lets middleware that wraps the mux
// from outside (AccessLog) read what middleware that runs inside the
// mux (Identity) wrote, since `r.WithContext(...)` only flows downward.
type identityHolder struct {
	id *inbound.Identity
}

// RequestID returns middleware that ensures every request has an
// X-Request-Id (preserving an inbound one or generating a new UUID),
// stashes it in the context, echoes it on the response, and seeds the
// per-request identityHolder so downstream middleware can record/read
// the resolved inbound identity.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		ctx = context.WithValue(ctx, identityHolderKey{}, &identityHolder{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request id attached by RequestID, or
// "" if absent.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// recordIdentity stores id into the per-request holder when present.
// Called by the Identity middleware after the auth chain returns.
func recordIdentity(ctx context.Context, id *inbound.Identity) {
	h, _ := ctx.Value(identityHolderKey{}).(*identityHolder)
	if h != nil {
		h.id = id
	}
}

// resolvedIdentity returns the identity recorded into the holder, or nil
// if RequestID didn't seed a holder or Identity hasn't run yet.
func resolvedIdentity(ctx context.Context) *inbound.Identity {
	h, _ := ctx.Value(identityHolderKey{}).(*identityHolder)
	if h == nil {
		return nil
	}
	return h.id
}
