package httpsrv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireCSRFHeader_PassesGet(t *testing.T) {
	called := false
	h := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/portal/api/things", nil))
	if !called {
		t.Error("GET should pass through")
	}
	if w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200", w.Code)
	}
}

func TestRequireCSRFHeader_BlocksPostWithoutHeader(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		called := false
		h := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(m, "/x", nil))
		if called {
			t.Errorf("%s without X-Requested-With: handler should not be reached", m)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", m, w.Code)
		}
	}
}

func TestRequireCSRFHeader_AllowsPostWithHeader(t *testing.T) {
	called := false
	h := requireCSRFHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called {
		t.Error("POST with X-Requested-With: handler should run")
	}
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
}
