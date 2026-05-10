package failure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/endpoints"
)

func newTestMux(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	New().Mount(mux, endpoints.PassthroughMiddleware)
	return mux
}

func TestStatus_Passthrough(t *testing.T) {
	mux := newTestMux(t)
	for _, code := range []int{200, 204, 301, 400, 404, 418, 500, 503} {
		req := httptest.NewRequest(http.MethodGet, "/v1/status/"+itoa(code), nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != code {
			t.Errorf("status %d: got %d", code, w.Code)
		}
	}
}

func TestStatus_Invalid(t *testing.T) {
	mux := newTestMux(t)
	for _, c := range []string{"abc", "99", "600", "-1"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/status/"+c, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d want 400", c, w.Code)
		}
	}
}

func TestSlow_HonorsContextCancel(t *testing.T) {
	mux := newTestMux(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/slow?ms=5000", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("slow handler did not return after cancel")
	}
	// Should be the 499 client-cancellation response.
	if w.Code != 499 {
		t.Errorf("status %d want 499", w.Code)
	}
}

func TestSlow_FastPath(t *testing.T) {
	mux := newTestMux(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/slow?ms=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp SlowResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RequestedM != 1 {
		t.Errorf("requested_ms = %d want 1", resp.RequestedM)
	}
}

func TestSlow_RejectsOverMax(t *testing.T) {
	mux := newTestMux(t)
	// At the cap exactly: succeeds (but we shorten the actual sleep via
	// a cancel so the test doesn't wait 60s; the assertion is on the
	// status, which gets written only after the sleep completes or
	// gets cancelled).
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/slow?ms=60000", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { mux.ServeHTTP(w, req); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if w.Code != 499 {
		t.Errorf("at-cap with cancel: status %d want 499", w.Code)
	}

	// One over the cap: 400 immediately, no sleep.
	req = httptest.NewRequest(http.MethodGet, "/v1/slow?ms=60001", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("ms=60001 status %d want 400", w.Code)
	}

	// Way over the cap: 400 immediately.
	req = httptest.NewRequest(http.MethodGet, "/v1/slow?ms=86400000", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("ms=86400000 status %d want 400", w.Code)
	}
}

func TestFlaky_Reproducible(t *testing.T) {
	mux := newTestMux(t)
	a := doStatus(t, mux, "/v1/flaky?fail_rate=0.5&seed=abc&call_id=7")
	b := doStatus(t, mux, "/v1/flaky?fail_rate=0.5&seed=abc&call_id=7")
	if a != b {
		t.Errorf("flaky not reproducible: %d vs %d", a, b)
	}
}

func TestFlaky_RateBounds(t *testing.T) {
	mux := newTestMux(t)
	if c := doStatus(t, mux, "/v1/flaky?fail_rate=0&seed=x&call_id=1"); c != 200 {
		t.Errorf("rate=0 should pass: status %d", c)
	}
	if c := doStatus(t, mux, "/v1/flaky?fail_rate=1&seed=x&call_id=1"); c != 503 {
		t.Errorf("rate=1 should fail: status %d", c)
	}
}

func doStatus(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
