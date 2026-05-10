package echo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

func newTestMux(t *testing.T, redact []string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	New(redact).Mount(mux, endpoints.PassthroughMiddleware)
	return mux
}

func TestEcho_GETRoundtrip(t *testing.T) {
	mux := newTestMux(t, []string{"authorization"})
	req := httptest.NewRequest(http.MethodGet, "/v1/echo?foo=1&foo=2&bar=baz", nil)
	req.Header.Set("X-Custom", "v1")
	req.Header.Set("Authorization", "Bearer keep-this-private")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Method != http.MethodGet {
		t.Errorf("method = %q", resp.Method)
	}
	if v, ok := resp.Query["foo"]; !ok || len(v) != 2 || v[0] != "1" || v[1] != "2" {
		t.Errorf("query foo = %v", resp.Query["foo"])
	}
	if got := resp.Headers["X-Custom"]; len(got) != 1 || got[0] != "v1" {
		t.Errorf("X-Custom = %v", got)
	}
	if got := resp.Headers["Authorization"]; len(got) != 1 || got[0] != "[redacted]" {
		t.Errorf("Authorization not redacted: %v", got)
	}
	if strings.Contains(w.Body.String(), "keep-this-private") {
		t.Error("response leaks Authorization value")
	}
}

func TestEcho_POSTBodyJSON(t *testing.T) {
	mux := newTestMux(t, nil)
	body := strings.NewReader(`{"a":1,"b":[2,3]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/echo", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Method != http.MethodPost {
		t.Errorf("method = %q", resp.Method)
	}
	if resp.BodySize == 0 {
		t.Errorf("body_size = 0")
	}
	m, ok := resp.Body.(map[string]any)
	if !ok {
		t.Fatalf("body not parsed as object: %T", resp.Body)
	}
	if m["a"] == nil {
		t.Errorf("body missing a: %v", m)
	}
}

func TestEcho_POSTBodyRawText(t *testing.T) {
	mux := newTestMux(t, nil)
	body := strings.NewReader(`not-json: bytes ; here`)
	req := httptest.NewRequest(http.MethodPost, "/v1/echo", body)
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.BodyRawText == "" {
		t.Error("expected raw text fallback to be populated")
	}
	if resp.Body != nil {
		t.Errorf("body should not parse as JSON, got %v", resp.Body)
	}
}

func TestEcho_HEADHasNoBody(t *testing.T) {
	mux := newTestMux(t, nil)
	req := httptest.NewRequest(http.MethodHead, "/v1/echo", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("HEAD response carried body of length %d", w.Body.Len())
	}
}

func TestEcho_AllMethodsRouted(t *testing.T) {
	mux := newTestMux(t, nil)
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"} {
		req := httptest.NewRequest(m, "/v1/echo", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", m, w.Code)
		}
	}
}
