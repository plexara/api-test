package oapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

// SelfCheck asserts that the rendered Document and the source Registry
// describe the same set of (method, path) routes. It catches:
//
//   - oapi.Build silently dropping a route (regression in this package).
//   - A future caller mutating the doc between Build and serve in a way
//     that desyncs from the registry.
//
// Called at boot from the composition layer; failure aborts startup.
func SelfCheck(doc Document, reg *endpoints.Registry) error {
	docRoutes := docRouteSet(doc)
	regRoutes := registryRouteSet(reg)

	var missingInDoc, missingInRegistry []string
	for r := range regRoutes {
		if !docRoutes[r] {
			missingInDoc = append(missingInDoc, r)
		}
	}
	for r := range docRoutes {
		if !regRoutes[r] {
			missingInRegistry = append(missingInRegistry, r)
		}
	}
	if len(missingInDoc) == 0 && len(missingInRegistry) == 0 {
		return nil
	}
	sort.Strings(missingInDoc)
	sort.Strings(missingInRegistry)
	var parts []string
	if len(missingInDoc) > 0 {
		parts = append(parts, "missing from openapi doc: "+strings.Join(missingInDoc, ", "))
	}
	if len(missingInRegistry) > 0 {
		parts = append(parts, "missing from registry: "+strings.Join(missingInRegistry, ", "))
	}
	return fmt.Errorf("openapi/registry mismatch: %s", strings.Join(parts, "; "))
}

func docRouteSet(doc Document) map[string]bool {
	out := map[string]bool{}
	for path, item := range doc.Paths {
		for _, m := range pathItemMethods(item) {
			out[m+" "+path] = true
		}
	}
	return out
}

func registryRouteSet(reg *endpoints.Registry) map[string]bool {
	out := map[string]bool{}
	for _, r := range reg.All() {
		out[strings.ToUpper(r.Method)+" "+r.Path] = true
	}
	return out
}

func pathItemMethods(p PathItem) []string {
	var out []string
	if p.Get != nil {
		out = append(out, "GET")
	}
	if p.Post != nil {
		out = append(out, "POST")
	}
	if p.Put != nil {
		out = append(out, "PUT")
	}
	if p.Patch != nil {
		out = append(out, "PATCH")
	}
	if p.Delete != nil {
		out = append(out, "DELETE")
	}
	if p.Head != nil {
		out = append(out, "HEAD")
	}
	if p.Options != nil {
		out = append(out, "OPTIONS")
	}
	return out
}
