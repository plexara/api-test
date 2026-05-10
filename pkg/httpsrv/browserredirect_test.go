package httpsrv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserRedirect_BouncesHTMLGetAtRoot(t *testing.T) {
	pass := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := BrowserRedirect("/portal/", pass)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/portal/" {
		t.Errorf("Location = %q, want /portal/", loc)
	}
}

func TestBrowserRedirect_DefaultPathFallback(t *testing.T) {
	h := BrowserRedirect("", http.NotFoundHandler())
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if loc := w.Header().Get("Location"); loc != "/portal/" {
		t.Errorf("Location with empty portalPath = %q, want /portal/ default", loc)
	}
}

func TestBrowserRedirect_NonBrowserPassesThrough(t *testing.T) {
	cases := []struct {
		name, accept, method, path string
	}{
		{"curl-no-accept", "", http.MethodGet, "/"},
		{"json-accept", "application/json", http.MethodGet, "/"},
		{"non-root-html", "text/html", http.MethodGet, "/v1/foo"},
		{"post-html", "text/html", http.MethodPost, "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			passed := false
			h := BrowserRedirect("/portal/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				passed = true
				w.WriteHeader(http.StatusOK)
			}))
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.accept != "" {
				r.Header.Set("Accept", tc.accept)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if !passed {
				t.Errorf("expected pass-through for %+v, got status %d", tc, w.Code)
			}
		})
	}
}
