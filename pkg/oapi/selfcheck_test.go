package oapi

import (
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

func TestSelfCheck_Match(t *testing.T) {
	reg := newTestRegistry()
	doc := Build(reg, BuildOptions{Info: Info{Title: "t", Version: "v0"}})
	if err := SelfCheck(doc, reg); err != nil {
		t.Errorf("SelfCheck: %v", err)
	}
}

func TestSelfCheck_DocMissingRoute(t *testing.T) {
	reg := newTestRegistry()
	doc := Build(reg, BuildOptions{Info: Info{Title: "t", Version: "v0"}})

	// Drop one operation from the doc to simulate Build dropping a route.
	item := doc.Paths["/v1/items/{key}"]
	item.Put = nil
	doc.Paths["/v1/items/{key}"] = item

	err := SelfCheck(doc, reg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing from openapi doc") {
		t.Errorf("error text wrong: %v", err)
	}
	if !strings.Contains(err.Error(), "PUT /v1/items/{key}") {
		t.Errorf("error should name PUT route: %v", err)
	}
}

func TestSelfCheck_DocHasExtra(t *testing.T) {
	reg := newTestRegistry()
	doc := Build(reg, BuildOptions{Info: Info{Title: "t", Version: "v0"}})

	// Add an operation the registry doesn't know about.
	doc.Paths["/v1/ghost"] = PathItem{Get: &Operation{
		OperationID: "ghost",
		Responses:   map[string]Response{"200": {Description: "ok"}},
	}}

	err := SelfCheck(doc, reg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing from registry") {
		t.Errorf("error text wrong: %v", err)
	}
	if !strings.Contains(err.Error(), "GET /v1/ghost") {
		t.Errorf("error should name ghost route: %v", err)
	}
}

func TestSelfCheck_EmptyRegistry(t *testing.T) {
	reg := endpoints.NewRegistry()
	doc := Build(reg, BuildOptions{Info: Info{Title: "t", Version: "v0"}})
	if err := SelfCheck(doc, reg); err != nil {
		t.Errorf("empty registry should self-check clean: %v", err)
	}
}
