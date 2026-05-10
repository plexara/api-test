package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

type stubGroup struct{}

func (stubGroup) Name() string { return "stub" }
func (stubGroup) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{{Name: "ping", Method: "GET", Path: "/v1/ping"}}
}
func (stubGroup) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/ping", mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})))
}

func TestBuildMux_RootBanner(t *testing.T) {
	r := endpoints.NewRegistry()
	r.Add(stubGroup{})
	mux := BuildMux(r, NewReadiness(), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "api-test" {
		t.Errorf("name = %v", body["name"])
	}
}

func TestBuildMux_Healthz(t *testing.T) {
	mux := BuildMux(endpoints.NewRegistry(), NewReadiness(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
}

func TestBuildMux_GroupMounted(t *testing.T) {
	r := endpoints.NewRegistry()
	r.Add(stubGroup{})
	mux := BuildMux(r, NewReadiness(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "pong" {
		t.Errorf("group not reachable: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestBuildMux_404(t *testing.T) {
	mux := BuildMux(endpoints.NewRegistry(), NewReadiness(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/no-such-thing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d want 404", w.Code)
	}
}
