// Package data provides deterministic-output test endpoints. Same input
// always produces the same output, which lets a gateway test enrichment
// dedup, caching, and size handling against known fixtures.
package data

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

const groupName = "data"

// Group implements endpoints.Endpoints for the data group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "fixed",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/fixed/{key}",
			Description:  "Return a deterministic body derived from the {key} path parameter. Same key, same body.",
			ResponseBody: (*FixedResponse)(nil),
		},
		{
			Name:         "sized",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/sized",
			Description:  "Return exactly ?bytes=N bytes of deterministic content (alphabet repeated). Capped at 32 MiB.",
			QueryParams:  (*SizedQuery)(nil),
			ResponseBody: (*SizedResponse)(nil),
		},
		{
			Name:         "lorem",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/lorem",
			Description:  "Return ?words=N words of seeded lorem-ipsum text. Same seed reproduces the same output.",
			QueryParams:  (*LoremQuery)(nil),
			ResponseBody: (*LoremResponse)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/fixed/{key}", mw(http.HandlerFunc(g.fixed)))
	mux.Handle("GET /v1/sized", mw(http.HandlerFunc(g.sized)))
	mux.Handle("GET /v1/lorem", mw(http.HandlerFunc(g.lorem)))
}

// FixedResponse is the wire shape of GET /v1/fixed/{key}.
type FixedResponse struct {
	Key  string `json:"key"`
	Hash string `json:"hash"`
	Body string `json:"body"`
}

func (g *Group) fixed(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	writeJSON(w, http.StatusOK, FixedResponse{
		Key:  key,
		Hash: h,
		Body: fmt.Sprintf("fixed[%s]: %s", key, h),
	})
}

// SizedQuery is the documented query parameters for GET /v1/sized.
type SizedQuery struct {
	Bytes int `json:"bytes"`
}

// SizedResponse is the wire shape of GET /v1/sized.
type SizedResponse struct {
	Bytes int    `json:"bytes"`
	Body  string `json:"body"`
}

const (
	sizedAlphabet = "abcdefghijklmnopqrstuvwxyz"
	// sizedMax bounds the result size at 32 MiB. Larger sizes belong on
	// the export endpoint group (M4), which streams to the asset store
	// instead of allocating in memory.
	sizedMax = 32 << 20
)

func (g *Group) sized(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("bytes"))
	if err != nil || n < 0 {
		writeJSONError(w, http.StatusBadRequest, "bytes must be a non-negative integer")
		return
	}
	if n > sizedMax {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("bytes %d exceeds max %d", n, sizedMax))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Stream the response so we don't allocate the full body up front.
	out := struct {
		Bytes int    `json:"bytes"`
		Body  string `json:"body"`
	}{Bytes: n, Body: ""}
	// Preamble: open object + bytes field + body field opening.
	_, _ = fmt.Fprintf(w, `{"bytes":%d,"body":"`, n)
	if n > 0 {
		buf := make([]byte, 4096)
		written := 0
		for written < n {
			chunk := n - written
			if chunk > len(buf) {
				chunk = len(buf)
			}
			for i := 0; i < chunk; i++ {
				buf[i] = sizedAlphabet[(written+i)%len(sizedAlphabet)]
			}
			_, _ = w.Write(buf[:chunk])
			written += chunk
		}
	}
	_, _ = w.Write([]byte(`"}`))
	_ = out // present for godoc; the streaming write above is the actual response
}

// LoremQuery is the documented query parameters for GET /v1/lorem.
type LoremQuery struct {
	Words int    `json:"words"`
	Seed  string `json:"seed,omitempty"`
}

// LoremResponse is the wire shape of GET /v1/lorem.
type LoremResponse struct {
	Words int    `json:"words"`
	Body  string `json:"body"`
}

const (
	// loremDefaultWords is the word count used when the caller omits
	// or sends a non-positive ?words= value.
	loremDefaultWords = 50
	// loremMaxWords caps the response word count so a caller can't
	// trigger an unbounded allocation by passing ?words=2147483647.
	// CodeQL's go/uncontrolled-allocation-size query specifically
	// recognizes the `min(n, const)` pattern; the older `if n > N { n = N }`
	// form does not break the taint flow even though it's runtime-safe.
	loremMaxWords = 5000
)

// loremDict is a small word bank for fake-Latin generation.
var loremDict = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing",
	"elit", "sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore",
	"et", "dolore", "magna", "aliqua", "enim", "ad", "minim", "veniam",
	"quis", "nostrud", "exercitation", "ullamco", "laboris", "nisi",
	"aliquip", "ex", "ea", "commodo", "consequat", "duis", "aute", "irure",
	"in", "reprehenderit", "voluptate", "velit", "esse", "cillum", "fugiat",
	"nulla", "pariatur", "excepteur", "sint", "occaecat", "cupidatat",
	"non", "proident", "sunt", "culpa", "qui", "officia", "deserunt",
	"mollit", "anim", "id", "est", "laborum",
}

func (g *Group) lorem(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n, _ := strconv.Atoi(q.Get("words"))
	if n <= 0 {
		n = loremDefaultWords
	}
	// min() (Go 1.21+) is the form CodeQL's go/uncontrolled-allocation-size
	// taint-flow query recognizes as a bound; replacing the prior
	// `if n > 5000 { n = 5000 }` clamp here is a CodeQL-shape change,
	// not a behavior change. See loremMaxWords doc for context.
	n = min(n, loremMaxWords)
	rng := newRand(q.Get("seed"))
	words := make([]string, n)
	for i := 0; i < n; i++ {
		words[i] = loremDict[rng.IntN(len(loremDict))]
	}
	if len(words) > 0 {
		first := words[0]
		words[0] = strings.ToUpper(first[:1]) + first[1:]
	}
	writeJSON(w, http.StatusOK, LoremResponse{
		Words: n,
		Body:  strings.Join(words, " ") + ".",
	})
}

// newRand returns a *rand.Rand seeded deterministically from seed; if seed
// is empty it returns one seeded from a non-deterministic source.
//
// math/rand/v2 is intentional here; these endpoints generate test fixtures
// and must be reproducible from a seed. crypto/rand would be wrong.
func newRand(seed string) *rand.Rand {
	if seed == "" {
		return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) // #nosec G404 -- non-crypto PRNG; test fixture
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	a := h.Sum64()
	h.Reset()
	_, _ = h.Write([]byte("salt:" + seed))
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
