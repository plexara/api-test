package httpsrv

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/plexara/api-test/pkg/config"
	"github.com/plexara/api-test/pkg/endpoints"
	"github.com/plexara/api-test/pkg/oapi"
)

// PortalDeps bundles everything needed to mount the portal under /portal/
// and /portal/api/*. Pass nil to BuildMux to disable the portal entirely.
type PortalDeps struct {
	Cfg         *config.Config
	SPA         fs.FS // when nil, /portal/ serves a JSON stub
	BrowserAuth *BrowserAuth
	PortalAuth  *PortalAuth
	PortalAPI   *PortalAPI
}

// BuildMux assembles the HTTP mux:
//   - /healthz, /readyz
//   - /v1/* endpoint groups (with the supplied middleware)
//   - /openapi.json, /openapi.yaml when oapiDoc != nil
//   - /.well-known/oauth-protected-resource and /.well-known/oauth-authorization-server
//   - /portal/, /portal/api/*, /portal/auth/{login,callback,logout} when portal != nil
//   - / — root handler returning a JSON banner (or a redirect to the portal
//     when the request looks like a browser GET)
//
// Returns an error only when oapiDoc rendering fails; callers that pass
// nil get a nil error.
func BuildMux(
	registry *endpoints.Registry,
	readiness *Readiness,
	mw endpoints.Middleware,
	portal *PortalDeps,
	oapiDoc *oapi.Document,
) (http.Handler, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", HealthzHandler())
	mux.HandleFunc("GET /readyz", readiness.ReadyzHandler())

	if mw == nil {
		mw = endpoints.PassthroughMiddleware
	}
	registry.Mount(mux, mw)

	if oapiDoc != nil {
		if err := mountOpenAPI(mux, *oapiDoc); err != nil {
			return nil, err
		}
	}

	if portal != nil {
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", ProtectedResourceMetadata(portal.Cfg))
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", AuthorizationServerStub(portal.Cfg))

		if portal.BrowserAuth != nil {
			portal.BrowserAuth.Mount(mux)
		}
		if portal.PortalAPI != nil && portal.PortalAuth != nil {
			portal.PortalAPI.Mount(mux, portal.PortalAuth.Middleware)
		}
		mux.Handle("GET /portal/", http.StripPrefix("/portal", spaOrStub(portal.SPA)))
	}

	mux.HandleFunc("GET /", rootHandler(registry, portal != nil))

	var handler http.Handler = mux
	if portal != nil {
		// Bounce browser GETs at "/" to /portal/ so a curl returns the JSON
		// banner but a browser visit lands on the SPA.
		handler = BrowserRedirect("/portal/", handler)
	}
	return CORS(handler), nil
}

// spaOrStub serves the SPA when spaFS is non-nil; otherwise emits a small
// JSON stub explaining how to build the UI. Used by `make dev` runs that
// haven't built the SPA yet so the binary still starts cleanly.
func spaOrStub(spaFS fs.FS) http.Handler {
	if spaFS != nil {
		return SPAHandler(spaFS)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "ui not built",
			"detail": "internal/ui/dist is empty; run `make ui` and rebuild the binary",
		})
	})
}

// rootHandler returns a small JSON banner at "/" so a curl to the bare host
// gets a useful response (a list of mounted endpoint groups + version
// pointer to /healthz). When the portal is enabled, the banner advertises
// the portal URL and BrowserRedirect handles browser GETs upstream.
func rootHandler(registry *endpoints.Registry, portalEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			writeJSONError(w, http.StatusNotFound, "no such endpoint")
			return
		}
		groups := make([]string, 0, len(registry.Groups()))
		for _, g := range registry.Groups() {
			groups = append(groups, g.Name())
		}
		links := map[string]string{
			"healthz":      "/healthz",
			"readyz":       "/readyz",
			"openapi_json": "/openapi.json",
			"openapi_yaml": "/openapi.yaml",
			"docs":         "/docs",
		}
		if portalEnabled {
			links["portal"] = "/portal/"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":            "api-test",
			"endpoint_groups": groups,
			"endpoints":       len(registry.All()),
			"links":           links,
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
