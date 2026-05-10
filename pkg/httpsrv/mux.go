package httpsrv

import (
	"encoding/json"
	"net/http"

	"github.com/plexara/api-test/pkg/endpoints"
)

// BuildMux assembles the M1 HTTP mux: /healthz, /readyz, the endpoint
// groups under /v1, and a friendly JSON 404 root. Later milestones will
// extend this with /.well-known/*, the portal SPA at /portal/, the
// portal/admin APIs at /api/v1/portal/, and the OpenAPI doc.
func BuildMux(registry *endpoints.Registry, readiness *Readiness, mw endpoints.Middleware) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", HealthzHandler())
	mux.HandleFunc("GET /readyz", readiness.ReadyzHandler())

	if mw == nil {
		mw = endpoints.PassthroughMiddleware
	}
	registry.Mount(mux, mw)

	mux.HandleFunc("GET /", rootHandler(registry))

	return CORS(mux)
}

// rootHandler returns a small JSON banner at "/" so a curl to the bare host
// gets a useful response (a list of mounted endpoint groups + version
// pointer to /healthz). M3 will replace this with a 302 to /portal/ when
// the SPA is enabled.
func rootHandler(registry *endpoints.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only handle the literal root; everything else here means "no
		// matching route" and should be a JSON 404.
		if r.URL.Path != "/" {
			writeJSONError(w, http.StatusNotFound, "no such endpoint")
			return
		}
		groups := make([]string, 0, len(registry.Groups()))
		for _, g := range registry.Groups() {
			groups = append(groups, g.Name())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":            "api-test",
			"endpoint_groups": groups,
			"endpoints":       len(registry.All()),
			"links": map[string]string{
				"healthz": "/healthz",
				"readyz":  "/readyz",
			},
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
