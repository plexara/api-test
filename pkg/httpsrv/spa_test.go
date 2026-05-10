package httpsrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newTestSPA() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<html>spa</html>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
	}
}

func TestSPAHandler_RootServesIndex(t *testing.T) {
	h := SPAHandler(newTestSPA())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("root: status=%d body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want text/html…", got)
	}
}

func TestSPAHandler_ExplicitIndexPath(t *testing.T) {
	h := SPAHandler(newTestSPA())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("index.html: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestSPAHandler_ServesRealAsset(t *testing.T) {
	h := SPAHandler(newTestSPA())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "console.log") {
		t.Errorf("asset: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestSPAHandler_FallsBackToIndexForClientRoute(t *testing.T) {
	h := SPAHandler(newTestSPA())
	for _, path := range []string{"/dashboard", "/audit/123", "/keys"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
				t.Errorf("client route %q: status=%d body=%q", path, w.Code, w.Body.String())
			}
		})
	}
}

func TestSPAHandler_404sMissingAsset(t *testing.T) {
	h := SPAHandler(newTestSPA())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("missing asset: status=%d, want 404", w.Code)
	}
}

func TestIsClientRoute(t *testing.T) {
	cases := map[string]bool{
		"dashboard":           true,
		"audit/some-id":       true,
		"keys/":               true, // trailing slash splits to empty -> "" has no dot -> client route
		"assets/app.js":       false,
		"favicon.ico":         false,
		"plexara-mark.svg":    false,
		"some/path/file.html": false,
	}
	for in, want := range cases {
		if got := isClientRoute(in); got != want {
			t.Errorf("isClientRoute(%q) = %v, want %v", in, got, want)
		}
	}
}
