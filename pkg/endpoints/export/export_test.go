package export

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/endpoints"
)

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	New().Mount(mux, endpoints.PassthroughMiddleware)
	return mux
}

func TestBigBody_RespectsSize(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/export/big-body?size_kb=8&seed=x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()
	if !strings.HasPrefix(string(body), "[") || !strings.HasSuffix(string(body), "]") {
		t.Errorf("body should be a JSON array; head=%q tail=%q",
			body[:10], body[len(body)-10:])
	}
	// Should be in the ballpark of 8 KiB. Allow generous slack; the
	// loop overshoots by one row at most.
	got := len(body)
	if got < 6*1024 || got > 12*1024 {
		t.Errorf("body length %d not in [6 KiB, 12 KiB]", got)
	}
	// Body should parse as JSON.
	var arr []BigBodyRow
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if len(arr) == 0 {
		t.Errorf("array empty")
	}
}

func TestBigBody_Deterministic(t *testing.T) {
	mux := newMux(t)
	a := getBody(t, mux, "/v1/export/big-body?size_kb=4&seed=fixed")
	b := getBody(t, mux, "/v1/export/big-body?size_kb=4&seed=fixed")
	if a != b {
		t.Errorf("same (size, seed) produced different bodies")
	}
	c := getBody(t, mux, "/v1/export/big-body?size_kb=4&seed=other")
	if a == c {
		t.Errorf("different seed produced same body")
	}
}

func TestBigBody_CapEnforced(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/export/big-body?size_kb=99999", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized request should be 400, got %d", w.Code)
	}
}

func TestCSV_HeaderAndRows(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/export/csv?rows=5&seed=x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := []string{}
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6 (header + 5 rows): %v", len(lines), lines)
	}
	if lines[0] != "index,value" {
		t.Errorf("header = %q", lines[0])
	}
	for i, line := range lines[1:] {
		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			t.Errorf("row %d malformed: %q", i, line)
			continue
		}
		if parts[0] != itoa(i) {
			t.Errorf("row %d index = %q, want %d", i, parts[0], i)
		}
	}
}

func TestCSV_RowsBounded(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/export/csv?rows=999999999", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized rows should be 400, got %d", w.Code)
	}
}

func TestLongRunning_HonorsDuration(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/export/long-running?duration_ms=100", nil))
	elapsed := time.Since(start)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if elapsed < 90*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("elapsed %v outside [90ms, 500ms]", elapsed)
	}
	var resp LongRunningResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.SleptMS != 100 {
		t.Errorf("SleptMS = %d, want 100", resp.SleptMS)
	}
}

func TestLongRunning_StopsOnContextCancel(t *testing.T) {
	mux := newMux(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/export/long-running?duration_ms=5000", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(w, req)
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("context cancel not honored: %v", elapsed)
	}
}

func TestLongRunning_DurationBounded(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/export/long-running?duration_ms=999999999", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized duration should be 400, got %d", w.Code)
	}
}

func TestValidation(t *testing.T) {
	mux := newMux(t)
	cases := []string{
		"/v1/export/big-body?size_kb=abc",
		"/v1/export/big-body?size_kb=0",
		"/v1/export/big-body?size_kb=-1",
		"/v1/export/csv?rows=0",
		"/v1/export/csv?rows=abc",
		"/v1/export/long-running?duration_ms=0",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
			if w.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400", w.Code)
			}
		})
	}
}

func TestRoutes_Shape(t *testing.T) {
	routes := New().Routes()
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3", len(routes))
	}
	for _, r := range routes {
		if r.Group != groupName {
			t.Errorf("%s group = %q", r.Path, r.Group)
		}
		if r.QueryParams == nil {
			t.Errorf("%s missing QueryParams", r.Path)
		}
		if r.ResponseBody == nil {
			t.Errorf("%s missing ResponseBody", r.Path)
		}
	}
}

func getBody(t *testing.T, mux *http.ServeMux, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d for %s", w.Code, path)
	}
	return w.Body.String()
}

// itoa avoids strconv import in the test for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string('0'+byte(n%10)) + digits
		n /= 10
	}
	return digits
}
