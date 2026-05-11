package httpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/audit"
)

// seedEvents adds n synthetic events spanning [start, start+window) with
// rotating success/failure and ascending duration_ms. Used by every test
// in this file.
func seedEvents(t *testing.T, log *audit.MemoryLogger, start time.Time, window time.Duration, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration(i) * window / time.Duration(n))
		ev := audit.Event{
			ID:            "ev-" + itoa3(i),
			Timestamp:     ts,
			DurationMS:    int64(10 + i*5),
			Method:        []string{"GET", "POST"}[i%2],
			Path:          []string{"/v1/whoami", "/v1/echo"}[i%2],
			RouteName:     []string{"whoami", "echo_post"}[i%2],
			EndpointGroup: []string{"identity", "echo"}[i%2],
			Status:        []int{200, 200, 200, 500}[i%4],
			Success:       i%4 != 3,
			AuthType:      []string{"apikey", "bearer"}[i%2],
			UserEmail:     []string{"a@x", "b@x"}[i%2],
		}
		if err := log.Log(context.Background(), ev); err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}
}

func TestAuditTimeseries_Shape(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	seedEvents(t, log, start, 30*time.Minute, 60)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/portal/audit/timeseries?from="+start.Format(time.RFC3339)+
			"&to="+start.Add(30*time.Minute).Format(time.RFC3339)+
			"&bucket_seconds=300", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		BucketSeconds int `json:"bucket_seconds"`
		Buckets       []struct {
			Time          time.Time `json:"time"`
			Count         int       `json:"count"`
			Errors        int       `json:"errors"`
			AvgDurationMS float64   `json:"avg_duration_ms"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
	}
	if body.BucketSeconds != 300 {
		t.Errorf("bucket_seconds = %d, want 300", body.BucketSeconds)
	}
	// 30-minute window at 5-minute buckets = 6 buckets (plus the
	// boundary slot from the +1 in totalBuckets).
	if len(body.Buckets) != 7 {
		t.Errorf("got %d buckets, want 7 (30m/5m + boundary)", len(body.Buckets))
	}
	total := 0
	for _, b := range body.Buckets {
		total += b.Count
	}
	if total != 60 {
		t.Errorf("sum of bucket counts = %d, want 60", total)
	}
}

func TestAuditTimeseries_BucketTooSmall(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	// 24h window with bucket=1s → 86400 buckets, over the 10000 cap.
	from := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/portal/audit/timeseries?from="+from+"&to="+to+"&bucket_seconds=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestAuditBreakdown_ByEndpointGroup(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	// Start slightly inside the default 1h window so timeWindow's
	// later time.Now() call doesn't push the first event out.
	start := time.Now().UTC().Add(-50 * time.Minute)
	seedEvents(t, log, start, 40*time.Minute, 40)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/portal/audit/breakdown?dimension=endpoint_group", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		Dimension string `json:"dimension"`
		Groups    []struct {
			Key    string `json:"key"`
			Count  int    `json:"count"`
			Errors int    `json:"errors"`
		} `json:"groups"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Dimension != "endpoint_group" {
		t.Errorf("dimension = %q", body.Dimension)
	}
	keys := map[string]int{}
	for _, g := range body.Groups {
		keys[g.Key] = g.Count
	}
	if keys["identity"] != 20 || keys["echo"] != 20 {
		t.Errorf("group counts wrong: %v", keys)
	}
}

func TestAuditBreakdown_UnknownDimension(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/portal/audit/breakdown?dimension=nonsense", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestAuditBreakdown_ByStatus(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	start := time.Now().UTC().Add(-50 * time.Minute)
	seedEvents(t, log, start, 40*time.Minute, 40)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/portal/audit/breakdown?dimension=status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Groups []struct {
			Key    string `json:"key"`
			Count  int    `json:"count"`
			Errors int    `json:"errors"`
		} `json:"groups"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	got := map[string]int{}
	for _, g := range body.Groups {
		got[g.Key] = g.Count
	}
	// Seed rotates status across [200, 200, 200, 500] → 30 successes
	// at 200, 10 errors at 500.
	if got["200"] != 30 || got["500"] != 10 {
		t.Errorf("status breakdown wrong: %v", got)
	}
}

func TestAuditStats_Percentiles(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	start := time.Now().UTC().Add(-50 * time.Minute)
	seedEvents(t, log, start, 40*time.Minute, 20)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Total     int     `json:"total"`
		Errors    int     `json:"errors"`
		Success   int     `json:"success"`
		ErrorRate float64 `json:"error_rate"`
		P50MS     int64   `json:"p50_ms"`
		P95MS     int64   `json:"p95_ms"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 20 {
		t.Errorf("total = %d, want 20", body.Total)
	}
	if body.Errors+body.Success != body.Total {
		t.Errorf("success(%d)+errors(%d) != total(%d)", body.Success, body.Errors, body.Total)
	}
	// p50 should be lower than p95 with non-degenerate data.
	if body.P50MS >= body.P95MS {
		t.Errorf("p50 (%d) should be less than p95 (%d)", body.P50MS, body.P95MS)
	}
}

// TestAggregationsIgnoreLimitQueryParam guards against a regression
// where parseQueryFilter forwarded ?limit= and ?offset= into the
// underlying audit.Logger.Query, silently truncating the event set
// behind the aggregations and producing wrong totals/percentiles.
func TestAggregationsIgnoreLimitQueryParam(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	start := time.Now().UTC().Add(-50 * time.Minute)
	seedEvents(t, log, start, 40*time.Minute, 40)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	t.Run("stats", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/portal/audit/stats?limit=5", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var body struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.Total != 40 {
			t.Errorf("total = %d, want 40 (limit param should not truncate event scan)", body.Total)
		}
	})

	t.Run("breakdown", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/portal/audit/breakdown?dimension=endpoint_group&limit=5", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var body struct {
			Groups []struct {
				Key   string `json:"key"`
				Count int    `json:"count"`
			} `json:"groups"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		total := 0
		for _, g := range body.Groups {
			total += g.Count
		}
		if total != 40 {
			t.Errorf("group counts sum to %d, want 40 (limit param should not truncate event scan)", total)
		}
	})

	t.Run("breakdown_top_caps_groups_not_events", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/portal/audit/breakdown?dimension=status&top=1", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var body struct {
			Groups []struct {
				Key   string `json:"key"`
				Count int    `json:"count"`
			} `json:"groups"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if len(body.Groups) != 1 {
			t.Errorf("got %d groups, want 1 (top=1 caps output groups)", len(body.Groups))
		}
		// The one group returned should reflect counting all 40 events.
		// status=200 dominates (30 events).
		if body.Groups[0].Count != 30 {
			t.Errorf("top group count = %d, want 30 (top caps output, not scan)", body.Groups[0].Count)
		}
	})

	t.Run("timeseries", func(t *testing.T) {
		// Use bucket_seconds=300 (5m) over the seed window so we get
		// a small, predictable response shape.
		from := start.Format(time.RFC3339)
		to := start.Add(40 * time.Minute).Format(time.RFC3339)
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/portal/audit/timeseries?bucket_seconds=300&limit=5&from="+from+"&to="+to, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var body struct {
			Buckets []struct {
				Count int `json:"count"`
			} `json:"buckets"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		total := 0
		for _, b := range body.Buckets {
			total += b.Count
		}
		if total != 40 {
			t.Errorf("bucket counts sum to %d, want 40 (limit param should not truncate)", total)
		}
	})
}

func TestPercentileEmpty(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile(nil) = %d, want 0", got)
	}
}

func TestPercentile_NearestRank(t *testing.T) {
	sorted := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		p    float64
		want int64
	}{
		{0.00, 10},
		{0.50, 50},
		{0.95, 90},
		{0.99, 90}, // (10-1)*0.99 = 8.91 → idx 8 → sorted[8] = 90
		{1.00, 100},
	}
	for _, c := range cases {
		if got := percentile(sorted, c.p); got != c.want {
			t.Errorf("percentile(%.2f) = %d, want %d", c.p, got, c.want)
		}
	}
}

func TestAuditMeta_AdvertisesEnabledFeatures(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/meta", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var body struct {
		Features map[string]bool `json:"features"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, k := range []string{"timeseries", "breakdown", "stats"} {
		if !body.Features[k] {
			t.Errorf("feature %s should be true (just shipped)", k)
		}
	}
}

func itoa3(n int) string {
	const digits = "0123456789"
	return string([]byte{
		digits[(n/100)%10],
		digits[(n/10)%10],
		digits[n%10],
	})
}
