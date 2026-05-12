package httpsrv

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/auth/inbound"
)

// replayHeaderMarker is the header attached to every replayed request
// so a replay can't infinite-loop into itself. The audit middleware
// also reads this header to populate Payload.ReplayedFrom on the new
// event row, so the constant lives in pkg/audit (audit.ReplayHeaderName)
// — this name is a local alias to keep the call sites short.
const replayHeaderMarker = audit.ReplayHeaderName

// replayMaxBodyBytes caps the request body we'll re-send and the
// response body we'll capture so a hostile captured payload can't
// allocate large amounts of memory at replay time.
const replayMaxBodyBytes = 1 << 20 // 1 MiB

// redactedReplayHeaders are credential-carrying header names whose
// captured values are guaranteed to be the "[redacted]" sentinel
// written by audit.SanitizeHeaders. Re-emitting them would put a
// non-empty value on the wire that httpmw.Identity would treat as a
// real credential, defeating the portal-identity bypass. The replay
// path carries identity through context, so dropping them is safe.
//
// TODO: paired with the same gap in pkg/httpmw/identity.go — if a
// deployment customizes APIKeysConfig.HeaderName, the captured
// redaction sentinel under the custom name will leak back into the
// replayed request. Fix when the chain gains a self-describing
// HasCredential surface.
var redactedReplayHeaders = map[string]struct{}{
	"authorization": {},
	"x-api-key":     {},
	"cookie":        {},
}

func isRedactedCredentialHeader(name string) bool {
	_, ok := redactedReplayHeaders[strings.ToLower(name)]
	return ok
}

// isRedactedCredentialQuery matches the ?api_key= form the apikey
// authenticator also accepts; audit.SanitizeQuery redacts it identically.
func isRedactedCredentialQuery(name string) bool {
	return strings.EqualFold(name, "api_key")
}

// auditReplay re-issues a captured request through the local mux.
// Requires the audit Logger to implement PayloadLogger (so we can
// reconstruct headers + body) and the composition layer to have wired
// a replayTarget via WithReplayTarget. Refuses to replay non-/v1/*
// paths and refuses to replay anything that already carries the loop
// marker.
//
// Returns:
//
//	{
//	  "replayed_from":  "<uuid>",
//	  "status":         200,
//	  "headers":        {...},
//	  "body":           "<string>",
//	  "body_truncated": false
//	}
func (p *PortalAPI) auditReplay(w http.ResponseWriter, r *http.Request) {
	if p.replayTarget == nil {
		writeError(w, http.StatusServiceUnavailable,
			errors.New("replay disabled: composition did not supply a target handler"))
		return
	}

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

	if !strings.HasPrefix(ev.Path, "/v1/") {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("path %q is not replay-eligible (only /v1/* routes can be replayed)", ev.Path))
		return
	}

	pl, ok := p.audit.(audit.PayloadLogger)
	if !ok {
		writeError(w, http.StatusServiceUnavailable,
			errors.New("audit logger does not persist payloads — replay requires PayloadLogger"))
		return
	}
	payload, perr := pl.GetPayload(r.Context(), id)
	if perr != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Errorf("fetch payload for event %q: %w", id, perr))
		return
	}
	if payload == nil {
		writeError(w, http.StatusNotFound,
			fmt.Errorf("payload for event %q not captured (audit.capture_payloads must be enabled when the event was recorded)", id))
		return
	}

	// Build the replay URL: keep the captured path; restore the
	// captured query parameters. Path comes from the audit log of a
	// previously-routed request, so url.Parse can't fail — but we
	// surface the (impossible) error anyway.
	u, err := url.Parse(ev.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("parse captured path: %w", err))
		return
	}
	if len(payload.RequestQuery) > 0 {
		values := url.Values{}
		for k, vs := range payload.RequestQuery {
			if isRedactedCredentialQuery(k) {
				continue
			}
			for _, v := range vs {
				values.Add(k, v)
			}
		}
		u.RawQuery = values.Encode()
	}

	body := payload.RequestBody
	if len(body) > replayMaxBodyBytes {
		body = body[:replayMaxBodyBytes]
	}

	// The captured request's Authorization / X-API-Key are persisted as
	// "[redacted]", so replaying them verbatim would 401 the call. Carry
	// the operator's portal-resolved identity into the replayed request's
	// context — same trust model as Try-It dispatch.
	replayCtx := r.Context()
	if portalID := auth.GetIdentity(replayCtx); portalID != nil {
		replayCtx = inbound.WithIdentity(replayCtx, portalIdentityToInbound(portalID))
	}

	replayReq, err := http.NewRequestWithContext(replayCtx,
		ev.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("construct replay request: %w", err))
		return
	}

	// Copy captured headers but refuse to replay anything already
	// carrying our marker (loop guard). Skip credential headers — the
	// audit middleware persists their values as "[redacted]", so
	// re-emitting them would put a redaction sentinel on the wire that
	// httpmw.Identity treats as a real credential and then fails to
	// validate. Identity flows through the dispatch context instead.
	for k, vs := range payload.RequestHeaders {
		if strings.EqualFold(k, replayHeaderMarker) {
			writeError(w, http.StatusBadRequest,
				errors.New("captured request already carries the replay marker — refusing to replay a replay"))
			return
		}
		if isRedactedCredentialHeader(k) {
			continue
		}
		for _, v := range vs {
			replayReq.Header.Add(k, v)
		}
	}
	replayReq.Header.Set(replayHeaderMarker, id)
	if payload.RequestContentType != "" && replayReq.Header.Get("Content-Type") == "" {
		replayReq.Header.Set("Content-Type", payload.RequestContentType)
	}

	rec := newCapResponseWriter(replayMaxBodyBytes)
	p.replayTarget.ServeHTTP(rec, replayReq)

	writeJSON(w, http.StatusOK, map[string]any{
		"replayed_from":  id,
		"status":         rec.status,
		"headers":        rec.headers,
		"body":           rec.body.String(),
		"body_truncated": rec.truncated,
	})
}

// capResponseWriter buffers up to maxBytes of response body, then
// silently drops further writes. Avoids the unbounded buffering of
// httptest.NewRecorder when a replayed endpoint emits a multi-MiB
// body (e.g. /v1/sized?bytes=33554432 → 32 MiB).
type capResponseWriter struct {
	headers   http.Header
	status    int
	body      bytes.Buffer
	maxBytes  int
	truncated bool
	wroteHdr  bool
}

func newCapResponseWriter(maxBytes int) *capResponseWriter {
	return &capResponseWriter{
		headers:  http.Header{},
		status:   http.StatusOK,
		maxBytes: maxBytes,
	}
}

func (c *capResponseWriter) Header() http.Header { return c.headers }

func (c *capResponseWriter) WriteHeader(status int) {
	if c.wroteHdr {
		return
	}
	c.status = status
	c.wroteHdr = true
}

func (c *capResponseWriter) Write(p []byte) (int, error) {
	if !c.wroteHdr {
		c.WriteHeader(http.StatusOK)
	}
	remaining := c.maxBytes - c.body.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		c.body.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	c.body.Write(p)
	return len(p), nil
}
