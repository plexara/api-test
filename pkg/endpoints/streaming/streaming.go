// Package streaming provides controllable streaming-response endpoints
// (chunked, Server-Sent Events, NDJSON). They exist so a gateway test can
// verify that the gateway preserves transfer encoding, flush boundaries,
// and content type across the proxy hop. Same (count, seed) → same body.
package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- intentional: PCG seeded from a caller-supplied string for reproducible test fixtures; crypto/rand would defeat the determinism contract.
	"net/http"
	"strconv"
	"time"

	"github.com/plexara/api-test/pkg/endpoints"
)

const (
	groupName = "streaming"

	// maxCount bounds chunked/sse/ndjson item count. Larger streams
	// belong on the export group, which is built for long bodies.
	maxCount = 1000

	// maxDelayMS bounds the inter-item delay. Long delays exercise
	// gateway read timeouts; values beyond this are diminishing
	// returns and a foot-gun for CI.
	maxDelayMS = 5000

	// defaultCount is the count returned when the caller omits the
	// query parameter.
	defaultCount = 5
)

// Group implements endpoints.Endpoints for the streaming group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "streaming_chunked",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/streaming/chunked",
			Description:  "Stream N text chunks with Transfer-Encoding: chunked. Each chunk is one line, deterministically generated from (seed, index).",
			QueryParams:  (*StreamQuery)(nil),
			ResponseBody: (*ChunkedResponse)(nil),
		},
		{
			Name:         "streaming_sse",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/streaming/sse",
			Description:  "Stream N Server-Sent Events (text/event-stream). Each event has id and data fields; data payload is deterministic from (seed, index).",
			QueryParams:  (*StreamQuery)(nil),
			ResponseBody: (*SSEEvent)(nil),
		},
		{
			Name:         "streaming_ndjson",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/streaming/ndjson",
			Description:  "Stream N newline-delimited JSON objects (application/x-ndjson). Same (count, seed) reproduces the same body.",
			QueryParams:  (*StreamQuery)(nil),
			ResponseBody: (*NDJSONLine)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/streaming/chunked", mw(http.HandlerFunc(g.chunked)))
	mux.Handle("GET /v1/streaming/sse", mw(http.HandlerFunc(g.sse)))
	mux.Handle("GET /v1/streaming/ndjson", mw(http.HandlerFunc(g.ndjson)))
}

// StreamQuery is the shared query-parameter shape for every streaming
// endpoint. Count caps at maxCount; delay caps at maxDelayMS; seed is
// optional and controls content determinism.
type StreamQuery struct {
	Count   int    `json:"count,omitempty"`
	DelayMS int    `json:"delay_ms,omitempty"`
	Seed    string `json:"seed,omitempty"`
}

// ChunkedResponse documents the shape of each chunk in the chunked
// endpoint's stream. The wire format is `text/plain`; this struct
// describes the per-chunk text the reflector emits so the OpenAPI doc
// has something concrete to point at.
type ChunkedResponse struct {
	Index int    `json:"index"`
	Word  string `json:"word"`
}

// SSEEvent documents the shape of one SSE event's data payload.
type SSEEvent struct {
	ID   int    `json:"id"`
	Word string `json:"word"`
}

// NDJSONLine documents the shape of one newline-delimited JSON object.
type NDJSONLine struct {
	Index int    `json:"index"`
	Word  string `json:"word"`
}

