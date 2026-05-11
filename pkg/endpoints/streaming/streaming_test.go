package streaming

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

func TestChunked_Default(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/streaming/chunked", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := strings.Split(strings.TrimRight(w.Body.String(), "\n"), "\n")
	if len(lines) != defaultCount {
		t.Errorf("got %d lines, want %d (body=%q)", len(lines), defaultCount, w.Body.String())
	}
	if !strings.HasPrefix(lines[0], "chunk 0:") {
		t.Errorf("first line shape wrong: %q", lines[0])
	}
}

func TestChunked_Deterministic(t *testing.T) {
	mux := newMux(t)
	body1 := getBody(t, mux, "/v1/streaming/chunked?count=10&seed=fixed")
	body2 := getBody(t, mux, "/v1/streaming/chunked?count=10&seed=fixed")
	if body1 != body2 {
		t.Errorf("same (count, seed) produced different bodies:\nA=%q\nB=%q", body1, body2)
	}
	// Different seed must produce different content.
	body3 := getBody(t, mux, "/v1/streaming/chunked?count=10&seed=other")
	if body1 == body3 {
		t.Errorf("different seed produced same body")
	}
}

func TestSSE_EventShape(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/streaming/sse?count=3&seed=x", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}

	body := w.Body.String()
	// Three events, each "id: N\ndata: {...}\n\n".
	parts := strings.Split(strings.TrimRight(body, "\n"), "\n\n")
	if len(parts) != 3 {
		t.Fatalf("got %d events, want 3 (body=%q)", len(parts), body)
	}
	for i, p := range parts {
		s := bufio.NewScanner(strings.NewReader(p))
		var idLine, dataLine string
		for s.Scan() {
			line := s.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				idLine = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				dataLine = strings.TrimPrefix(line, "data: ")
			}
		}
		wantID := []string{"0", "1", "2"}[i]
		if idLine != wantID {
			t.Errorf("event %d id = %q, want %q", i, idLine, wantID)
		}
		var ev SSEEvent
		if err := json.Unmarshal([]byte(dataLine), &ev); err != nil {
			t.Errorf("event %d data not valid JSON: %v (data=%q)", i, err, dataLine)
		}
		if ev.ID != i {
			t.Errorf("event %d JSON ID = %d", i, ev.ID)
		}
	}
}

func TestNDJSON_OneObjectPerLine(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/streaming/ndjson?count=4&seed=q", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := strings.Split(strings.TrimRight(w.Body.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (body=%q)", len(lines), w.Body.String())
	}
	for i, line := range lines {
		var got NDJSONLine
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Errorf("line %d not JSON: %v (%q)", i, err, line)
			continue
		}
		if got.Index != i {
			t.Errorf("line %d Index = %d", i, got.Index)
		}
	}
}

func TestCountValidation(t *testing.T) {
	mux := newMux(t)
	cases := []struct {
		query  string
		status int
	}{
		{"count=abc", http.StatusBadRequest},
		{"count=-1", http.StatusBadRequest},
		{"count=99999", http.StatusBadRequest},
		{"delay_ms=abc", http.StatusBadRequest},
		{"delay_ms=-1", http.StatusBadRequest},
		{"delay_ms=99999", http.StatusBadRequest},
		{"count=0", http.StatusOK},
		{"count=5", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/streaming/chunked?"+c.query, nil))
			if w.Code != c.status {
				t.Errorf("status %d, want %d (body=%s)", w.Code, c.status, w.Body.String())
			}
		})
	}
}

func TestCountZero_EmptyBody(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/streaming/ndjson?count=0", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("count=0 should produce empty body, got %q", w.Body.String())
	}
}

func TestContextCancelStopsStream(t *testing.T) {
	mux := newMux(t)
	// count=10, delay_ms=200 would take ~1.8s normally. Cancel after
	// 50ms and the handler must return promptly.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/streaming/chunked?count=10&delay_ms=200", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(w, req)
	elapsed := time.Since(start)

	// Allow generous slack for CI; the bound matters, not the exact
	// time. With the bug the handler ran for ~1.8s.
	if elapsed > 500*time.Millisecond {
		t.Errorf("handler did not honor context cancel: ran for %v", elapsed)
	}
}

func TestDeterministicWord_StableAcrossCalls(t *testing.T) {
	a := deterministicWord("seed", 42)
	b := deterministicWord("seed", 42)
	if a != b {
		t.Errorf("same input produced different output: %q vs %q", a, b)
	}
	c := deterministicWord("other", 42)
	if a == c {
		t.Errorf("different seed produced same word: %q", a)
	}
}

func TestRoutes_RegisteredShape(t *testing.T) {
	routes := New().Routes()
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3", len(routes))
	}
	want := map[string]bool{
		"GET /v1/streaming/chunked": false,
		"GET /v1/streaming/sse":     false,
		"GET /v1/streaming/ndjson":  false,
	}
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected route: %s", key)
			continue
		}
		want[key] = true
		if r.Group != groupName {
			t.Errorf("route %s Group = %q, want %q", key, r.Group, groupName)
		}
		if r.QueryParams == nil {
			t.Errorf("route %s missing QueryParams (OpenAPI reflection needs this)", key)
		}
		if r.ResponseBody == nil {
			t.Errorf("route %s missing ResponseBody", key)
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("missing route %s", k)
		}
	}
}

func getBody(t *testing.T, mux *http.ServeMux, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d for %s (body=%s)", w.Code, path, w.Body.String())
	}
	return w.Body.String()
}
