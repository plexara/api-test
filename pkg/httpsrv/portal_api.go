package httpsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/plexara/api-test/pkg/apikeys"
	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/build"
	"github.com/plexara/api-test/pkg/config"
	"github.com/plexara/api-test/pkg/endpoints"
)

// PortalAPI bundles the portal handlers under /api/v1/portal/*.
//
// State separation: read-only handlers (me, server, endpoints, audit/events,
// dashboard, wellknown, keys list) are mounted via the standard auth
// middleware; mutating handlers (keys create/delete) are wrapped in the
// CSRF header check on top of auth so a forged <form> POST cannot reach
// them via SameSite=Lax cookies alone.
type PortalAPI struct {
	cfg          *config.Config
	registry     *endpoints.Registry
	audit        audit.Logger
	keys         *apikeys.Store // nil if config.APIKeys.DB.Enabled=false
	replayTarget http.Handler   // nil disables /audit/replay and /tryit
}

// NewPortalAPI returns the API. keys may be nil when the DB-backed key store
// is not enabled; the keys handlers respond 503 in that case.
func NewPortalAPI(
	cfg *config.Config,
	registry *endpoints.Registry,
	auditLog audit.Logger,
	keys *apikeys.Store,
) *PortalAPI {
	return &PortalAPI{cfg: cfg, registry: registry, audit: auditLog, keys: keys}
}

// WithReplayTarget enables the audit replay and Try-It endpoints,
// dispatching requests through h. h is the mux *before* the wrapping
// access-log / request-id / browser-redirect / CORS layers — that way
// dispatched requests go straight to the audit middleware and the
// registered routes without recursing through the portal API itself.
// Returns the receiver so the composition can chain.
func (p *PortalAPI) WithReplayTarget(h http.Handler) *PortalAPI {
	p.replayTarget = h
	return p
}

// WithDispatchTarget is an alias for WithReplayTarget kept for call
// sites added before the field rename. Use WithReplayTarget for new
// code; this shim will be removed once external callers update.
func (p *PortalAPI) WithDispatchTarget(h http.Handler) *PortalAPI {
	return p.WithReplayTarget(h)
}

// Mount adds every endpoint behind the supplied auth middleware.
//
// As a side effect, the supplied mux becomes the default replay/dispatch
// target for /audit/replay and /tryit if WithReplayTarget hasn't already
// been called. The mux at the time those endpoints are invoked has every
// /v1/* route mounted on it (BuildMux mounts those before calling
// PortalAPI.Mount), so dispatched/replayed requests go straight into
// the audit-wrapped endpoint handlers and show up as new audit rows.
func (p *PortalAPI) Mount(mux *http.ServeMux, mw func(http.Handler) http.Handler) {
	if p.replayTarget == nil {
		p.replayTarget = mux
	}
	wrap := func(h http.Handler) http.Handler { return mw(requireCSRFHeader(h)) }

	mux.Handle("GET /api/v1/portal/me", mw(http.HandlerFunc(p.me)))
	mux.Handle("GET /api/v1/portal/server", mw(http.HandlerFunc(p.server)))
	mux.Handle("GET /api/v1/portal/wellknown", mw(http.HandlerFunc(p.wellknown)))
	mux.Handle("GET /api/v1/portal/dashboard", mw(http.HandlerFunc(p.dashboard)))

	mux.Handle("GET /api/v1/portal/endpoints", mw(http.HandlerFunc(p.endpoints)))
	mux.Handle("GET /api/v1/portal/endpoints/{name}", mw(http.HandlerFunc(p.endpointDetail)))

	mux.Handle("GET /api/v1/portal/audit/meta", mw(http.HandlerFunc(p.auditMeta)))
	mux.Handle("GET /api/v1/portal/audit/events", mw(http.HandlerFunc(p.auditEvents)))
	mux.Handle("GET /api/v1/portal/audit/events/{id}", mw(http.HandlerFunc(p.auditEventDetail)))
	mux.Handle("GET /api/v1/portal/audit/timeseries", mw(http.HandlerFunc(p.auditTimeseries)))
	mux.Handle("GET /api/v1/portal/audit/breakdown", mw(http.HandlerFunc(p.auditBreakdown)))
	mux.Handle("GET /api/v1/portal/audit/stats", mw(http.HandlerFunc(p.auditStats)))
	mux.Handle("GET /api/v1/portal/audit/stream", mw(http.HandlerFunc(p.auditStream)))
	mux.Handle("GET /api/v1/portal/audit/export.ndjson", mw(http.HandlerFunc(p.auditExportNDJSON)))
	mux.Handle("POST /api/v1/portal/audit/replay/{id}", wrap(http.HandlerFunc(p.auditReplay)))

	mux.Handle("POST /api/v1/portal/tryit/{group}/{route}", wrap(http.HandlerFunc(p.tryIt)))

	mux.Handle("GET /api/v1/admin/keys", mw(http.HandlerFunc(p.listKeys)))
	mux.Handle("POST /api/v1/admin/keys", wrap(http.HandlerFunc(p.createKey)))
	mux.Handle("DELETE /api/v1/admin/keys/{name}", wrap(http.HandlerFunc(p.deleteKey)))
}

