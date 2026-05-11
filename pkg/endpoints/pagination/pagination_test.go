package pagination

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

func TestLink_Defaults(t *testing.T) {
	mux := newMux(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/pagination/link", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}
	var body LinkResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Page != 1 || body.PerPage != defaultPageSize || body.Total != defaultTotal {
		t.Errorf("defaults wrong: page=%d per_page=%d total=%d", body.Page, body.PerPage, body.Total)
	}
	if len(body.Items) != defaultPageSize {
		t.Errorf("got %d items, want %d", len(body.Items), defaultPageSize)
	}
	if body.Items[0].ID != 0 || body.Items[len(body.Items)-1].ID != defaultPageSize-1 {
		t.Errorf("page-1 item ids wrong: first=%d last=%d", body.Items[0].ID, body.Items[len(body.Items)-1].ID)
	}
}

func TestLink_HeaderShape(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/link?page=2&per_page=10&total=50", nil))

	header := w.Header().Get("Link")
	for _, rel := range []string{`rel="first"`, `rel="last"`, `rel="prev"`, `rel="next"`} {
		if !strings.Contains(header, rel) {
			t.Errorf("Link header missing %s\n%s", rel, header)
		}
	}
	// First page has no prev; last page has no next.
	wFirst := httptest.NewRecorder()
	mux.ServeHTTP(wFirst, httptest.NewRequest(http.MethodGet, "/v1/pagination/link?page=1&per_page=10&total=50", nil))
	if strings.Contains(wFirst.Header().Get("Link"), `rel="prev"`) {
		t.Errorf("page 1 should not have prev: %s", wFirst.Header().Get("Link"))
	}

	wLast := httptest.NewRecorder()
	mux.ServeHTTP(wLast, httptest.NewRequest(http.MethodGet, "/v1/pagination/link?page=5&per_page=10&total=50", nil))
	if strings.Contains(wLast.Header().Get("Link"), `rel="next"`) {
		t.Errorf("last page should not have next: %s", wLast.Header().Get("Link"))
	}
}

func TestLink_PartialLastPage(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/link?page=3&per_page=10&total=25", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body LinkResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Items) != 5 {
		t.Errorf("last partial page should have 5 items, got %d", len(body.Items))
	}
}

func TestLink_PastEnd(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/link?page=100&per_page=10&total=20", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("past-end should be 400, got %d", w.Code)
	}
}

