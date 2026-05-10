package httpsrv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	w := httptest.NewRecorder()
	HealthzHandler()(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("body %q", w.Body.String())
	}
}

func TestReadyz_Toggle(t *testing.T) {
	r := NewReadiness()

	w := httptest.NewRecorder()
	r.ReadyzHandler()(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK {
		t.Errorf("ready status %d", w.Code)
	}

	r.SetReady(false)
	w = httptest.NewRecorder()
	r.ReadyzHandler()(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("draining status %d", w.Code)
	}
}

func TestCORS_Preflight(t *testing.T) {
	called := false
	h := CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status %d", w.Code)
	}
	if called {
		t.Error("preflight should short-circuit")
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS Allow-Origin missing")
	}
}

func TestCORS_PassThrough(t *testing.T) {
	called := false
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("non-preflight should call next")
	}
	if w.Code != http.StatusTeapot {
		t.Errorf("status %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Allow-Origin missing on non-preflight")
	}
}