// chunked writes count text lines separated by '\n', flushing between
// each. Transfer-Encoding: chunked is set by net/http automatically
// because we don't pre-set Content-Length and we flush.
func (g *Group) chunked(w http.ResponseWriter, r *http.Request) {
	q, ok := parseStreamQuery(w, r)
	if !ok {
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	for i := 0; i < q.Count; i++ {
		_, _ = fmt.Fprintf(w, "chunk %d: %s\n", i, deterministicWord(q.Seed, i))
		if flusher != nil {
			flusher.Flush()
		}
		if q.DelayMS > 0 && i < q.Count-1 {
			if !waitOrCancel(r.Context(), time.Duration(q.DelayMS)*time.Millisecond) {
				return
			}
		}
	}
}

// sse writes count SSE events. Each event has an explicit id and a JSON
// payload so a gateway can verify event boundaries are preserved through
// proxying.
func (g *Group) sse(w http.ResponseWriter, r *http.Request) {
	q, ok := parseStreamQuery(w, r)
	if !ok {
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	for i := 0; i < q.Count; i++ {
		payload, _ := json.Marshal(SSEEvent{ID: i, Word: deterministicWord(q.Seed, i)})
		_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i, payload)
		if flusher != nil {
			flusher.Flush()
		}
		if q.DelayMS > 0 && i < q.Count-1 {
			if !waitOrCancel(r.Context(), time.Duration(q.DelayMS)*time.Millisecond) {
				return
			}
		}
	}
}

// ndjson writes count newline-delimited JSON objects.
func (g *Group) ndjson(w http.ResponseWriter, r *http.Request) {
	q, ok := parseStreamQuery(w, r)
	if !ok {
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	for i := 0; i < q.Count; i++ {
		_ = enc.Encode(NDJSONLine{Index: i, Word: deterministicWord(q.Seed, i)})
		if flusher != nil {
			flusher.Flush()
		}
		if q.DelayMS > 0 && i < q.Count-1 {
			if !waitOrCancel(r.Context(), time.Duration(q.DelayMS)*time.Millisecond) {
				return
			}
		}
	}
}

// parseStreamQuery extracts and validates count/delay_ms/seed from the
// request. On validation failure it writes a 400 and returns ok=false.
func parseStreamQuery(w http.ResponseWriter, r *http.Request) (StreamQuery, bool) {
	q := r.URL.Query()
	out := StreamQuery{Seed: q.Get("seed")}

	if raw := q.Get("count"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeBadRequest(w, "count must be a non-negative integer")
			return out, false
		}
		if n > maxCount {
			writeBadRequest(w, fmt.Sprintf("count %d exceeds max %d", n, maxCount))
			return out, false
		}
		out.Count = n
	} else {
		out.Count = defaultCount
	}

	if raw := q.Get("delay_ms"); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms < 0 {
			writeBadRequest(w, "delay_ms must be a non-negative integer")
			return out, false
		}
		if ms > maxDelayMS {
			writeBadRequest(w, fmt.Sprintf("delay_ms %d exceeds max %d", ms, maxDelayMS))
			return out, false
		}
		out.DelayMS = ms
	}
	return out, true
}

// deterministicWord returns a stable word for the (seed, index) pair so
// callers can replay a stream and bit-compare. Re-seeded per call so
// requesting index 7 in a 100-item stream returns the same word it would
// in a 10-item stream.
func deterministicWord(seed string, index int) string {
	combined := fmt.Sprintf("%s:%d", seed, index)
	h := fnv.New64a()
	_, _ = h.Write([]byte(combined))
	sum := h.Sum64()
	rng := rand.New(rand.NewPCG(sum, sum>>1)) //#nosec G404 -- see package import note
	return dict[rng.IntN(len(dict))]
}

// dict is a fixed word list so the deterministic generator's output is
// readable and comparable across builds. Same word at the same index
// for any given seed.
var dict = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot",
	"golf", "hotel", "india", "juliet", "kilo", "lima",
	"mike", "november", "oscar", "papa", "quebec", "romeo",
	"sierra", "tango", "uniform", "victor", "whiskey", "xray",
	"yankee", "zulu",
}

// waitOrCancel sleeps for d or returns early when ctx is cancelled (the
// usual cause: client disconnect). Reports true when the wait completed
// normally, false when the context cancelled — the caller stops streaming
// so we don't sit on a goroutine the client no longer cares about.
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

// writeBadRequest writes a 400 JSON error. Streaming endpoints reject
// malformed input before opening the stream, so this is the only error
// path; using a fixed-status helper keeps the lint pass happy and makes
// the call sites read at a glance.
func writeBadRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
