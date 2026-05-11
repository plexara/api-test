// Package security provides probe endpoints designed to LOOK like
// dangerous gateway targets so the gateway can pattern-match them and
// refuse to forward. The handlers themselves are inert — they never
// fetch a URL, never escalate privileges, never emit smuggling-shaped
// responses. The job is to give a gateway tester a stable set of
// "the gateway should have refused this" probes.
//
// Probes:
//
//   - GET  /v1/security/admin/secret        — privileged-looking path.
//   - GET  /v1/security/fetch?url=...       — SSRF-shape input.
//   - GET  /v1/security/big-headers         — 32 KiB of response headers.
//   - POST /v1/security/redirect-to?url=... — open-redirect shape.
//   - GET  /v1/security/control-chars?q=... — control chars in query.
//
// Each handler returns a small JSON body so a tester can observe both
// the gateway's refusal (404/451/etc from the gateway) and the api-test
// response shape if the gateway forwards regardless.
package security

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/plexara/api-test/pkg/endpoints"
)

const (
	groupName = "security"

	// bigHeaderCount + bigHeaderValueLen yields ~32 KiB of response
	// headers; gateway header-size caps should reject this if the
	// gateway enforces RFC 7230 §3.2.5 sensibly.
	bigHeaderCount    = 64
	bigHeaderValueLen = 512
)

// Group implements endpoints.Endpoints for the security group.
type Group struct{}

// New returns a Group.
func New() *Group { return &Group{} }

// Name implements endpoints.Endpoints.
func (Group) Name() string { return groupName }

// Routes implements endpoints.Endpoints.
func (Group) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{
			Name:         "security_admin",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/security/admin/secret",
			Description:  "Privileged-looking path. A gateway with path filtering should refuse to forward.",
			ResponseBody: (*AdminResponse)(nil),
		},
		{
			Name:         "security_fetch",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/security/fetch",
			Description:  "SSRF-shape input (?url=...). The handler does NOT fetch; it echoes the URL asked. A gateway with SSRF heuristics should refuse to forward when url points at localhost, link-local addresses, or non-allowlisted hosts.",
			QueryParams:  (*FetchQuery)(nil),
			ResponseBody: (*FetchResponse)(nil),
		},
		{
			Name:         "security_big_headers",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/security/big-headers",
			Description:  "Emits ~32 KiB of response headers. A gateway with response-header-size limits should reject or rewrite.",
			ResponseBody: (*BigHeadersResponse)(nil),
		},
		{
			Name:         "security_redirect_to",
			Group:        groupName,
			Method:       http.MethodPost,
			Path:         "/v1/security/redirect-to",
			Description:  "Open-redirect shape: returns 200 + X-Would-Redirect-To header carrying the caller's ?url. NOT a real Location header (and NOT a 3xx status) — the probe is inert by design. Gateway URL filters that scan arbitrary response headers can still pattern-match.",
			QueryParams:  (*RedirectQuery)(nil),
			ResponseBody: (*RedirectResponse)(nil),
		},
		{
			Name:         "security_control_chars",
			Group:        groupName,
			Method:       http.MethodGet,
			Path:         "/v1/security/control-chars",
			Description:  "Echoes ?q= back in the response body with any control characters intact. A gateway that sanitizes by replacing or stripping control bytes will produce a different body than the one written here.",
			QueryParams:  (*ControlCharsQuery)(nil),
			ResponseBody: (*ControlCharsResponse)(nil),
		},
	}
}

// Mount implements endpoints.Endpoints.
func (g *Group) Mount(mux *http.ServeMux, mw endpoints.Middleware) {
	mux.Handle("GET /v1/security/admin/secret", mw(http.HandlerFunc(g.admin)))
	mux.Handle("GET /v1/security/fetch", mw(http.HandlerFunc(g.fetch)))
	mux.Handle("GET /v1/security/big-headers", mw(http.HandlerFunc(g.bigHeaders)))
	mux.Handle("POST /v1/security/redirect-to", mw(http.HandlerFunc(g.redirectTo)))
	mux.Handle("GET /v1/security/control-chars", mw(http.HandlerFunc(g.controlChars)))
}

// AdminResponse is the body of /v1/security/admin/secret.
type AdminResponse struct {
	Message string `json:"message"`
}

// FetchQuery is the query shape for /v1/security/fetch.
type FetchQuery struct {
	URL string `json:"url"`
}

// FetchResponse is the body of /v1/security/fetch.
type FetchResponse struct {
	AskedFor         string `json:"asked_for"`
	WouldHaveFetched bool   `json:"would_have_fetched"`
}

// BigHeadersResponse is the body of /v1/security/big-headers.
type BigHeadersResponse struct {
	HeaderCount int `json:"header_count"`
	HeaderBytes int `json:"header_bytes"`
}

// RedirectQuery is the query shape for /v1/security/redirect-to.
type RedirectQuery struct {
	URL string `json:"url"`
}

// RedirectResponse is the body of /v1/security/redirect-to.
type RedirectResponse struct {
	WouldRedirectTo string `json:"would_redirect_to"`
}

// ControlCharsQuery is the query shape for /v1/security/control-chars.
type ControlCharsQuery struct {
	Q string `json:"q"`
}

// ControlCharsResponse is the body of /v1/security/control-chars.
type ControlCharsResponse struct {
	Q          string `json:"q"`
	ByteCount  int    `json:"byte_count"`
	HasControl bool   `json:"has_control"`
}

func (g *Group) admin(w http.ResponseWriter, _ *http.Request) {
	writeJSONOK(w, AdminResponse{
		Message: "This endpoint exists to be a probe target. A correctly-configured gateway should have refused to forward this request before it reached api-test.",
	})
}

func (g *Group) fetch(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeBadRequest(w, "url must be provided (this endpoint is a probe; it does not actually fetch)")
		return
	}
	writeJSONOK(w, FetchResponse{
		AskedFor:         url,
		WouldHaveFetched: false,
	})
}

func (g *Group) bigHeaders(w http.ResponseWriter, _ *http.Request) {
	value := strings.Repeat("A", bigHeaderValueLen)
	totalBytes := 0
	for i := 0; i < bigHeaderCount; i++ {
		name := "X-Big-Probe-" + strconv.Itoa(i)
		w.Header().Set(name, value)
		totalBytes += len(name) + len(value) + 4 // ": " + CRLF
	}
	writeJSONOK(w, BigHeadersResponse{
		HeaderCount: bigHeaderCount,
		HeaderBytes: totalBytes,
	})
}

func (g *Group) redirectTo(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeBadRequest(w, "url must be provided")
		return
	}
	// We deliberately use a custom X-Would-Redirect-To header rather
	// than Location, and return 200 rather than 3xx. Both choices keep
	// the response unambiguously inert: browsers don't auto-follow,
	// and CodeQL's go/unvalidated-url-redirection rule isn't triggered
	// because we never sink user input into a redirect-recognized
	// header. Gateways that scan response headers can still pattern-
	// match the X-Would-Redirect-To shape to flag suspect requests.
	w.Header().Set("X-Would-Redirect-To", url)
	writeJSONOK(w, RedirectResponse{WouldRedirectTo: url})
}

func (g *Group) controlChars(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	writeJSONOK(w, ControlCharsResponse{
		Q:          q,
		ByteCount:  len(q),
		HasControl: containsControl(q),
	})
}

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// writeJSONOK writes body as application/json with a 200 status. Every
// probe in this package returns 200 (with body distinguishing the
// probe semantics); using a fixed-status helper keeps the lint pass
// happy and makes call sites read at a glance.
func writeJSONOK(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

func writeBadRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