// TestLink_HugePageNoOverflow guards against (page-1)*perPage integer
// overflow producing a negative start that bypasses the past-end check
// and returns items with negative IDs. Regression for an earlier
// implementation that validated `start >= total` after the multiplication.
func TestLink_HugePageNoOverflow(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/v1/pagination/link?page=9223372036854775807&per_page=10&total=100", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("huge page should be 400, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestOData_NextLink(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/odata?$top=10&$skip=0&total=25", nil))
	var resp ODataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 25 {
		t.Errorf("@odata.count = %d, want 25", resp.Count)
	}
	if !strings.Contains(resp.NextLink, "$skip=10") {
		t.Errorf("nextLink should advance to $skip=10: %q", resp.NextLink)
	}
	if len(resp.Value) != 10 {
		t.Errorf("page size wrong: %d", len(resp.Value))
	}
}

func TestOData_LastPageNoNextLink(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/odata?$top=10&$skip=20&total=25", nil))
	var resp ODataResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.NextLink != "" {
		t.Errorf("last page should have empty nextLink, got %q", resp.NextLink)
	}
	if len(resp.Value) != 5 {
		t.Errorf("last partial page size = %d, want 5", len(resp.Value))
	}
}

func TestCursor_Walk(t *testing.T) {
	mux := newMux(t)
	cursor := ""
	seen := map[int]bool{}
	for steps := 0; steps < 20; steps++ {
		query := "limit=10&total=25"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/cursor?"+query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("step %d status %d (body=%s)", steps, w.Code, w.Body.String())
		}
		var resp CursorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		for _, item := range resp.Items {
			if seen[item.ID] {
				t.Errorf("step %d duplicate item id %d", steps, item.ID)
			}
			seen[item.ID] = true
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	if len(seen) != 25 {
		t.Errorf("walked %d items, want 25 — cursor pagination skipped or duplicated", len(seen))
	}
}

func TestCursor_OpaqueRoundTrip(t *testing.T) {
	mux := newMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/pagination/cursor?limit=5&total=50", nil))
	var resp CursorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.NextCursor == "" {
		t.Fatal("expected next cursor")
	}
	// The cursor must be opaque to the client; we only assert it
	// round-trips and advances the offset.
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest(http.MethodGet,
		"/v1/pagination/cursor?limit=5&total=50&cursor="+resp.NextCursor, nil))
	var resp2 CursorResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Items[0].ID != 5 {
		t.Errorf("cursor did not advance: first id = %d, want 5", resp2.Items[0].ID)
	}
}

func TestCursor_Malformed(t *testing.T) {
	mux := newMux(t)
	cases := []string{
		"%21%21%21",
		"not-base64!",
		"AAAA", // valid base64 but content is three nul bytes — Atoi fails
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
				"/v1/pagination/cursor?cursor="+c, nil))
			if w.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeterministicValue_StableAcrossStyles(t *testing.T) {
	mux := newMux(t)
	// Pull item id=7 from each style.
	get := func(t *testing.T, path string) Item {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		var items []Item
		if data, ok := raw["items"]; ok {
			_ = json.Unmarshal(data, &items)
		} else if data, ok := raw["value"]; ok {
			_ = json.Unmarshal(data, &items)
		}
		for _, it := range items {
			if it.ID == 7 {
				return it
			}
		}
		t.Fatalf("id 7 not in %s body", path)
		return Item{}
	}
	a := get(t, "/v1/pagination/link?page=1&per_page=10&total=10")
	b := get(t, "/v1/pagination/odata?$top=10&$skip=0&total=10")
	c := get(t, "/v1/pagination/cursor?limit=10&total=10")
	if a.Value != b.Value || b.Value != c.Value {
		t.Errorf("id 7 value differs across styles: link=%q odata=%q cursor=%q",
			a.Value, b.Value, c.Value)
	}
}

func TestValidation(t *testing.T) {
	mux := newMux(t)
	cases := []struct {
		path   string
		status int
	}{
		{"/v1/pagination/link?page=0", http.StatusBadRequest},
		{"/v1/pagination/link?page=abc", http.StatusBadRequest},
		{"/v1/pagination/link?per_page=99999", http.StatusBadRequest},
		{"/v1/pagination/link?total=99999", http.StatusBadRequest},
		{"/v1/pagination/odata?$top=0", http.StatusBadRequest},
		{"/v1/pagination/odata?$skip=-1", http.StatusBadRequest},
		{"/v1/pagination/cursor?limit=0", http.StatusBadRequest},
		{"/v1/pagination/link", http.StatusOK},
		{"/v1/pagination/odata", http.StatusOK},
		{"/v1/pagination/cursor", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.path, nil))
			if w.Code != c.status {
				t.Errorf("status %d, want %d (body=%s)", w.Code, c.status, w.Body.String())
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
			t.Errorf("%s Group = %q", r.Path, r.Group)
		}
		if r.QueryParams == nil {
			t.Errorf("%s missing QueryParams", r.Path)
		}
		if r.ResponseBody == nil {
			t.Errorf("%s missing ResponseBody", r.Path)
		}
	}
}

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 10, 999, 12345} {
		enc := encodeCursor(n)
		got, err := decodeCursor(enc)
		if err != nil {
			t.Errorf("decode %q: %v", enc, err)
			continue
		}
		if got != n {
			t.Errorf("round trip %d → %q → %d", n, enc, got)
		}
	}
}
