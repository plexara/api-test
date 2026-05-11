package httpsrv

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/plexara/api-test/pkg/audit"
)

// auditTimeseriesHandler returns bucketed counts and latency for the
// requested time window. Aggregation runs in Go against the audit
// Logger's Query results — fine for a test fixture's traffic. Bucket
// granularity defaults to 60s; clamped to [1, 3600].
//
// Response shape:
//
//	{
//	  "from":         "2026-05-10T00:00:00Z",
//	  "to":           "2026-05-10T01:00:00Z",
//	  "bucket_seconds": 60,
//	  "buckets":      [{
//	      "time":        "2026-05-10T00:00:00Z",
//	      "count":       42,
//	      "errors":      3,
//	      "avg_duration_ms": 18.3
//	  }]
//	}
func (p *PortalAPI) auditTimeseries(w http.ResponseWriter, r *http.Request) {
	f, from, to := timeWindow(r)
	bucket := bucketSeconds(r)

	events, err := p.audit.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	bucketDur := time.Duration(bucket) * time.Second
	totalBuckets := int(to.Sub(from)/bucketDur) + 1
	if totalBuckets < 1 {
		totalBuckets = 1
	}
	if totalBuckets > 10_000 {
		// Cap UI-generated runaway requests; the SPA's resolution
		// picker never crosses this even at 1-second buckets over
		// a 24-hour window.
		writeError(w, http.StatusBadRequest, fmt.Errorf("bucket_seconds %d × window produces %d buckets (max 10000); pick a coarser bucket or narrower window", bucket, totalBuckets))
		return
	}

	type aggregate struct {
		Count      int
		Errors     int
		DurationMS int64
	}
	bucketsAgg := make([]aggregate, totalBuckets)
	for _, ev := range events {
		if ev.Timestamp.Before(from) || !ev.Timestamp.Before(to) {
			continue
		}
		idx := int(ev.Timestamp.Sub(from) / bucketDur)
		if idx < 0 || idx >= totalBuckets {
			continue
		}
		bucketsAgg[idx].Count++
		bucketsAgg[idx].DurationMS += ev.DurationMS
		if !ev.Success {
			bucketsAgg[idx].Errors++
		}
	}

	type bucketOut struct {
		Time          time.Time `json:"time"`
		Count         int       `json:"count"`
		Errors        int       `json:"errors"`
		AvgDurationMS float64   `json:"avg_duration_ms"`
	}
	out := make([]bucketOut, 0, totalBuckets)
	for i, agg := range bucketsAgg {
		var avg float64
		if agg.Count > 0 {
			avg = float64(agg.DurationMS) / float64(agg.Count)
		}
		out = append(out, bucketOut{
			Time:          from.Add(time.Duration(i) * bucketDur).UTC(),
			Count:         agg.Count,
			Errors:        agg.Errors,
			AvgDurationMS: round1(avg),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":           from.UTC(),
		"to":             to.UTC(),
		"bucket_seconds": bucket,
		"buckets":        out,
	})
}

// auditBreakdownHandler groups events by one dimension and reports
// count + errors + avg latency per group. Dimensions: endpoint_group,
// route_name, status, auth_type, user. Limited to the top N groups
// (default 20).
func (p *PortalAPI) auditBreakdown(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	if dimension == "" {
		dimension = "endpoint_group"
	}
	keyFn, ok := breakdownKeyFn(dimension)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown dimension %q; valid: endpoint_group, route_name, status, auth_type, user", dimension))
		return
	}
	// `top` caps the number of groups returned, not the number of
	// events scanned. Distinct from `?limit=` (events-list pagination)
	// so the two semantics don't collide.
	top := 20
	if v, _ := strconv.Atoi(r.URL.Query().Get("top")); v > 0 && v <= 200 {
		top = v
	}

	f, from, to := timeWindow(r)
	events, err := p.audit.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type aggregate struct {
		Count      int
		Errors     int
		DurationMS int64
	}
	grouped := map[string]*aggregate{}
	for _, ev := range events {
		k := keyFn(ev)
		agg := grouped[k]
		if agg == nil {
			agg = &aggregate{}
			grouped[k] = agg
		}
		agg.Count++
		agg.DurationMS += ev.DurationMS
		if !ev.Success {
			agg.Errors++
		}
	}

	type groupOut struct {
		Key           string  `json:"key"`
		Count         int     `json:"count"`
		Errors        int     `json:"errors"`
		AvgDurationMS float64 `json:"avg_duration_ms"`
	}
	out := make([]groupOut, 0, len(grouped))
	for k, agg := range grouped {
		var avg float64
		if agg.Count > 0 {
			avg = float64(agg.DurationMS) / float64(agg.Count)
		}
		out = append(out, groupOut{
			Key:           k,
			Count:         agg.Count,
			Errors:        agg.Errors,
			AvgDurationMS: round1(avg),
		})
	}
	// Sort by count desc, then key asc for stability.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > top {
		out = out[:top]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":      from.UTC(),
		"to":        to.UTC(),
		"dimension": dimension,
		"groups":    out,
	})
}

