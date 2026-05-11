package oapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

// fakeGroup is a minimal endpoints.Endpoints implementation used to
// exercise Build without depending on a concrete group package.
type fakeGroup struct {
	name   string
	routes []endpoints.EndpointMeta
}

func (g fakeGroup) Name() string                                   { return g.name }
func (g fakeGroup) Routes() []endpoints.EndpointMeta               { return g.routes }
func (g fakeGroup) Mount(_ *http.ServeMux, _ endpoints.Middleware) {}

type itemPath struct {
	Key string `json:"key"`
}

type itemQuery struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

type itemBody struct {
	Note string `json:"note"`
}

type itemResp struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func newTestRegistry() *endpoints.Registry {
	reg := endpoints.NewRegistry()
	reg.Add(fakeGroup{
		name: "items",
		routes: []endpoints.EndpointMeta{
			{
				Name:         "get_item",
				Group:        "items",
				Method:       http.MethodGet,
				Path:         "/v1/items/{key}",
				Description:  "Fetch one item by key.",
				PathParams:   (*itemPath)(nil),
				QueryParams:  (*itemQuery)(nil),
				ResponseBody: (*itemResp)(nil),
				AuthRequired: true,
			},
			{
				Name:         "put_item",
				Group:        "items",
				Method:       http.MethodPut,
				Path:         "/v1/items/{key}",
				PathParams:   (*itemPath)(nil),
				RequestBody:  (*itemBody)(nil),
				ResponseBody: (*itemResp)(nil),
			},
		},
	})
	return reg
}

func TestBuild_BasicShape(t *testing.T) {
	reg := newTestRegistry()
	doc := Build(reg, BuildOptions{
		Info:         Info{Title: "api-test", Version: "v0"},
		Servers:      []Server{{URL: "http://localhost:8080"}},
		APIKeyHeader: "X-API-Key",
	})

	if doc.OpenAPI != "3.1.0" {
		t.Errorf("OpenAPI = %q, want 3.1.0", doc.OpenAPI)
	}
	if doc.Info.Title != "api-test" {
		t.Errorf("Info.Title = %q", doc.Info.Title)
	}
	if got := len(doc.Paths); got != 1 {
		t.Fatalf("Paths count = %d, want 1 (same path for GET+PUT)", got)
	}

	item := doc.Paths["/v1/items/{key}"]
	if item.Get == nil || item.Put == nil {
		t.Fatalf("expected both GET and PUT under /v1/items/{key}")
	}
	if item.Get.OperationID != "get_item" {
		t.Errorf("Get.OperationID = %q", item.Get.OperationID)
	}
	if item.Get.Tags[0] != "items" {
		t.Errorf("Get.Tags[0] = %q", item.Get.Tags[0])
	}
}

func TestBuild_PathAndQueryParams(t *testing.T) {
	doc := Build(newTestRegistry(), BuildOptions{
		Info:         Info{Title: "t", Version: "v0"},
		APIKeyHeader: "X-API-Key",
	})
	get := doc.Paths["/v1/items/{key}"].Get
	if get == nil {
		t.Fatal("GET nil")
	}

	var sawPath, sawLimit, sawCursor bool
	for _, p := range get.Parameters {
		switch {
		case p.Name == "key" && p.In == "path":
			sawPath = true
			if !p.Required {
				t.Errorf("path param key should be required")
			}
			if p.Schema.Type != "string" {
				t.Errorf("path param key schema = %+v", p.Schema)
			}
		case p.Name == "limit" && p.In == "query":
			sawLimit = true
			if !p.Required {
				t.Errorf("limit (no omitempty) should be required")
			}
			if p.Schema.Type != "integer" {
				t.Errorf("limit schema type = %q", p.Schema.Type)
			}
		case p.Name == "cursor" && p.In == "query":
			sawCursor = true
			if p.Required {
				t.Errorf("cursor (omitempty) should not be required")
			}
		}
	}
	if !sawPath || !sawLimit || !sawCursor {
		t.Errorf("missing param: path=%v limit=%v cursor=%v (params=%+v)",
			sawPath, sawLimit, sawCursor, get.Parameters)
	}
}

