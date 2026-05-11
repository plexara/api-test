package httpsrv

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/plexara/api-test/pkg/audit"
)

const (
	// auditStreamPollInterval is how often the SSE handler polls the
	// audit Logger for new events. 1s is fast enough for a dashboard
	// live-tail and slow enough to avoid hammering the DB.
	auditStreamPollInterval = 1 * time.Second

	// auditStreamHeartbeatInterval is how often a comment line is
	// flushed when no events have arrived. Keeps intermediaries from
	// closing the connection as idle.
	auditStreamHeartbeatInterval = 15 * time.Second

	// auditStreamMaxPagesPerTick bounds the inner page loop so a
	// pathological backlog can't pin the handler indefinitely.
	// Combined with audit.MaxQueryLimit this caps each tick at
	// 10*1000 = 10k events, which is more than the dashboard could
	// usefully render even if every one of them arrived in a single
	// second.
	auditStreamMaxPagesPerTick = 10

	// auditExportLimit caps NDJSON export at 100k events. The audit
	// Logger's MaxQueryLimit is 1000 per call; we paginate via
	// repeated Query() calls and stop here as defense against runaway.
	auditExportLimit = 100_000
)

// auditStream serves a long-lived Server-Sent Events stream of new
// audit_events. Polls every auditStreamPollInterval; emits a heartbeat
// comment every auditStreamHeartbeatInterval if the queue is empty.
// Honors r.Context() cancellation so a closed connection ends the loop
// promptly.
func (p *PortalAPI) auditStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this writer"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	// Establish the high-water mark at connection time so we don't
	// flood the client with historical events. The client gets the
	// existing /audit/events endpoint for historical replay.
	lastSeen := time.Now().UTC()

	pollTicker := time.NewTicker(auditStreamPollInterval)
	defer pollTicker.Stop()
	heartbeatTicker := time.NewTicker(auditStreamHeartbeatInterval)
	defer heartbeatTicker.Stop()

	// Send an initial comment so the EventSource onopen handler fires.
	_, _ = fmt.Fprintln(w, ": stream open")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-heartbeatTicker.C:
			_, _ = fmt.Fprintln(w, ": heartbeat")
			flusher.Flush()

		case <-pollTicker.C:
			now := time.Now().UTC()
			// Page within the tick: Query orders DESC and clamps at
			// MaxQueryLimit. A burst exceeding the clamp would
			// silently drop the older half if we advanced lastSeen
			// directly to `now` without paging. Walk the upper bound
			// down to the oldest emitted event's timestamp; dedup
			// the boundary by event ID since Logger boundaries are
			// inclusive on both ends.
			cursor := now
			seen := map[string]bool{}
			emittedAny := false
			for page := 0; page < auditStreamMaxPagesPerTick; page++ {
				events, err := p.audit.Query(r.Context(), audit.QueryFilter{
					From:  lastSeen,
					To:    cursor,
					Limit: audit.MaxQueryLimit,
				})
				if err != nil {
					_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape(err.Error()))
					flusher.Flush()
					break
				}
				freshThisPage := 0
				for i := range events {
					ev := events[i]
					// Skip events at exactly lastSeen to avoid
					// duplicating the boundary on the next tick.
					if !ev.Timestamp.After(lastSeen) {
						continue
					}
					if seen[ev.ID] {
						continue
					}
					seen[ev.ID] = true
					payload, _ := json.Marshal(ev)
					_, _ = fmt.Fprintf(w, "id: %s\nevent: audit\ndata: %s\n\n",
						ev.ID, payload)
					emittedAny = true
					freshThisPage++
				}
				if len(events) < audit.MaxQueryLimit {
					break
				}
				if freshThisPage == 0 {
					// Boundary stuck — every event on this page
					// was already in `seen` (a burst of events
					// at the same timestamp; the cursor can't
					// advance past a tied-ts page without an
					// ID-tiebreaker filter on the Logger). Emit
					// an explicit saturated frame so the SPA can
					// surface that events were dropped rather
					// than silently losing them.
					_, _ = fmt.Fprintf(w, "event: saturated\ndata: {\"reason\":\"tied_timestamps\",\"emitted_this_tick\":%d}\n\n",
						len(seen))
					break
				}
				// DESC order: oldest is at len-1; advance the
				// upper bound (inclusive) to it. The seen map
				// suppresses re-emission on the boundary.
				cursor = events[len(events)-1].Timestamp
			}
			if emittedAny {
				flusher.Flush()
			}
			lastSeen = now
		}
	}
}

// auditExportNDJSON streams matching events as newline-delimited JSON.
// Paginates through the Logger via repeated Query() calls because the
// per-call MaxQueryLimit is small. Stops at auditExportLimit as a
// safety cap.
//
// Concurrent inserts: the handler pins the upper bound on the time
// window at entry. Events with ts > entry-time are filtered out of
// every page, so offset pagination stays stable even when new audit
// events arrive during the export. Without this pin, a row inserted
// between pages would shift every subsequent row one position later
// in the DESC ordering, duplicating the boundary row of each page.
func (p *PortalAPI) auditExportNDJSON(w http.ResponseWriter, r *http.Request) {
	f := parseQueryFilter(r)
	// Force MaxQueryLimit per page so we don't accidentally honor a
	// caller-supplied small ?limit= as a per-page hint.
	f.Limit = audit.MaxQueryLimit
	f.Offset = 0
	// Pin the upper bound to NOW so concurrent inserts don't shift
	// the offset window mid-export.
	if f.To.IsZero() || f.To.After(time.Now().UTC()) {
		f.To = time.Now().UTC()
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-events.ndjson"`)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	written := 0
	enc := json.NewEncoder(w)
	for written < auditExportLimit {
		if r.Context().Err() != nil {
			return
		}
		events, err := p.audit.Query(r.Context(), f)
		if err != nil {
			// Mid-stream error: write a final JSON object documenting
			// the error so consumers can spot the partial export.
			_ = enc.Encode(map[string]any{"_export_error": err.Error()})
			return
		}
		if len(events) == 0 {
			return
		}
		for _, ev := range events {
			_ = enc.Encode(ev)
			written++
			if written >= auditExportLimit {
				_ = enc.Encode(map[string]any{
					"_export_truncated": true,
					"_limit":            auditExportLimit,
				})
				return
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
		// Advance: ask for the next page. The audit Query orders
		// newest-first by default; we advance by offset to walk
		// the whole window.
		f.Offset += len(events)
		// If a backend returns fewer than the requested limit,
		// we've reached the end.
		if len(events) < f.Limit {
			return
		}
	}
}

// jsonEscape returns a JSON-safe quoted version of s, minus the outer
// quotes, suitable for embedding in an SSE `data:` payload.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	if len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}