// auditStatsHandler returns totals plus p50/p95 latency for the time
// window.
func (p *PortalAPI) auditStats(w http.ResponseWriter, r *http.Request) {
	f, from, to := timeWindow(r)

	events, err := p.audit.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	durations := make([]int64, 0, len(events))
	total := 0
	errs := 0
	for _, ev := range events {
		total++
		if !ev.Success {
			errs++
		}
		durations = append(durations, ev.DurationMS)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var errorRate float64
	if total > 0 {
		errorRate = float64(errs) / float64(total)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":       from.UTC(),
		"to":         to.UTC(),
		"total":      total,
		"errors":     errs,
		"success":    total - errs,
		"error_rate": round3(errorRate),
		"p50_ms":     percentile(durations, 0.50),
		"p95_ms":     percentile(durations, 0.95),
		"p99_ms":     percentile(durations, 0.99),
	})
}

// timeWindow returns the parsed filter plus the resolved window.
// Defaults: to = now, from = 1h ago.
//
// The Logger's Query honors [From, To] inclusive on both ends; the
// in-handler filters in auditTimeseries use a half-open [from, to)
// semantic for bucket assignment so an event at exactly `to` doesn't
// inflate a +1 boundary slot. The two differ only on the single
// boundary timestamp, which is measure-zero in real traffic.
//
// Limit/Offset from parseQueryFilter are intentionally overridden:
// aggregation endpoints have no per-page pagination, so the events-
// list ?limit=/?offset= would otherwise silently truncate the event
// set and yield wrong totals/percentiles.
func timeWindow(r *http.Request) (audit.QueryFilter, time.Time, time.Time) {
	f := parseQueryFilter(r)
	now := time.Now().UTC()
	to := f.To
	if to.IsZero() {
		to = now
	}
	from := f.From
	if from.IsZero() {
		from = to.Add(-1 * time.Hour)
	}
	if from.After(to) {
		from, to = to, from
	}
	f.From = from
	f.To = to
	f.Limit = audit.MaxQueryLimit
	f.Offset = 0
	return f, from, to
}

// bucketSeconds extracts ?bucket_seconds= clamped to [1, 3600]; default 60.
func bucketSeconds(r *http.Request) int {
	v, _ := strconv.Atoi(r.URL.Query().Get("bucket_seconds"))
	if v <= 0 {
		return 60
	}
	if v > 3600 {
		return 3600
	}
	return v
}

// breakdownKeyFn returns the per-event key function for the named
// dimension. The boolean reports validity.
func breakdownKeyFn(dimension string) (func(audit.Event) string, bool) {
	switch dimension {
	case "endpoint_group":
		return func(ev audit.Event) string {
			if ev.EndpointGroup == "" {
				return "(unknown)"
			}
			return ev.EndpointGroup
		}, true
	case "route_name":
		return func(ev audit.Event) string {
			if ev.RouteName == "" {
				return "(unknown)"
			}
			return ev.RouteName
		}, true
	case "status":
		return func(ev audit.Event) string { return strconv.Itoa(ev.Status) }, true
	case "auth_type":
		return func(ev audit.Event) string {
			if ev.AuthType == "" {
				return "(unauthenticated)"
			}
			return ev.AuthType
		}, true
	case "user":
		return func(ev audit.Event) string {
			if ev.UserEmail != "" {
				return ev.UserEmail
			}
			if ev.UserSubject != "" {
				return ev.UserSubject
			}
			if ev.APIKeyName != "" {
				return "key:" + ev.APIKeyName
			}
			return "(anonymous)"
		}, true
	}
	return nil, false
}

// percentile returns the value at the requested percentile of a sorted
// slice. Returns 0 for an empty slice. Uses nearest-rank (no
// interpolation) — simple and adequate for the dashboard.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func round3(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}
