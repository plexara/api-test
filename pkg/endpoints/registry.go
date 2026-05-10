// Package endpoints defines the Endpoints interface and shared metadata used
// by the portal to render endpoint catalogs and Try-It forms, and by the
// in-tree OpenAPI generator (pkg/oapi) to build the served spec.
package endpoints

import (
	"net/http"
	"strings"
)

// Endpoints is the contract every group of test endpoints implements.
//
// Mount mounts the group's HTTP routes onto the given mux. The Middleware
// argument lets the composition layer wrap each handler with audit/identity
// middleware without each group having to know about it.
type Endpoints interface {
	Name() string
	Routes() []EndpointMeta
	Mount(mux *http.ServeMux, mw Middleware)
}

// Middleware wraps an http.Handler. The composition layer supplies the audit
// and identity middleware here so every endpoint group records consistently.
//
// Groups call mw to wrap their handlers before mux.Handle, e.g.:
//
//	mux.Handle("GET /v1/whoami", mw(http.HandlerFunc(whoami)))
type Middleware func(http.Handler) http.Handler

// PassthroughMiddleware is the no-op identity middleware. Used by tests and
// during M1 when audit/identity middleware isn't wired yet.
var PassthroughMiddleware Middleware = func(h http.Handler) http.Handler { return h }

// EndpointMeta is the portal- and OpenAPI-friendly description of one route.
//
// PathParams, QueryParams, RequestBody, ResponseBody are nil-able example
// shapes used by the OpenAPI generator's reflection step. Groups should set
// the matching field with a typed zero value (e.g. (*sizedInput)(nil)) so
// reflection picks up the field tags without instantiating data.
type EndpointMeta struct {
	Name         string `json:"name"`
	Group        string `json:"group"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Description  string `json:"description"`
	AuthRequired bool   `json:"auth_required"`
	PathParams   any    `json:"-"`
	QueryParams  any    `json:"-"`
	RequestBody  any    `json:"-"`
	ResponseBody any    `json:"-"`
}

// Registry collects endpoint groups for portal listing, OpenAPI generation,
// and audit dispatch (route + group resolution from a live request).
type Registry struct {
	groups []Endpoints
	// flat is the materialized (group, route) pairs in registration
	// order. The audit middleware walks it to resolve a request's
	// matched template, since path-parameterized routes like
	// /v1/fixed/{key} don't equal /v1/fixed/abc under literal lookup.
	flat []routeEntry
}

type routeEntry struct {
	group string
	meta  EndpointMeta
	segs  []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Add appends a group and indexes its routes for pattern matching.
func (r *Registry) Add(g Endpoints) {
	r.groups = append(r.groups, g)
	for _, route := range g.Routes() {
		r.flat = append(r.flat, routeEntry{
			group: g.Name(),
			meta:  route,
			segs:  splitPathSegments(route.Path),
		})
	}
}

// Groups returns the registered groups in registration order.
func (r *Registry) Groups() []Endpoints { return r.groups }

// RouteForRequest resolves a live (method, requestPath) to the matched
// route's (group, name). Returns ("", "") when no registered route
// matches. Used by the audit middleware to populate endpoint_group and
// route_name on the event row.
//
// Matching mirrors Go 1.22+ http.ServeMux pattern semantics for the
// shapes this project actually uses: literal segments must match
// exactly, {name} segments match any single segment, and the path's
// segment count must match. Cross-segment wildcards ({name...}) are not
// used by api-test routes today and are not supported here; if a future
// route needs them, fall through to the mux's own resolver.
func (r *Registry) RouteForRequest(method, requestPath string) (group, name string) {
	reqSegs := splitPathSegments(requestPath)
	for _, e := range r.flat {
		if e.meta.Method != method {
			continue
		}
		if matchSegments(e.segs, reqSegs) {
			return e.group, e.meta.Name
		}
	}
	return "", ""
}

// All returns a flat list of every route's metadata across all groups.
func (r *Registry) All() []EndpointMeta {
	var out []EndpointMeta
	for _, g := range r.groups {
		out = append(out, g.Routes()...)
	}
	return out
}

// Mount mounts every group's routes onto mux, wrapped with mw.
func (r *Registry) Mount(mux *http.ServeMux, mw Middleware) {
	for _, g := range r.groups {
		g.Mount(mux, mw)
	}
}

// splitPathSegments returns the path's "/"-separated segments with empty
// segments dropped. "/v1/fixed/{key}" → ["v1", "fixed", "{key}"].
func splitPathSegments(p string) []string {
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := parts[:0]
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// matchSegments reports whether requestSegs satisfies the patternSegs
// from a registered route. {name}-style pattern segments match any
// single literal segment.
func matchSegments(patternSegs, requestSegs []string) bool {
	if len(patternSegs) != len(requestSegs) {
		return false
	}
	for i, p := range patternSegs {
		if len(p) >= 2 && p[0] == '{' && p[len(p)-1] == '}' {
			continue
		}
		if p != requestSegs[i] {
			return false
		}
	}
	return true
}
