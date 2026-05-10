// Package identity contains test endpoints that surface inbound auth identity
// and HTTP headers. The bread-and-butter of verifying an API gateway's
// pass-through behavior.
//
// Generic request echo lives in pkg/endpoints/echo so this package can stay
// focused on identity/header inspection.
package identity

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/auth/inbound"
	"github.com/plexara/api-test/pkg/endpoints"
)

const groupName = "identity"

// Group implements endpoints.Endpoints for the identity group.
type Group struct {
	redactHeaders []string
}

// New returns a Group. redactHeaders names headers whose values should be
// replaced with "[redacted]" by the headers endpoint.
func New(redactHeaders []string) *Group {
	rh := make([]string, 0, len(redactHeaders))
	for _, h := range redactHeaders {
		rh = append(rh, strings.ToLower(h))
	}
	return &Group{redactHeaders: rh}
}

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "whoami",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/whoami",
			Description:  "Return the resolved inbound auth identity (mode, key id, subject, scopes).",
			AuthRequired: false,
			ResponseBody: (*WhoamiResponse)(nil),
		},
		{
			Name:         "headers",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/headers",
			Description:  "Return inbound HTTP headers, with sensitive values redacted.",
			AuthRequired: false,
			ResponseBody: (*HeadersResponse)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/whoami", mw(http.HandlerFunc(g.whoami)))
	mux.Handle("GET /v1/headers", mw(http.HandlerFunc(g.headers)))
}

// WhoamiResponse is the wire shape of GET /v1/whoami.
//
// Reads the resolved inbound.Identity off the request context (set by
// the inbound auth middleware in pkg/httpmw). When no identity is
// present (e.g. tests bypassing the middleware) it reports anonymous.
type WhoamiResponse struct {
	Subject  string         `json:"subject"`
	Email    string         `json:"email,omitempty"`
	AuthType string         `json:"auth_type"`
	KeyName  string         `json:"key_name,omitempty"`
	Scopes   []string       `json:"scopes,omitempty"`
	Claims   map[string]any `json:"claims,omitempty"`
}

func (g *Group) whoami(w http.ResponseWriter, r *http.Request) {
	id := inbound.FromContext(r.Context())
	if id == nil {
		writeJSON(w, http.StatusOK, WhoamiResponse{AuthType: "anonymous"})
		return
	}
	writeJSON(w, http.StatusOK, WhoamiResponse{
		Subject:  id.Subject,
		Email:    id.Email,
		AuthType: id.AuthType,
		KeyName:  id.KeyName,
		Scopes:   id.Scopes,
		Claims:   id.Claims,
	})
}

// HeadersResponse is the wire shape of GET /v1/headers.
type HeadersResponse struct {
	Headers map[string][]string `json:"headers"`
	Count   int                 `json:"count"`
}

func (g *Group) headers(w http.ResponseWriter, r *http.Request) {
	out := make(map[string][]string, len(r.Header))
	for k, vs := range r.Header {
		if g.shouldRedact(strings.ToLower(k)) {
			out[k] = []string{"[redacted]"}
			continue
		}
		out[k] = append([]string{}, vs...)
	}
	writeJSON(w, http.StatusOK, HeadersResponse{Headers: out, Count: len(out)})
}

func (g *Group) shouldRedact(headerLower string) bool {
	for _, r := range g.redactHeaders {
		if strings.Contains(headerLower, r) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
