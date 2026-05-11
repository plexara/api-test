// Package export provides large/long-running endpoints intended as
// targets for the Plexara API gateway's api_export tool. The point is
// to verify the gateway handles big bodies and slow first-byte
// scenarios without timing out, truncating, or dropping connections.
//
// Endpoints:
//
//   - GET /v1/export/big-body?size_kb=N&seed=S   — N KiB JSON-array
//   - GET /v1/export/csv?rows=N&seed=S           — N-row CSV
//   - GET /v1/export/long-running?duration_ms=N  — slow first-byte
package export

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/plexara/api-test/pkg/endpoints"
)

const (
	groupName = "export"

	// maxBodyKB bounds /v1/export/big-body responses at 10 MiB so a
	// runaway test request can't consume unbounded memory or wall
	// time. Real api_export usage moves more than this; the cap is
	// fixture-side defense, not a contract limit.
	maxBodyKB = 10240 // 10 MiB

	// maxRows bounds /v1/export/csv at 250k rows (~ a few MiB).
	maxRows = 250_000

	// maxDurationMS bounds /v1/export/long-running at 60 s. Long
	// enough to exercise gateway request-timeout policy, short
	// enough that test runs stay tractable.
	maxDurationMS = 60_000

	// defaultSizeKB and defaultRows are the values returned when the
	// caller omits the size knob.
	defaultSizeKB = 64
	defaultRows   = 1000
)

// Group implements endpoints.Endpoints for the export group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "export_big_body",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/export/big-body",
			Description:  "Stream an approximately N KiB JSON array of deterministic rows. Tests how the gateway forwards large response bodies (buffering, content-length, connection reuse).",
			QueryParams:  (*BigBodyQuery)(nil),
			ResponseBody: (*BigBodyRow)(nil),
		},
		{
			Name:         "export_csv",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/export/csv",
			Description:  "Return a deterministic CSV with N rows. Tests gateway behavior on non-JSON content types and large text bodies.",
			QueryParams:  (*CSVQuery)(nil),
			ResponseBody: (*CSVRow)(nil),
		},
		{
			Name:         "export_long_running",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/export/long-running",
			Description:  "Sleep for duration_ms before responding with timing info. Tests how the gateway handles slow first-byte. Stops early on client disconnect (no goroutine leak).",
			QueryParams:  (*LongRunningQuery)(nil),
			ResponseBody: (*LongRunningResponse)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/export/big-body", mw(http.HandlerFunc(g.bigBody)))
	mux.Handle("GET /v1/export/csv", mw(http.HandlerFunc(g.csv)))
	mux.Handle("GET /v1/export/long-running", mw(http.HandlerFunc(g.longRunning)))
}

// BigBodyQuery documents the big-body query parameters.
type BigBodyQuery struct {
	SizeKB int    `json:"size_kb,omitempty"`
	Seed   string `json:"seed,omitempty"`
}

// BigBodyRow is one element of the big-body response array.
type BigBodyRow struct {
	Index int    `json:"index"`
	Value string `json:"value"`
}

// CSVQuery documents the csv query parameters.
type CSVQuery struct {
	Rows int    `json:"rows,omitempty"`
	Seed string `json:"seed,omitempty"`
}

// CSVRow documents the csv row shape. Wire format is text/csv; this
// struct exists so the OpenAPI reflector can produce a schema for the
// row contents that match the produced columns.
type CSVRow struct {
	Index int    `json:"index"`
	Value string `json:"value"`
}

// LongRunningQuery documents the long-running query parameters.
type LongRunningQuery struct {
	DurationMS int `json:"duration_ms,omitempty"`
}

// LongRunningResponse is the body of /v1/export/long-running.
type LongRunningResponse struct {
	SleptMS int `json:"slept_ms"`
}

// bigBody streams a large JSON array of {index, value} rows where value
// is hex(sha256(seed:index))[:16]. The handler stops writing on client
// disconnect.
func (g *Group) bigBody(w http.ResponseWriter, r *http.Request) {
	sizeKB, err := boundedDefault(r.URL.Query().Get("size_kb"),
		defaultSizeKB, maxBodyKB, "size_kb")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	seed := r.URL.Query().Get("seed")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	target := sizeKB * 1024
	// Each row is roughly 50 bytes ({"index":N,"value":"..."}). Cap
	// the loop independently in case our size estimate drifts so we
	// never emit dramatically more than requested.
	maxIter := target/40 + 64
	written := 1
	_, _ = w.Write([]byte("["))
	first := true
	for i := 0; i < maxIter && written < target; i++ {
		if r.Context().Err() != nil {
			return
		}
		row := BigBodyRow{Index: i, Value: deterministicValue(seed, i)}
		enc, _ := json.Marshal(row)
		if !first {
			_, _ = w.Write([]byte(","))
			written++
		}
		n, _ := w.Write(enc)
		written += n
		first = false
		if flusher != nil && i%64 == 0 {
			flusher.Flush()
		}
	}
	_, _ = w.Write([]byte("]"))
}

// csv writes a CSV with header + N rows. Content-Type is text/csv so a
// gateway has to handle non-JSON bodies correctly.
func (g *Group) csv(w http.ResponseWriter, r *http.Request) {
	rows, err := boundedDefault(r.URL.Query().Get("rows"),
		defaultRows, maxRows, "rows")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	seed := r.URL.Query().Get("seed")

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	// encoding/csv handles RFC 4180 escaping (commas, quotes, newlines)
	// regardless of what seed contains. Using it instead of fmt.Fprintf
	// also keeps gosec's G705 (XSS-via-taint) check from flagging seed
	// flowing into a templated writer.
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"index", "value"})
	for i := 0; i < rows; i++ {
		if r.Context().Err() != nil {
			cw.Flush()
			return
		}
		_ = cw.Write([]string{strconv.Itoa(i), deterministicValue(seed, i)})
		if flusher != nil && i%256 == 0 {
			cw.Flush()
			flusher.Flush()
		}
	}
	cw.Flush()
}

// longRunning sleeps for the requested duration before responding,
// honoring r.Context() cancellation so a client disconnect aborts the
// goroutine instead of running out the full duration.
func (g *Group) longRunning(w http.ResponseWriter, r *http.Request) {
	dur, err := boundedDefault(r.URL.Query().Get("duration_ms"),
		1000, maxDurationMS, "duration_ms")
	if err != nil {
		writeBadRequest(w, err.Error())
		return
	}
	if !waitOrCancel(r.Context(), time.Duration(dur)*time.Millisecond) {
		// Client gave up before we got here; don't try to write.
		return
	}
	writeJSONOK(w, LongRunningResponse{SleptMS: dur})
}

// waitOrCancel sleeps for d or returns early when ctx is cancelled.
// Reports true when the wait completed normally, false on cancel.
func waitOrCancel(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// deterministicValue returns hex(sha256(seed:index))[:16] as a stable
// per-(seed, index) value. Matches the pagination group's
// deterministicValue style so test fixtures share semantics across
// groups.
func deterministicValue(seed string, index int) string {
	combined := seed + ":" + strconv.Itoa(index)
	sum := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(sum[:8])
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

func writeJSONOK(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

func writeBadRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": strings.TrimSpace(msg)})
}
