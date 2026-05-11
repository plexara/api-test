package httpsrv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

// tryItRegistry returns a registry with one stub group whose route
// catalogs match the Try-It handler's lookup-by-(group, name) contract.
func tryItRegistry(t *testing.T) *endpoints.Registry {
	t.Helper()
	r := endpoints.NewRegistry()
	r.Add(tryItStubGroup{})
	return r
}

type tryItStubGroup struct{}

func (tryItStubGroup) Name() string { return "stub" }
func (tryItStubGroup) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{Name: "ping", Group: "stub", Method: http.MethodGet, Path: "/v1/ping"},
		{Name: "fixed", Group: "stub", Method: http.MethodGet, Path: "/v1/fixed/{key}"},
		{Name: "create", Group: "stub", Method: http.MethodPost, Path: "/v1/create"},
	}
}
func (tryItStubGroup) Mount(*http.ServeMux, endpoints.Middleware) {}

// tryItStubTarget returns a mux that echoes the dispatched request as
// JSON so tests can verify Try-It built the request correctly.
func tryItStubTarget(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	echo := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method":   r.Method,
			"path":     r.URL.Path,
			"query":    r.URL.Query(),
			"body":     string(body),
			"x_custom": r.Header.Get("X-Custom"),
			"x_tryit":  r.Header.Get(tryItHeaderMarker),
			"cookie":   r.Header.Get("Cookie"),
			"authz":    r.Header.Get("Authorization"),
		})
	}
	mux.HandleFunc("GET /v1/ping", echo)
	mux.HandleFunc("GET /v1/fixed/{key}", echo)
	mux.HandleFunc("POST /v1/create", echo)
	return mux
}

func TestTryIt_DispatchesGetWithQuery(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	body := mustJSON(t, TryItRequest{
		QueryParams: map[string][]string{
			"a": {"1", "2"},
			"b": {"x"},
		},
	})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/ping", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}

	var resp TryItResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", resp.Method)
	}
	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if !strings.Contains(resp.DispatchedTo, "a=1&a=2&b=x") {
		t.Errorf("dispatched_to should contain query: %q", resp.DispatchedTo)
	}

	var echoed map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &echoed)
	if echoed["method"] != "GET" || echoed["path"] != "/v1/ping" {
		t.Errorf("dispatched request shape wrong: %+v", echoed)
	}
	if echoed["x_tryit"] != "true" {
		t.Errorf("dispatched request missing Try-It marker header: %v", echoed["x_tryit"])
	}
}

func TestTryIt_SubstitutesPathParams(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	body := mustJSON(t, TryItRequest{PathParams: map[string]string{"key": "my key"}})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/fixed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}

	var resp TryItResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// URL escaping turns the space into %20.
	if !strings.Contains(resp.DispatchedTo, "/v1/fixed/my%20key") {
		t.Errorf("dispatched_to did not escape path param: %q", resp.DispatchedTo)
	}
}

func TestTryIt_MissingPathParam(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	body := mustJSON(t, TryItRequest{}) // no PathParams
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/fixed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestTryIt_PathParamWithSlash_Refused(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	body := mustJSON(t, TryItRequest{
		PathParams: map[string]string{"key": "a/b/c"},
	})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/fixed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 (slash in path param must be refused)", w.Code)
	}
}

func TestTryIt_SendsBody(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	payload := `{"hi":1}`
	body := mustJSON(t, TryItRequest{Body: payload})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}

	var resp TryItResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	var echoed map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &echoed)
	if echoed["body"] != payload {
		t.Errorf("dispatched body = %q, want %q", echoed["body"], payload)
	}
}

func TestTryIt_StripsCookieAndAuthorization(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	body := mustJSON(t, TryItRequest{
		Headers: map[string][]string{
			"Cookie":        {"session=abc"},
			"Authorization": {"Bearer leaked"},
			"X-Custom":      {"kept"},
		},
	})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/ping", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp TryItResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	var echoed map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &echoed)
	if echoed["cookie"] != "" {
		t.Errorf("Cookie should be stripped, got %q", echoed["cookie"])
	}
	if echoed["authz"] != "" {
		t.Errorf("Authorization should be stripped, got %q", echoed["authz"])
	}
	if echoed["x_custom"] != "kept" {
		t.Errorf("X-Custom should pass through, got %q", echoed["x_custom"])
	}
}

func TestTryIt_UnknownRoute_404(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	p.WithDispatchTarget(tryItStubTarget(t))
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/no-such-route", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestTryIt_DisabledWhenNoDispatchTarget(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	// Do NOT call WithDispatchTarget; replicate the route registration
	// directly so the auto-wire in Mount doesn't override.
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/portal/tryit/{group}/{route}",
		passthroughMW(requireCSRFHeader(http.HandlerFunc(p.tryIt))))

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/ping", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
}

func TestTryIt_CapsResponseBody(t *testing.T) {
	p := NewPortalAPI(nil, tryItRegistry(t), nil, nil)
	target := http.NewServeMux()
	target.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		chunk := bytes.Repeat([]byte("A"), 32*1024)
		for i := 0; i < 160; i++ { // ~5 MiB
			_, _ = w.Write(chunk)
		}
	})
	p.WithDispatchTarget(target)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	body := mustJSON(t, TryItRequest{})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/tryit/stub/ping", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp TryItResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.BodyTruncated {
		t.Errorf("body_truncated = false; expected true for >1 MiB response")
	}
	if len(resp.Body) > tryItMaxBodyBytes {
		t.Errorf("captured body is %d bytes; cap is %d", len(resp.Body), tryItMaxBodyBytes)
	}
}

func TestSubstitutePathParams(t *testing.T) {
	cases := []struct {
		template string
		params   map[string]string
		want     string
		wantErr  bool
	}{
		{"/v1/ping", nil, "/v1/ping", false},
		{"/v1/fixed/{key}", map[string]string{"key": "abc"}, "/v1/fixed/abc", false},
		{"/v1/fixed/{key}", map[string]string{"key": "a b"}, "/v1/fixed/a%20b", false},
		{"/v1/fixed/{key}", map[string]string{}, "", true},             // missing
		{"/v1/fixed/{key}", map[string]string{"key": ""}, "", true},    // empty
		{"/v1/fixed/{key}", map[string]string{"key": "a/b"}, "", true}, // slash
		{"/v1/{a}/{b}", map[string]string{"a": "x", "b": "y"}, "/v1/x/y", false},
	}
	for _, c := range cases {
		t.Run(c.template, func(t *testing.T) {
			got, err := substitutePathParams(c.template, c.params)
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
