// Package pagination provides three deterministic paginated endpoints,
// one per cursor style the Plexara API gateway recognizes:
//
//   - Link header (RFC 5988) — /v1/pagination/link
//   - OData v4 ($top, $skip, @odata.nextLink) — /v1/pagination/odata
//   - Opaque cursor — /v1/pagination/cursor
//
// Each one slices a deterministic synthetic dataset of (id, value) pairs.
// Same (total, page-shape) produces the same items, so a gateway client
// can assert that pagination metadata (Link headers, nextLink URLs,
// opaque cursors) survive the proxy hop intact.
package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

const (
	groupName = "pagination"

	// defaultTotal is the synthetic dataset size when ?total= is
	// omitted. 100 is large enough to need pagination but small
	// enough that "fetch every page" tests stay snappy.
	defaultTotal = 100

	// defaultPageSize is the page size when ?per_page/$top/limit is
	// omitted.
	defaultPageSize = 10

	// maxTotal bounds the synthetic dataset. Tests that need more
	// items belong on the export group.
	maxTotal = 10000

	// maxPageSize bounds a single page's item count.
	maxPageSize = 1000
)

// Group implements endpoints.Endpoints for the pagination group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "pagination_link",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/pagination/link",
			Description:  "Paginate via RFC 5988 Link header (rel=next/prev/first/last). Verify the gateway preserves Link header URLs through proxying.",
			QueryParams:  (*LinkQuery)(nil),
			ResponseBody: (*LinkResponse)(nil),
		},
		{
			Name:         "pagination_odata",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/pagination/odata",
			Description:  "Paginate via OData v4 $top / $skip with @odata.nextLink in the body. Verify the gateway preserves the nextLink URL.",
			QueryParams:  (*ODataQuery)(nil),
			ResponseBody: (*ODataResponse)(nil),
		},
		{
			Name:         "pagination_cursor",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/pagination/cursor",
			Description:  "Paginate via opaque base64 cursor. Verify the gateway round-trips the cursor value without interpretation.",
			QueryParams:  (*CursorQuery)(nil),
			ResponseBody: (*CursorResponse)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/pagination/link", mw(http.HandlerFunc(g.link)))
	mux.Handle("GET /v1/pagination/odata", mw(http.HandlerFunc(g.odata)))
	mux.Handle("GET /v1/pagination/cursor", mw(http.HandlerFunc(g.cursor)))
}

// Item is the shared item shape across all three styles. value is
// hex(sha256(id)[:8]) so a caller can replay any (id) and bit-compare.
type Item struct {
	ID    int    `json:"id"`
	Value string `json:"value"`
}

// LinkQuery documents the link-style query parameters.
type LinkQuery struct {
	Page    int `json:"page,omitempty"`
	PerPage int `json:"per_page,omitempty"`
	Total   int `json:"total,omitempty"`
}

