// Package echo provides a generic catch-all endpoint that returns the
// inbound request verbatim (with auth headers redacted). Useful for
// ad-hoc try-it use from the portal or a curl one-liner against a Plexara
// connection registered for api-test.
package echo

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

const groupName = "echo"

// Group implements endpoints.Endpoints for the echo group.
type Group struct {
	redactHeaders []string
}

// New returns a Group. redactHeaders names headers whose values should be
// replaced with "[redacted]" in the response.
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
//
// We register one entry per supported method so the OpenAPI generator and
// portal display them individually; the underlying handler is shared.
func (Group) Routes() []endpoints.EndpointMeta {
	methods := []string{
		http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodHead,
	}
	out := make([]endpoints.EndpointMeta, 0, len(methods))
	for _, m := range methods {
		out = append(out, endpoints.EndpointMeta{
			Name:         "echo_" + strings.ToLower(m),
			Group:        groupName,
			Method:       m,
			Path:         "/v1/echo",
			Description:  "Echo the request (method, path, query, headers, body) back. Sensitive headers redacted.",
			ResponseBody: (*Response)(nil),
		})
	}
	return out
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	for _, m := range []string{
		http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodHead,
	} {
		mux.Handle(m+" /v1/echo", mw(http.HandlerFunc(g.handle)))
	}
}

// Response is the wire shape of /v1/echo.
type Response struct {
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Query       map[string][]string `json:"query,omitempty"`
	Headers     map[string][]string `json:"headers"`
	Body        any                 `json:"body,omitempty"`
	BodyRawText string              `json:"body_raw_text,omitempty"`
	BodySize    int                 `json:"body_size"`
}

func (g *Group) handle(w http.ResponseWriter, r *http.Request) {
	resp := Response{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.Query(),
		Headers: make(map[string][]string, len(r.Header)),
	}
	for k, vs := range r.Header {
		if g.shouldRedact(strings.ToLower(k)) {
			resp.Headers[k] = []string{"[redacted]"}
			continue
		}
		resp.Headers[k] = append([]string{}, vs...)
	}

	if r.Body != nil && r.Method != http.MethodHead {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		resp.BodySize = len(raw)
		if len(raw) > 0 {
			var parsed any
			if err := json.Unmarshal(raw, &parsed); err == nil {
				resp.Body = parsed
			} else {
				resp.BodyRawText = string(raw)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (g *Group) shouldRedact(headerLower string) bool {
	for _, r := range g.redactHeaders {
		if strings.Contains(headerLower, r) {
			return true
		}
	}
	return false
}
