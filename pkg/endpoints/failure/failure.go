// Package failure provides test endpoints that produce controlled failure
// modes - error responses, latency, and probabilistic flakiness; so a
// gateway can be exercised against well-defined adversarial inputs.
package failure

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/plexara/api-test/pkg/endpoints"
)

const groupName = "failure"

// Group implements endpoints.Endpoints for the failure group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "status",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/status/{code}",
			Description:  "Return the supplied HTTP status code (httpbin-style). Body documents what was returned.",
			ResponseBody: (*StatusResponse)(nil),
		},
		{
			Name:         "slow",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/slow",
			Description:  "Sleep for ?ms=N milliseconds before responding (cap 60s). Honors context cancellation.",
			QueryParams:  (*SlowQuery)(nil),
			ResponseBody: (*SlowResponse)(nil),
		},
		{
			Name:         "flaky",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/flaky",
			Description:  "Return 200 or 503 based on ?fail_rate=&seed=&call_id=. Same seed+call_id always yields the same outcome.",
			QueryParams:  (*FlakyQuery)(nil),
			ResponseBody: (*FlakyResponse)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/status/{code}", mw(http.HandlerFunc(g.status)))
	mux.Handle("GET /v1/slow", mw(http.HandlerFunc(g.slow)))
	mux.Handle("GET /v1/flaky", mw(http.HandlerFunc(g.flaky)))
}

// StatusResponse is the wire shape of GET /v1/status/{code}.
type StatusResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (g *Group) status(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(r.PathValue("code"))
	if err != nil || code < 100 || code > 599 {
		writeJSONError(w, http.StatusBadRequest, "code must be an integer in [100, 599]")
		return
	}
	writeJSON(w, code, StatusResponse{Status: code, Message: http.StatusText(code)})
}

// SlowQuery is the documented query parameters for GET /v1/slow.
type SlowQuery struct {
	MS int `json:"ms"`
}

// SlowResponse is the wire shape of GET /v1/slow.
type SlowResponse struct {
	SleptMS    int64 `json:"slept_ms"`
	Cancelled  bool  `json:"cancelled,omitempty"`
	RequestedM int   `json:"requested_ms"`
}

func (g *Group) slow(w http.ResponseWriter, r *http.Request) {
	ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
	if ms < 0 {
		ms = 0
	}
	if ms > 60_000 {
		ms = 60_000
	}
	start := time.Now()
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		writeJSON(w, http.StatusOK, SlowResponse{
			SleptMS:    time.Since(start).Milliseconds(),
			RequestedM: ms,
		})
	case <-r.Context().Done():
		// Best-effort: write the cancellation note. The client may already
		// be gone; the audit middleware records the partial state.
		writeJSON(w, 499, SlowResponse{
			SleptMS:    time.Since(start).Milliseconds(),
			Cancelled:  true,
			RequestedM: ms,
		})
	}
}

// FlakyQuery is the documented query parameters for GET /v1/flaky.
type FlakyQuery struct {
	FailRate float64 `json:"fail_rate"`
	Seed     string  `json:"seed,omitempty"`
	CallID   int     `json:"call_id,omitempty"`
}

// FlakyResponse is the wire shape of GET /v1/flaky.
type FlakyResponse struct {
	Failed   bool    `json:"failed"`
	Roll     float64 `json:"roll"`
	FailRate float64 `json:"fail_rate"`
}

func (g *Group) flaky(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rate, _ := strconv.ParseFloat(q.Get("fail_rate"), 64)
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	callID, _ := strconv.Atoi(q.Get("call_id"))
	rng := flakyRand(q.Get("seed"), callID)
	roll := rng.Float64()
	if roll < rate {
		writeJSON(w, http.StatusServiceUnavailable, FlakyResponse{
			Failed: true, Roll: roll, FailRate: rate,
		})
		return
	}
	writeJSON(w, http.StatusOK, FlakyResponse{
		Failed: false, Roll: roll, FailRate: rate,
	})
}

// flakyRand returns a *rand.Rand seeded by (seed, callID) so failures are
// reproducible across runs. math/rand/v2 is intentional; this is a test
// fixture, not a security primitive.
func flakyRand(seed string, callID int) *rand.Rand {
	if seed == "" {
		return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) // #nosec G404 -- non-crypto PRNG; test fixture
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	_, _ = fmt.Fprintf(h, "|%d", callID)
	a := h.Sum64()
	h.Reset()
	_, _ = h.Write([]byte("salt|" + seed))
	_, _ = fmt.Fprintf(h, "|%d", callID)
	b := h.Sum64()
	return rand.New(rand.NewPCG(a, b)) // #nosec G404 -- non-crypto PRNG; test fixture
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