// LinkResponse is the body shape for /v1/pagination/link.
type LinkResponse struct {
	Items   []Item `json:"items"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	Total   int    `json:"total"`
}

// ODataQuery documents the OData query parameters. The OData spec uses
// $top and $skip, which aren't valid Go identifiers; the json tags
// rename them for parameter docs.
type ODataQuery struct {
	Top   int `json:"$top,omitempty"`
	Skip  int `json:"$skip,omitempty"`
	Total int `json:"total,omitempty"`
}

// ODataResponse is the body shape for /v1/pagination/odata.
type ODataResponse struct {
	Value    []Item `json:"value"`
	Count    int    `json:"@odata.count"`
	NextLink string `json:"@odata.nextLink,omitempty"`
}

// CursorQuery documents the cursor-style query parameters.
type CursorQuery struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Total  int    `json:"total,omitempty"`
}

// CursorResponse is the body shape for /v1/pagination/cursor.
type CursorResponse struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// link serves the RFC 5988 Link-header style.
func (g *Group) link(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := positiveDefault(q.Get("page"), 1)
	if err != nil {
		writeBadRequest(w, "page must be a positive integer")
		return
	}
	perPage, err := boundedDefault(q.Get("per_page"), defaultPageSize, maxPageSize, "per_page")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	total, err := boundedDefault(q.Get("total"), defaultTotal, maxTotal, "total")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	// Validate page against lastPage BEFORE computing `start`. The
	// untrusted `page` value combined with `perPage` would overflow
	// int multiplication (e.g. page=MaxInt) and produce a negative
	// `start` that bypasses the past-end check. lastPage is bounded
	// by maxTotal, so this comparison is always safe.
	lastPage := lastPageFor(total, perPage)
	if page > lastPage {
		writeBadRequest(w, fmt.Sprintf("page %d is past end of dataset (total=%d, per_page=%d, last_page=%d)",
			page, total, perPage, lastPage))
		return
	}
	start := (page - 1) * perPage
	items := sliceItems(start, perPage, total)

	base := requestBaseURL(r)
	parts := []string{
		linkHeader(base, "first", 1, perPage, total),
		linkHeader(base, "last", lastPage, perPage, total),
	}
	if page > 1 {
		parts = append(parts, linkHeader(base, "prev", page-1, perPage, total))
	}
	if page < lastPage {
		parts = append(parts, linkHeader(base, "next", page+1, perPage, total))
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
	writeJSON(w, http.StatusOK, LinkResponse{
		Items: items, Page: page, PerPage: perPage, Total: total,
	})
}

// odata serves the OData v4 $top / $skip / @odata.nextLink style.
func (g *Group) odata(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	top, err := boundedDefault(q.Get("$top"), defaultPageSize, maxPageSize, "$top")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	skip, err := nonNegativeDefault(q.Get("$skip"), 0)
	if err != nil {
		writeBadRequest(w, "$skip must be a non-negative integer")
		return
	}
	total, err := boundedDefault(q.Get("total"), defaultTotal, maxTotal, "total")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	if skip >= total {
		writeBadRequest(w, fmt.Sprintf("$skip %d is past end of dataset (total=%d)", skip, total))
		return
	}

	items := sliceItems(skip, top, total)
	resp := ODataResponse{Value: items, Count: total}
	if skip+top < total {
		resp.NextLink = fmt.Sprintf("%s?$top=%d&$skip=%d&total=%d",
			requestBaseURL(r), top, skip+top, total)
	}
	writeJSON(w, http.StatusOK, resp)
}

// cursor serves the opaque-cursor style. The cursor is a base64-encoded
// integer offset; clients shouldn't interpret it, only pass it back.
func (g *Group) cursor(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := boundedDefault(q.Get("limit"), defaultPageSize, maxPageSize, "limit")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	total, err := boundedDefault(q.Get("total"), defaultTotal, maxTotal, "total")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}

	offset := 0
	if c := q.Get("cursor"); c != "" {
		n, derr := decodeCursor(c)
		if derr != nil {
			writeBadRequest(w, "cursor is malformed")
			return
		}
		offset = n
	}
	if offset >= total {
		writeBadRequest(w, fmt.Sprintf("cursor offset %d is past end of dataset (total=%d)", offset, total))
		return
	}

	items := sliceItems(offset, limit, total)
	resp := CursorResponse{Items: items}
	if offset+limit < total {
		resp.NextCursor = encodeCursor(offset + limit)
	}
	writeJSON(w, http.StatusOK, resp)
}

// sliceItems returns the [start, start+pageSize) window of the synthetic
// dataset, clamped to total. Returns nil (not []Item{}) when start is
// already past total so the response body is consistent across styles.
func sliceItems(start, pageSize, total int) []Item {
	if start >= total {
		return nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]Item, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, Item{ID: i, Value: deterministicValue(i)})
	}
	return out
}

// deterministicValue returns hex(sha256(id)[:8]) as a stable per-id
// value. Same id always produces the same value, across builds and
// across pagination styles, so a gateway test can assert "items at
// offset N from /link match items at $skip=N from /odata".
func deterministicValue(id int) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(id)))
	return hex.EncodeToString(sum[:8])
}

// lastPageFor returns the 1-indexed last page number. Returns 1 when
// total is 0 so the response body and Link header remain well-formed.
func lastPageFor(total, perPage int) int {
	if total <= 0 {
		return 1
	}
	return (total + perPage - 1) / perPage
}

// linkHeader builds one RFC 5988 Link header entry: <url>; rel="rel".
func linkHeader(base, rel string, page, perPage, total int) string {
	return fmt.Sprintf(`<%s?page=%d&per_page=%d&total=%d>; rel=%q`,
		base, page, perPage, total, rel)
}

// requestBaseURL returns the scheme+host+path with no query string. Used
// by link and odata to construct next/prev URLs that point back at the
// same handler.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.Path)
}

// encodeCursor wraps an int offset as an opaque base64 string. URL-safe
// encoding so the value round-trips through a query parameter without
// further escaping.
func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeCursor is the inverse of encodeCursor.
func decodeCursor(s string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("cursor offset negative")
	}
	return n, nil
}

func positiveDefault(raw string, def int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return n, nil
}

func nonNegativeDefault(raw string, def int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	return n, nil
}

func boundedDefault(raw string, def, upper int, name string) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	if n > upper {
		return 0, fmt.Errorf("%s %d exceeds max %d", name, n, upper)
	}
	return n, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeBadRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
