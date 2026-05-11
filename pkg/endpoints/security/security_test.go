package security

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

func TestAdmin_AlwaysServes(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/security/admin/secret", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp AdminResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Message == "" {
		t.Errorf("admin should return a non-empty message body")
	}
}

func TestFetch_DoesNotActuallyFetch(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/v1/security/fetch?url=http://169.254.169.254/latest/meta-data/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp FetchResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.WouldHaveFetched {
		t.Errorf("fetch probe must always report would_have_fetched=false; got true")
	}
	if !strings.Contains(resp.AskedFor, "169.254.169.254") {
		t.Errorf("asked_for should echo URL; got %q", resp.AskedFor)
	}
}

func TestFetch_MissingURL(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/security/fetch", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestBigHeaders_EmitsManyHeaders(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/security/big-headers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	probeCount := 0
	for k := range w.Header() {
		if strings.HasPrefix(k, "X-Big-Probe-") {
			probeCount++
		}
	}
	if probeCount != bigHeaderCount {
		t.Errorf("got %d probe headers, want %d", probeCount, bigHeaderCount)
	}
}

func TestRedirectTo_Inert(t *testing.T) {
	mux := newMux(t)
	target := "https://evil.example/landing"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
		"/v1/security/redirect-to?url="+target, nil))

	// The probe is inert by design: status 200 (browsers don't auto-
	// follow) and a custom header X-Would-Redirect-To (CodeQL's
	// go/unvalidated-url-redirection rule does not trace through
	// non-Location headers). NEITHER a 3xx status NOR a Location
	// header is allowed.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (must be inert)", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("Location header set to %q — probe must NOT set Location", loc)
	}
	if got := w.Header().Get("X-Would-Redirect-To"); got != target {
		t.Errorf("X-Would-Redirect-To = %q, want %q", got, target)
	}
	var resp RedirectResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.WouldRedirectTo != target {
		t.Errorf("body WouldRedirectTo = %q, want %q", resp.WouldRedirectTo, target)
	}
}

func TestRedirectTo_MissingURL(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/security/redirect-to", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestControlChars_DetectsControlBytes(t *testing.T) {
	mux := newMux(t)
	cases := []struct {
		query       string
		wantControl bool
	}{
		{"plain", false},
		{"with%00nul", true},
		{"with%0Anewline", true},
		{"with%7Fdel", true},
		{"ascii-only", false},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
				"/v1/security/control-chars?q="+c.query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d", w.Code)
			}
			var resp ControlCharsResponse
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.HasControl != c.wantControl {
				t.Errorf("HasControl = %v, want %v (q=%q)", resp.HasControl, c.wantControl, resp.Q)
			}
			if resp.ByteCount != len(resp.Q) {
				t.Errorf("ByteCount %d != len(Q) %d", resp.ByteCount, len(resp.Q))
			}
		})
	}
}

func TestContainsControl_KnownBytes(t *testing.T) {
	cases := map[string]bool{
		"":                 false,
		"plain ascii":      false,
		"\x00":             true,
		"end\x1ftext":      true,
		"newline\nhere":    true,
		"tab\there":        true,
		"\x7f":             true,
		"emoji \U0001F600": false,
	}
	for in, want := range cases {
		if got := containsControl(in); got != want {
			t.Errorf("containsControl(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRoutes_Shape(t *testing.T) {
	routes := New().Routes()
	if len(routes) != 5 {
		t.Fatalf("got %d routes, want 5", len(routes))
	}
	for _, r := range routes {
		if r.Group != groupName {
			t.Errorf("%s group = %q", r.Path, r.Group)
		}
		if r.ResponseBody == nil {
			t.Errorf("%s missing ResponseBody", r.Path)
		}
	}
}