func (p *PortalAPI) me(w http.ResponseWriter, r *http.Request) {
	id := auth.GetIdentity(r.Context())
	writeJSON(w, http.StatusOK, id)
}

func (p *PortalAPI) server(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, sanitizedConfig(p.cfg))
}

func (p *PortalAPI) wellknown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protected_resource_url": ProtectedResourceMetadataURL(p.cfg),
		"authorization_server":   p.cfg.OIDC.Issuer,
		"oidc_enabled":           p.cfg.OIDC.Enabled,
		"audience":               p.cfg.OIDC.Audience,
		"api_endpoint":           strings.TrimRight(p.cfg.Server.BaseURL, "/") + "/v1/",
	})
}

func (p *PortalAPI) endpoints(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoints": p.registry.All(),
	})
}

func (p *PortalAPI) endpointDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, m := range p.registry.All() {
		if m.Name == name {
			writeJSON(w, http.StatusOK, m)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("endpoint %q not found", name))
}

// auditMeta exposes the filter contract surface the SPA's audit filter
// editor uses. The features map tells the SPA which optional panels to
// enable; the SPA disables matching panels when a flag is false.
func (p *PortalAPI) auditMeta(w http.ResponseWriter, _ *http.Request) {
	// Replay needs three things: a wired target handler, an audit
	// Logger that persists payloads, and (in practice) capture being
	// enabled at the time the original event was recorded. We can
	// check the first two here; the third is a runtime property the
	// handler surfaces as 404. SPA can only check the static flag.
	_, payloadCapable := p.audit.(audit.PayloadLogger)
	writeJSON(w, http.StatusOK, map[string]any{
		"filters": []string{"from", "to", "method", "path", "route_name", "status", "user", "session", "success", "q"},
		"features": map[string]bool{
			"timeseries": true,
			"breakdown":  true,
			"stats":      true,
			"stream":     true,
			"export":     true,
			"replay":     p.replayTarget != nil && payloadCapable,
		},
	})
}

func (p *PortalAPI) auditEvents(w http.ResponseWriter, r *http.Request) {
	f := parseQueryFilter(r)
	if f.Limit == 0 {
		f.Limit = 50
	}
	events, err := p.audit.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	total, _ := p.audit.Count(r.Context(), f)
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"total":  total,
		"limit":  f.Limit,
		"offset": f.Offset,
	})
}

