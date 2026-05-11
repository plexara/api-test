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
	replayReq, err := http.NewRequestWithContext(r.Context(),
		ev.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("construct replay request: %w", err))
		return
	}

	// Copy captured headers but refuse to replay anything already
	// carrying our marker (loop guard).
	for k, vs := range payload.RequestHeaders {
		if strings.EqualFold(k, replayHeaderMarker) {
			writeError(w, http.StatusBadRequest,
				errors.New("captured request already carries the replay marker — refusing to replay a replay"))
			return
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
