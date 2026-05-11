package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
	"github.com/plexara/api-test/pkg/oapi"
)

func TestBuildMux_OpenAPI_JSON(t *testing.T) {
	reg := endpoints.NewRegistry()
	reg.Add(stubGroup{})
	doc := oapi.Build(reg, oapi.BuildOptions{
		Info:    oapi.Info{Title: "api-test", Version: "v0"},
		Servers: []oapi.Server{{URL: "http://localhost:8080"}},
	})
	mux, err := BuildMux(reg, NewReadiness(), nil, nil, &doc)
	if err != nil {
		t.Fatalf("BuildMux: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", body["openapi"])
	}
	paths, _ := body["paths"].(map[string]any)
	if paths["/v1/ping"] == nil {
		t.Errorf("missing /v1/ping in paths: %v", paths)
	}
}

func TestBuildMux_OpenAPI_YAML(t *testing.T) {
	reg := endpoints.NewRegistry()
	reg.Add(stubGroup{})
	doc := oapi.Build(reg, oapi.BuildOptions{Info: oapi.Info{Title: "t", Version: "v0"}})
	mux, err := BuildMux(reg, NewReadiness(), nil, nil, &doc)
	if err != nil {
		t.Fatalf("BuildMux: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "openapi: 3.1.0") {
		t.Errorf("YAML missing openapi field; body:\n%s", w.Body.String())
	}
}

func TestBuildMux_OpenAPI_DisabledWhenNil(t *testing.T) {
	reg := endpoints.NewRegistry()
	reg.Add(stubGroup{})
	mux, err := BuildMux(reg, NewReadiness(), nil, nil, nil)
	if err != nil {
		t.Fatalf("BuildMux: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Errorf("status = 200, openapi should be off when nil doc passed")
	}
}