// auditEventDetail returns one event identified by ID, plus its
// audit_payloads sibling row (when the configured logger persists payloads).
// Validates the path value as a UUID before any backend lookup; the
// canonicalized form is what gets logged so gosec's taint analysis doesn't
// have to trust the raw path bytes.
func (p *PortalAPI) auditEventDetail(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	parsed, err := uuid.Parse(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("event id is not a valid uuid"))
		return
	}
	id := parsed.String()

	events, err := p.audit.Query(r.Context(), audit.QueryFilter{Limit: 1, EventID: id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(events) == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("event %q not found", id))
		return
	}
	ev := events[0]
	ev.Payload = nil
	if pl, ok := p.audit.(audit.PayloadLogger); ok {
		if payload, perr := pl.GetPayload(r.Context(), id); perr == nil {
			ev.Payload = payload
		}
	}
	writeJSON(w, http.StatusOK, ev)
}

// dashboard computes a small inline summary from Query: total + success
// counts in the last hour, plus a list of the 20 most recent events. When
// the audit Logger gains a Stats / TimeSeries / Breakdown surface in M3+,
// move the heavy lifting into the backend.
func (p *PortalAPI) dashboard(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	from := now.Add(-1 * time.Hour)
	f := audit.QueryFilter{From: from, To: now}
	total, _ := p.audit.Count(r.Context(), f)
	succ := true
	successFilter := f
	successFilter.Success = &succ
	successCount, _ := p.audit.Count(r.Context(), successFilter)
	recent, _ := p.audit.Query(r.Context(), audit.QueryFilter{From: from, To: now, Limit: 20})
	writeJSON(w, http.StatusOK, map[string]any{
		"window_from":   from,
		"window_to":     now,
		"total":         total,
		"success_count": successCount,
		"recent":        recent,
	})
}

