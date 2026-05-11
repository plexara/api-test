package httpsrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
	"github.com/plexara/api-test/pkg/oapi"
)

func TestBuildMux_Docs(t *testing.T) {
	reg := endpoints.NewRegistry()
	reg.Add(stubGroup{})
	doc := oapi.Build(reg, oapi.BuildOptions{Info: oapi.Info{Title: "t", Version: "v0"}})
	mux, err := BuildMux(reg, NewReadiness(), nil, nil, &doc)
	if err != nil {
		t.Fatalf("BuildMux: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `spec-url="/openapi.json"`) {
		t.Errorf("docs HTML does not point at /openapi.json:\n%s", body)
	}
}
