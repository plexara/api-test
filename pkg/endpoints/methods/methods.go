// Package methods provides a method-matrix endpoint that accepts every
// common HTTP verb at a single path and reports back the method it
// observed. Used to verify the gateway preserves HTTP verbs verbatim
// when forwarding (no GET→POST rewrites, no OPTIONS swallowed by a
// CORS pre-flight handler, etc.).
package methods

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

const groupName = "methods"

// supportedMethods is the matrix of verbs the endpoint accepts. OPTIONS
// is included so callers can sanity-check that the gateway's CORS layer
// (if any) doesn't strip pre-flight responses on the way back.
var supportedMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodHead,
	http.MethodOptions,
}

// Group implements endpoints.Endpoints for the methods group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints. One EndpointMeta entry per
// supported method so the OpenAPI generator and portal list each verb
// explicitly. The handler is shared across all of them.
func (Group) Routes() []endpoints.EndpointMeta {
	out := make([]endpoints.EndpointMeta, 0, len(supportedMethods))
	for _, m := range supportedMethods {
		out = append(out, endpoints.EndpointMeta{
			Name:         "method_" + strings.ToLower(m),
			Group:        groupName,
			Method:       m,
			Path:         "/v1/method/echo",
			Description:  "Echo the HTTP method the server observed. Verify the gateway preserves the request verb across forwarding.",
			ResponseBody: (*Response)(nil),
		})
	}
	return out
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	for _, m := range supportedMethods {
		mux.Handle(m+" /v1/method/echo", mw(http.HandlerFunc(g.handle)))
	}
}

// Response is the wire shape of /v1/method/echo. HEAD bodies are
// suppressed at the HTTP layer (Go's ResponseWriter discards body on
// HEAD), so the schema is documented here for the other verbs.
type Response struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  map[string][]string `json:"query,omitempty"`
}

func (g *Group) handle(w http.ResponseWriter, r *http.Request) {
	resp := Response{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		// Advertise the verb matrix in Allow so an OPTIONS probe is
		// informative even before the gateway forwards a real request.
		w.Header().Set("Allow", strings.Join(supportedMethods, ", "))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}