func (p *PortalAPI) listKeys(w http.ResponseWriter, r *http.Request) {
	if p.keys == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("api keys store not enabled"))
		return
	}
	keys, err := p.keys.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if keys == nil {
		keys = []apikeys.Key{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

type createKeyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"` // RFC3339; "" = no expiry
}

func (p *PortalAPI) createKey(w http.ResponseWriter, r *http.Request) {
	if p.keys == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("api keys store not enabled"))
		return
	}
	var body createKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name required"))
		return
	}
	var expires *time.Time
	if body.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("expires_at: %w", err))
			return
		}
		expires = &t
	}
	createdBy := ""
	if id := auth.GetIdentity(r.Context()); id != nil {
		createdBy = firstNonEmpty(id.Email, id.Subject, id.Name)
	}
	created, err := p.keys.Create(r.Context(), body.Name, body.Description, createdBy, expires)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (p *PortalAPI) deleteKey(w http.ResponseWriter, r *http.Request) {
	if p.keys == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("api keys store not enabled"))
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name required"))
		return
	}
	if err := p.keys.Delete(r.Context(), name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sanitizedConfig returns a config with secrets replaced by "[redacted]".
//
// Important: every nested slice that holds secrets is deep-copied before
// mutating. A naive `c := *cfg` only copies the slice header, so mutating
// entries through the local copy would corrupt the live in-memory config
// and leak the redacted form back to the inbound auth chain.
func sanitizedConfig(cfg *config.Config) map[string]any {
	c := *cfg
	c.Portal.CookieSecret = redactIfSet(c.Portal.CookieSecret)
	c.OIDC.ClientSecret = redactIfSet(c.OIDC.ClientSecret)
	c.Plexara.Register.AuthHeader = redactIfSet(c.Plexara.Register.AuthHeader)
	if len(c.APIKeys.File) > 0 {
		clone := make([]config.FileAPIKey, len(c.APIKeys.File))
		copy(clone, c.APIKeys.File)
		for i := range clone {
			clone[i].Key = redactIfSet(clone[i].Key)
		}
		c.APIKeys.File = clone
	}
	if len(c.Bearer.Tokens) > 0 {
		clone := make([]config.FileBearerToken, len(c.Bearer.Tokens))
		copy(clone, c.Bearer.Tokens)
		for i := range clone {
			clone[i].Token = redactIfSet(clone[i].Token)
		}
		c.Bearer.Tokens = clone
	}
	c.Database.URL = redactDatabaseURL(c.Database.URL)
	return map[string]any{
		"version": build.Version,
		"commit":  build.Commit,
		"date":    build.Date,
		"config":  c,
	}
}

func redactIfSet(v string) string {
	if v == "" {
		return ""
	}
	return "[redacted]"
}

// redactDatabaseURL redacts the password portion of either form pgxpool
// accepts. pgx's accepted password-bearing keywords are `password` AND
// `sslpassword` (the SSL client-key passphrase); pgx's keyword/value
// separators include vertical tab `\v` which Go's `\s` does NOT match.
// Both forms below account for those.
//
//   - URL form (postgres://user:pass@host/db?...): use stdlib
//     (*url.URL).Redacted() to redact the userinfo password, but blanket
//     -redact the whole string if the query string carries `password=` or
//     `sslpassword=` (pgx accepts these as connection settings;
//     Redacted() does not touch the query).
//   - libpq DSN form (host=localhost user=api password=secret dbname=...):
//     if the string contains any `password=` or `sslpassword=` keyword
//     (case-insensitive, libpq separator set, allowing whitespace around
//     `=`), the whole string is replaced with "[redacted]". Intentionally
//     blanket: hand-rolled value parsers for libpq quoting / escaping /
//     whitespace have repeatedly leaked tails on malformed-but-accepted
//     inputs (rounds 4–5 of pre-commit review). Loud loss of host
//     visibility on the Config page is acceptable; silent leak is not.
//
// The goal is "no plaintext password reaches the SPA via
// /api/v1/portal/server", whichever form the operator configured.
// pgxSepClass is pgx's keyword/value separator set per
// pgconn.parseKeywordValueSettings (pgconn/config.go) — `[\t\n\v\f\r ]`.
// Critically includes `\v` (vertical tab) which Go's regex `\s` does NOT
// match. Used as the leading anchor and the around-`=` whitespace class
// in dsnPasswordKeywordRE so pgx-accepted DSNs cannot bypass detection
// via any separator variant.
const pgxSepClass = `[\t\n\v\f\r ]`

var dsnPasswordKeywordRE = regexp.MustCompile(`(?i)(?:^|` + pgxSepClass + `)(?:ssl)?password` + pgxSepClass + `*=`)

func redactDatabaseURL(s string) string {
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" &&
		(strings.EqualFold(u.Scheme, "postgres") || strings.EqualFold(u.Scheme, "postgresql")) {
		q := u.Query()
		// Key-presence rather than first-value: a URL like
		// `?password=&password=actualsecret` would have q.Get("password")=="",
		// but the second value still reaches pgx as the connection password.
		if _, ok := q["password"]; ok {
			return "[redacted]"
		}
		if _, ok := q["sslpassword"]; ok {
			return "[redacted]"
		}
		return u.Redacted()
	}
	if dsnPasswordKeywordRE.MatchString(s) {
		return "[redacted]"
	}
	return s
}

func parseQueryFilter(r *http.Request) audit.QueryFilter {
	q := r.URL.Query()
	f := audit.QueryFilter{}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = t
		}
	}
	f.Method = q.Get("method")
	f.Path = q.Get("path")
	f.RouteName = q.Get("route_name")
	f.UserID = q.Get("user")
	f.SessionID = q.Get("session")
	f.Search = q.Get("q")
	if v := q.Get("status"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Status = n
		}
	}
	switch q.Get("success") {
	case "true":
		yes := true
		f.Success = &yes
	case "false":
		no := false
		f.Success = &no
	}
	if v, _ := strconv.Atoi(q.Get("limit")); v > 0 {
		f.Limit = v
	}
	if v, _ := strconv.Atoi(q.Get("offset")); v > 0 {
		f.Offset = v
	}
	return f
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}
