// Try-It dispatch handler. Takes an operator-constructed request from
// the portal (method + path-params + query + headers + body), routes
// it into the local mux through PortalAPI.replayTarget, and returns
// the captured response. Distinct from audit replay (which reconstructs
// a request from an audit_payloads row); this is "operator authors a
// fresh request from the endpoint catalog UI."

package httpsrv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

const (
	// tryItHeaderMarker is set on every dispatched request so the audit
	// middleware (and any future loop guard) can tell a Try-It dispatch
	// apart from external traffic. The value is "true" — the operator's
	// identity is already on the audit row via the inbound auth chain.
	tryItHeaderMarker = "X-Plexara-Try-It"

	// tryItMaxBodyBytes caps both the inbound request body we'll send
	// and the response body we'll capture. Matches the replay cap; both
	// surfaces share the same "operator-authored request through the
	// portal" envelope, so the same limit applies.
	tryItMaxBodyBytes = 1 << 20 // 1 MiB
)

// TryItRequest is the JSON body the SPA sends.
type TryItRequest struct {
	// Method overrides the registered route's Method. Most routes
	// accept exactly one method; this is mostly here for the echo
	// group (registered once per supported verb).
	Method string `json:"method,omitempty"`

	// PathParams maps each "{name}" placeholder in the route's Path
	// to its substitution. Missing params → 400.
	PathParams map[string]string `json:"path_params,omitempty"`

	// QueryParams maps query-parameter names to values. Each value
	// can repeat.
	QueryParams map[string][]string `json:"query_params,omitempty"`

	// Headers maps header names to values. Cookie and authorization
	// headers are silently dropped; operators authenticate at the
	// portal level, not by injecting credentials into the dispatched
	// request.
	Headers map[string][]string `json:"headers,omitempty"`

	// Body is the raw request body (already serialized by the SPA).
	// Empty for GET-style routes.
	Body string `json:"body,omitempty"`
}

// TryItResponse is the JSON envelope returned to the SPA.
type TryItResponse struct {
	DispatchedTo  string              `json:"dispatched_to"`
	Method        string              `json:"method"`
	Status        int                 `json:"status"`
	Headers       map[string][]string `json:"headers"`
	Body          string              `json:"body"`
	BodyTruncated bool                `json:"body_truncated"`
}

// pathParamRe matches "{name}" placeholders in route templates.
var pathParamRe = regexp.MustCompile(`\{([^}/]+)\}`)

// disallowedTryItHeaders are dropped from the operator-supplied
// header map before dispatch. The portal session already authenticates
// the operator; injecting Cookie/Authorization into the dispatched
// request would let a low-privilege operator escalate via /v1/* auth
// surfaces. The X-API-Key path is left allowed because it's the
// documented test-fixture credential channel.
var disallowedTryItHeaders = map[string]struct{}{
	"cookie":        {},
	"authorization": {},
	"set-cookie":    {},
}

// tryIt dispatches an operator-constructed request through the local
// mux and returns the captured response.
func (p *PortalAPI) tryIt(w http.ResponseWriter, r *http.Request) {
	if p.replayTarget == nil {
		writeError(w, http.StatusServiceUnavailable,
			errors.New("try-it disabled: composition did not supply a dispatch target"))
		return
	}

	group := r.PathValue("group")
	routeName := r.PathValue("route")

	meta, ok := p.findRoute(group, routeName)
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Errorf("endpoint %s/%s not registered", group, routeName))
		return
	}

	var req TryItRequest
	if r.ContentLength > 0 || r.Header.Get("Content-Type") != "" {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tryItMaxBodyBytes)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("decode request body: %w", err))
			return
		}
	}

	method := req.Method
	if method == "" {
		method = meta.Method
	}

	path, err := substitutePathParams(meta.Path, req.PathParams)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	u, err := url.Parse(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("parse path: %w", err))
		return
	}
	if len(req.QueryParams) > 0 {
		values := url.Values{}
		for k, vs := range req.QueryParams {
			for _, v := range vs {
				values.Add(k, v)
			}
		}
		u.RawQuery = values.Encode()
	}

	body := []byte(req.Body)
	if len(body) > tryItMaxBodyBytes {
		body = body[:tryItMaxBodyBytes]
	}
	dispatched, err := http.NewRequestWithContext(r.Context(),
		method, u.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("construct request: %w", err))
		return
	}

	for k, vs := range req.Headers {
		if _, banned := disallowedTryItHeaders[strings.ToLower(k)]; banned {
			continue
		}
		for _, v := range vs {
			dispatched.Header.Add(k, v)
		}
	}
	dispatched.Header.Set(tryItHeaderMarker, "true")

	rec := newCapResponseWriter(tryItMaxBodyBytes)
	p.replayTarget.ServeHTTP(rec, dispatched)

	writeJSON(w, http.StatusOK, TryItResponse{
		DispatchedTo:  u.String(),
		Method:        method,
		Status:        rec.status,
		Headers:       rec.headers,
		Body:          rec.body.String(),
		BodyTruncated: rec.truncated,
	})
}

// findRoute locates the EndpointMeta whose Group and Name match. Both
// match exactly; the operator is expected to copy these from the
// portal's endpoints catalog (/api/v1/portal/endpoints), not type them.
func (p *PortalAPI) findRoute(group, routeName string) (endpoints.EndpointMeta, bool) {
	if p.registry == nil {
		return endpoints.EndpointMeta{}, false
	}
	for _, m := range p.registry.All() {
		if m.Group == group && m.Name == routeName {
			return m, true
		}
	}
	return endpoints.EndpointMeta{}, false
}

// substitutePathParams replaces each "{name}" in template with
// params[name]. Returns an error if a placeholder has no value, if a
// value would change the segment count (contains "/"), or if a value
// is empty.
func substitutePathParams(template string, params map[string]string) (string, error) {
	missing := []string{}
	out := pathParamRe.ReplaceAllStringFunc(template, func(match string) string {
		name := match[1 : len(match)-1]
		v, ok := params[name]
		if !ok || v == "" {
			missing = append(missing, name)
			return match
		}
		if strings.Contains(v, "/") {
			missing = append(missing, name+" (contains '/')")
			return match
		}
		return url.PathEscape(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("missing or invalid path params: %s",
			strings.Join(missing, ", "))
	}
	return out, nil
}

// capResponseWriter is defined in portal_audit_replay.go and reused
// here. Both the Try-It dispatch and audit replay paths need bounded
// response buffering with the same semantics.
