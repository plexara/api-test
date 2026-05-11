package httpsrv

import (
	"fmt"
	"net/http"

	"github.com/plexara/api-test/pkg/oapi"
)

// mountOpenAPI mounts GET /openapi.json and GET /openapi.yaml backed by
// the pre-rendered document. Rendering happens once at boot; the registry
// is fixed after composition, so the bytes are valid for the process
// lifetime.
//
// Returns an error if either rendering step fails so server boot can
// surface the problem instead of silently serving 500s.
func mountOpenAPI(mux *http.ServeMux, doc oapi.Document) error {
	jsonBytes, err := oapi.RenderJSON(doc)
	if err != nil {
		return fmt.Errorf("render openapi.json: %w", err)
	}
	yamlBytes, err := oapi.RenderYAML(doc)
	if err != nil {
		return fmt.Errorf("render openapi.yaml: %w", err)
	}
	mux.HandleFunc("GET /openapi.json", serveBytes("application/json", jsonBytes))
	mux.HandleFunc("GET /openapi.yaml", serveBytes("application/yaml", yamlBytes))
	mux.HandleFunc("GET /docs", docsHandler())
	return nil
}

func serveBytes(contentType string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
