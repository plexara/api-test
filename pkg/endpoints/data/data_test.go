package data

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

func newTestMux(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	New().Mount(mux, endpoints.PassthroughMiddleware)
	return mux
}

func TestFixed_Deterministic(t *testing.T) {
	mux := newTestMux(t)
	a := doGet(t, mux, "/v1/fixed/hello")
	b := doGet(t, mux, "/v1/fixed/hello")
	if a != b {
		t.Errorf("fixed not deterministic:\n a=%s\n b=%s", a, b)
	}
	if c := doGet(t, mux, "/v1/fixed/world"); c == a {
		t.Error("different keys produced identical body")
	}
}

func TestSized_ExactBytes(t *testing.T) {
	mux := newTestMux(t)
	for _, n := range []int{0, 1, 26, 27, 1024, 65536} {
		body := doGet(t, mux, "/v1/sized?bytes="+itoa(n))
		var resp struct {
			Bytes int    `json:"bytes"`
			Body  string `json:"body"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode (n=%d): %v", n, err)
		}
		if resp.Bytes != n {
			t.Errorf("bytes field = %d want %d", resp.Bytes, n)
		}
		if len(resp.Body) != n {
			t.Errorf("body length (n=%d): got %d", n, len(resp.Body))
		}
		// Determinism check: alphabet repeats from index 0.
		if n > 0 && resp.Body[0] != 'a' {
			t.Errorf("body (n=%d) does not start with 'a': %q", n, resp.Body[:1])
		}
	}
}

func TestSized_RejectsBadInput(t *testing.T) {
	mux := newTestMux(t)
	for _, q := range []string{"bytes=abc", "bytes=-1", "bytes=999999999999"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/sized?"+q, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d want 400", q, w.Code)
		}
	}
}

func TestLorem_SeededReproducible(t *testing.T) {
	mux := newTestMux(t)
	a := doGet(t, mux, "/v1/lorem?words=20&seed=cat")
	b := doGet(t, mux, "/v1/lorem?words=20&seed=cat")
	if a != b {
		t.Errorf("lorem not deterministic for fixed seed:\n a=%s\n b=%s", a, b)
	}
	c := doGet(t, mux, "/v1/lorem?words=20&seed=dog")
	if c == a {
		t.Error("different seeds produced identical body")
	}
}

func TestLorem_DefaultsAndRejects(t *testing.T) {
	mux := newTestMux(t)

	// ?words=0 → defaults to 50 words and a body terminating in a period.
	body := doGet(t, mux, "/v1/lorem?words=0&seed=x")
	var resp LoremResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Words != 50 {
		t.Errorf("default words = %d want 50", resp.Words)
	}
	if !strings.HasSuffix(resp.Body, ".") {
		t.Error("lorem body missing trailing period")
	}

	// ?words=5000 (the cap exactly) → still succeeds.
	body = doGet(t, mux, "/v1/lorem?words=5000&seed=x")
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Words != 5000 {
		t.Errorf("at-cap words = %d want 5000", resp.Words)
	}

	// ?words=5001 (one over the cap) → 400 (validate-and-reject; CodeQL's
	// go/uncontrolled-allocation-size query recognizes early return as
	// a sanitizer but does NOT recognize a clamp).
	req := httptest.NewRequest(http.MethodGet, "/v1/lorem?words=5001&seed=x", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("words=5001 status %d want 400", w.Code)
	}

	// ?words=100000 (well over the cap) → 400 same as above.
	req = httptest.NewRequest(http.MethodGet, "/v1/lorem?words=100000&seed=x", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("words=100000 status %d want 400", w.Code)
	}
}

func doGet(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code/100 != 2 {
		t.Fatalf("%s: status %d body=%s", path, w.Code, w.Body.String())
	}
	return w.Body.String()
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
