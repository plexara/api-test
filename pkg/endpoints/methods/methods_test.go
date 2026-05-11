package methods

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	New().Mount(mux, endpoints.PassthroughMiddleware)
	return mux
}

func TestEveryMethodEchoes(t *testing.T) {
	mux := newMux(t)
	for _, m := range supportedMethods {
		t.Run(m, func(t *testing.T) {
			req := httptest.NewRequest(m, "/v1/method/echo", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d", w.Code)
			}
			if m == http.MethodHead {
				if w.Body.Len() != 0 {
					t.Errorf("HEAD should have empty body, got %q", w.Body.String())
				}
				return
			}
			var resp Response
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v (body=%q)", err, w.Body.String())
			}
			if resp.Method != m {
				t.Errorf("body method = %q, want %q", resp.Method, m)
			}
			if resp.Path != "/v1/method/echo" {
				t.Errorf("body path = %q", resp.Path)
			}
		})
	}
}

func TestOptionsAdvertisesAllow(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodOptions, "/v1/method/echo", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	allow := w.Header().Get("Allow")
	for _, m := range supportedMethods {
		if !strings.Contains(allow, m) {
			t.Errorf("Allow header missing %q (have %q)", m, allow)
		}
	}
}

func TestQueryEchoed(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/method/echo?a=1&a=2&b=x", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if got := resp.Query["a"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("query a wrong: %v", got)
	}
	if resp.Query["b"][0] != "x" {
		t.Errorf("query b wrong: %v", resp.Query["b"])
	}
}

func TestUnsupportedMethodReturns405(t *testing.T) {
	mux := newMux(t)
	// CONNECT and TRACE are not in supportedMethods; Go's mux returns
	// 405 Method Not Allowed when patterns for the path exist for
	// other verbs.
	req := httptest.NewRequest(http.MethodConnect, "/v1/method/echo", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("CONNECT status = %d, want 405", w.Code)
	}
}

func TestRoutes_OnePerMethod(t *testing.T) {
	routes := New().Routes()
	if len(routes) != len(supportedMethods) {
		t.Fatalf("got %d routes, want %d (one per verb)", len(routes), len(supportedMethods))
	}
	seen := map[string]bool{}
	for _, r := range routes {
		if r.Group != groupName {
			t.Errorf("%s group = %q", r.Method, r.Group)
		}
		if r.Path != "/v1/method/echo" {
			t.Errorf("%s path = %q", r.Method, r.Path)
		}
		if seen[r.Method] {
			t.Errorf("duplicate route for %s", r.Method)
		}
		seen[r.Method] = true
	}
}
