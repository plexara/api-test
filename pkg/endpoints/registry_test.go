package endpoints

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubGroup struct {
	name   string
	routes []EndpointMeta
}

func (g *stubGroup) Name() string           { return g.name }
func (g *stubGroup) Routes() []EndpointMeta { return g.routes }
func (g *stubGroup) Mount(mux *http.ServeMux, mw Middleware) {
	for _, r := range g.routes {
		mux.Handle(r.Method+" "+r.Path, mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(r.Name))
		})))
	}
}

func TestRegistry_AddIndexAndAll(t *testing.T) {
	g := &stubGroup{
		name: "g1",
		routes: []EndpointMeta{
			{Name: "a", Method: "GET", Path: "/a"},
			{Name: "b", Method: "POST", Path: "/b"},
		},
	}
	r := NewRegistry()
	r.Add(g)

	if group, name := r.RouteForRequest("GET", "/a"); group != "g1" || name != "a" {
		t.Errorf("RouteForRequest(GET /a) = %q,%q, want g1,a", group, name)
	}
	if group, _ := r.RouteForRequest("DELETE", "/a"); group != "" {
		t.Errorf("RouteForRequest(DELETE /a) = %q, want \"\"", group)
	}
	if all := r.All(); len(all) != 2 {
		t.Errorf("All() = %d, want 2", len(all))
	}
	if groups := r.Groups(); len(groups) != 1 {
		t.Errorf("Groups() = %d, want 1", len(groups))
	}
}

func TestRegistry_RouteForRequest_PathParams(t *testing.T) {
	r := NewRegistry()
	r.Add(&stubGroup{
		name: "data",
		routes: []EndpointMeta{
			{Name: "fixed", Method: "GET", Path: "/v1/fixed/{key}"},
			{Name: "sized", Method: "GET", Path: "/v1/sized"},
		},
	})
	r.Add(&stubGroup{
		name: "failure",
		routes: []EndpointMeta{
			{Name: "status", Method: "GET", Path: "/v1/status/{code}"},
		},
	})

	cases := []struct {
		method, path string
		wantGroup    string
		wantName     string
	}{
		{"GET", "/v1/fixed/abc", "data", "fixed"},
		{"GET", "/v1/fixed/anything-here", "data", "fixed"},
		{"GET", "/v1/status/503", "failure", "status"},
		{"GET", "/v1/status/abc", "failure", "status"}, // path matches; handler validates
		{"GET", "/v1/sized", "data", "sized"},          // literal still works
		{"GET", "/v1/fixed", "", ""},                   // missing the {key} segment
		{"GET", "/v1/fixed/abc/extra", "", ""},         // extra segment
		{"POST", "/v1/fixed/abc", "", ""},              // wrong method
		{"GET", "/no-such-thing", "", ""},
	}
	for _, tc := range cases {
		gotGroup, gotName := r.RouteForRequest(tc.method, tc.path)
		if gotGroup != tc.wantGroup || gotName != tc.wantName {
			t.Errorf("RouteForRequest(%s %s) = (%q,%q), want (%q,%q)",
				tc.method, tc.path, gotGroup, gotName, tc.wantGroup, tc.wantName)
		}
	}
}

func TestRegistry_MountWrapsWithMiddleware(t *testing.T) {
	calls := 0
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			next.ServeHTTP(w, r)
		})
	}
	g := &stubGroup{
		name: "g",
		routes: []EndpointMeta{
			{Name: "x", Method: "GET", Path: "/x"},
		},
	}
	r := NewRegistry()
	r.Add(g)

	mux := http.NewServeMux()
	r.Mount(mux, mw)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if calls != 1 {
		t.Errorf("middleware not invoked: calls=%d", calls)
	}
}

func TestPassthroughMiddleware(t *testing.T) {
	called := false
	h := PassthroughMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Error("passthrough middleware did not invoke next")
	}
}