func TestBuild_RequestAndResponse(t *testing.T) {
	doc := Build(newTestRegistry(), BuildOptions{
		Info:         Info{Title: "t", Version: "v0"},
		APIKeyHeader: "X-API-Key",
	})
	put := doc.Paths["/v1/items/{key}"].Put
	if put == nil {
		t.Fatal("PUT nil")
	}
	if put.RequestBody == nil {
		t.Fatal("PUT.RequestBody nil")
	}
	media, ok := put.RequestBody.Content["application/json"]
	if !ok || media.Schema == nil || media.Schema.Type != "object" {
		t.Errorf("request body shape wrong: %+v", put.RequestBody)
	}
	if media.Schema.Properties["note"].Type != "string" {
		t.Errorf("request body note schema wrong")
	}

	resp, ok := put.Responses["200"]
	if !ok {
		t.Fatal("missing 200 response")
	}
	rmedia, ok := resp.Content["application/json"]
	if !ok || rmedia.Schema == nil {
		t.Fatal("response 200 application/json missing")
	}
	if rmedia.Schema.Properties["value"].Type != "string" {
		t.Errorf("response shape wrong")
	}
}

func TestBuild_Security(t *testing.T) {
	doc := Build(newTestRegistry(), BuildOptions{
		Info:          Info{Title: "t", Version: "v0"},
		APIKeyHeader:  "X-API-Key",
		BearerEnabled: true,
	})
	if doc.Components == nil || doc.Components.SecuritySchemes == nil {
		t.Fatal("expected components.securitySchemes")
	}
	if doc.Components.SecuritySchemes["apiKey"].In != "header" {
		t.Errorf("apiKey scheme not header")
	}
	if doc.Components.SecuritySchemes["bearer"].Scheme != "bearer" {
		t.Errorf("bearer scheme name wrong")
	}

	get := doc.Paths["/v1/items/{key}"].Get
	if len(get.Security) == 0 {
		t.Errorf("auth_required op should carry security requirement")
	}
	put := doc.Paths["/v1/items/{key}"].Put
	if len(put.Security) != 0 {
		t.Errorf("non-auth_required op should have no security: %+v", put.Security)
	}
}

func TestBuild_TagsSorted(t *testing.T) {
	reg := endpoints.NewRegistry()
	reg.Add(fakeGroup{name: "zeta"})
	reg.Add(fakeGroup{name: "alpha"})
	reg.Add(fakeGroup{name: "mu"})
	doc := Build(reg, BuildOptions{Info: Info{Title: "t", Version: "v0"}})
	got := make([]string, 0, len(doc.Tags))
	for _, tg := range doc.Tags {
		got = append(got, tg.Name)
	}
	want := []string{"alpha", "mu", "zeta"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Tags[%d]=%q, want %q (got %v)", i, got[i], w, got)
		}
	}
}

func TestRenderJSON_RoundTrip(t *testing.T) {
	doc := Build(newTestRegistry(), BuildOptions{
		Info:         Info{Title: "t", Version: "v0"},
		APIKeyHeader: "X-API-Key",
	})
	b, err := RenderJSON(doc)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back["openapi"] != "3.1.0" {
		t.Errorf("openapi field missing/wrong: %v", back["openapi"])
	}
	paths, ok := back["paths"].(map[string]any)
	if !ok || paths["/v1/items/{key}"] == nil {
		t.Errorf("paths missing: %v", back["paths"])
	}
}

func TestRenderYAML(t *testing.T) {
	doc := Build(newTestRegistry(), BuildOptions{
		Info: Info{Title: "t", Version: "v0"},
	})
	b, err := RenderYAML(doc)
	if err != nil {
		t.Fatalf("RenderYAML: %v", err)
	}
	if len(b) == 0 {
		t.Fatalf("empty YAML output")
	}
}
