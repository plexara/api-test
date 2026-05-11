package httpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/audit"
)

func TestAuditExportNDJSON_StreamsAllEvents(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	// Timestamps in the past — that's where real audit events live.
	// auditExportNDJSON pins To=now() at entry, so events with ts in
	// the future would be filtered out (covered by another test).
	start := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < 5; i++ {
		_ = log.Log(context.Background(), audit.Event{
			ID:        "e" + itoa3(i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Method:    "GET",
			Path:      "/v1/echo",
			Status:    200,
			Success:   true,
		})
	}

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/export.ndjson", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}
	if disp := w.Header().Get("Content-Disposition"); !strings.Contains(disp, "audit-events.ndjson") {
		t.Errorf("Content-Disposition = %q", disp)
	}

	lines := []string{}
	for _, line := range strings.Split(strings.TrimRight(w.Body.String(), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5: body=%q", len(lines), w.Body.String())
	}
	for i, line := range lines {
		var ev audit.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("line %d not JSON: %v (%q)", i, err, line)
		}
	}
}

func TestAuditExportNDJSON_EmptyExport(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/export.ndjson", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("empty export should have empty body, got %q", w.Body.String())
	}
}

func TestAuditExportNDJSON_IgnoresClientLimit(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	for i := 0; i < 10; i++ {
		_ = log.Log(context.Background(), audit.Event{
			ID:        "e" + itoa3(i),
			Timestamp: time.Now().UTC().Add(-1 * time.Hour).Add(time.Duration(i) * time.Minute),
		})
	}
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/export.ndjson?limit=3", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	lineCount := 0
	for _, line := range strings.Split(strings.TrimRight(w.Body.String(), "\n"), "\n") {
		if line != "" {
			lineCount++
		}
	}
	if lineCount != 10 {
		t.Errorf("got %d lines, want 10 (?limit= must not cap export)", lineCount)
	}
}

func TestAuditStream_HeadersAndOpens(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	// Open the stream with a short-lived context so it exits promptly
	// after we've observed the open frame.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), ": stream open") {
		t.Errorf("expected ': stream open' in body, got %q", w.Body.String())
	}
}

func TestAuditStream_DeliversNewEvent(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	// Need a real http server so the SSE flush actually goes through
	// the network. httptest.NewRecorder doesn't drive a goroutine.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Open the stream in a goroutine that closes its response body
	// when this test ends.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/portal/audit/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream open: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Give the handler a tick to record lastSeen, then write an
	// event with a timestamp the poll loop will pick up.
	time.Sleep(100 * time.Millisecond)
	_ = log.Log(context.Background(), audit.Event{
		ID:        "stream-test-1",
		Timestamp: time.Now().UTC().Add(50 * time.Millisecond),
		Method:    "GET",
		Path:      "/v1/whoami",
		Status:    200,
		Success:   true,
	})

	// Read until we see the event frame or hit the context timeout.
	deadline := time.Now().Add(3 * time.Second)
	buf := make([]byte, 4096)
	collected := strings.Builder{}
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			collected.WriteString(string(buf[:n]))
			if strings.Contains(collected.String(), "event: audit") &&
				strings.Contains(collected.String(), "stream-test-1") {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("did not observe audit event in stream within deadline. got:\n%s", collected.String())
}

// itoa3 is defined in portal_audit_aggregations_test.go; reused here.

// TestAuditExportNDJSON_PinsToNow regresses the offset-pagination
// duplicate-rows bug. With To pinned at handler entry, events whose
// timestamps land AFTER the pin must not appear in the output.
//
// We exercise the pin synchronously: seed past events, then seed a
// future-timestamped event before invoking the handler. The handler's
// `f.To = time.Now()` pin happens at entry, AFTER the future event
// exists in the log; if the pin code is deleted, the future event
// slips through and the test fails.
func TestAuditExportNDJSON_PinsToNow(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	past := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < 5; i++ {
		_ = log.Log(context.Background(), audit.Event{
			ID:        "old" + itoa3(i),
			Timestamp: past.Add(time.Duration(i) * time.Minute),
		})
	}
	// Synchronously add a far-future event BEFORE the handler runs,
	// so the pin actually has something to exclude.
	_ = log.Log(context.Background(), audit.Event{
		ID:        "future-event",
		Timestamp: time.Now().UTC().Add(time.Hour),
	})

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/export.ndjson", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "future-event") {
		t.Errorf("future-ts event must be filtered by pinned To, but appeared in body:\n%s", body)
	}
	lineCount := 0
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line != "" {
			lineCount++
		}
	}
	if lineCount != 5 {
		t.Errorf("got %d export lines, want 5 (the future event must be excluded)", lineCount)
	}
}

// TestAuditStream_BurstExceedsBatchSize regresses the silent-event-loss
// bug. Seeding more events than the per-page Query limit in one tick
// must not drop the oldest events.
//
// Memory-backend specific: we drive a tight burst before opening the
// stream, then open it. The handler establishes lastSeen = now() so
// any events with ts < now are excluded — that's the documented
// "open-time baseline" semantics. To exercise within-tick paging we
// instead emit events AFTER the stream opens whose timestamps are
// well-bunched and exceed the MaxQueryLimit by a few.
func TestAuditStream_PagesWithinTickWithoutDroppingEvents(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/portal/audit/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream open: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	time.Sleep(100 * time.Millisecond)

	// Inject 1500 events spread over a tiny window. Each ID is
	// distinct so we can verify all of them surface in the stream.
	const N = 1500
	emitStart := time.Now().UTC().Add(50 * time.Millisecond)
	for i := 0; i < N; i++ {
		_ = log.Log(context.Background(), audit.Event{
			ID:        "burst-" + itoa4(i),
			Timestamp: emitStart.Add(time.Duration(i) * time.Microsecond),
			Method:    "GET",
			Path:      "/v1/whoami",
		})
	}

	// Collect for ~3 seconds (enough for 3 poll ticks) then assert
	// every burst ID appears at least once.
	deadline := time.Now().Add(3500 * time.Millisecond)
	buf := make([]byte, 64*1024)
	collected := strings.Builder{}
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			collected.WriteString(string(buf[:n]))
		}
		if err != nil {
			break
		}
	}
	body := collected.String()
	missing := 0
	for i := 0; i < N; i++ {
		if !strings.Contains(body, "burst-"+itoa4(i)) {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d/%d burst events missing from stream — within-tick paging dropped them", missing, N)
	}
}

// TestAuditStream_EmitsSaturatedFrameOnTiedTimestamps regresses the
// tied-ts data-loss edge case. When more than MaxQueryLimit events
// share a single timestamp, the cursor in the within-tick page loop
// can't advance past them (no ID tiebreaker filter on the Logger).
// The handler emits an explicit `event: saturated` SSE frame so the
// loss is observable rather than silent.
func TestAuditStream_EmitsSaturatedFrameOnTiedTimestamps(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/portal/audit/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream open: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	time.Sleep(100 * time.Millisecond)

	// 1100 events all sharing the same timestamp — exceeds the
	// per-page MaxQueryLimit (1000), so the cursor stalls.
	tied := time.Now().UTC().Add(50 * time.Millisecond)
	for i := 0; i < 1100; i++ {
		_ = log.Log(context.Background(), audit.Event{
			ID:        "tied-" + itoa4(i),
			Timestamp: tied,
		})
	}

	deadline := time.Now().Add(3 * time.Second)
	buf := make([]byte, 64*1024)
	collected := strings.Builder{}
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			collected.WriteString(string(buf[:n]))
			if strings.Contains(collected.String(), "event: saturated") {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("did not observe 'event: saturated' frame after 1100 tied-ts events. got:\n%s",
		collected.String())
}

// itoa4 returns a 4-digit zero-padded decimal string.
func itoa4(n int) string {
	const digits = "0123456789"
	return string([]byte{
		digits[(n/1000)%10],
		digits[(n/100)%10],
		digits[(n/10)%10],
		digits[n%10],
	})
}

func TestJSONEscape_HandlesQuotesAndControlBytes(t *testing.T) {
	cases := map[string]string{
		`plain`:       `plain`,
		`"quoted"`:    `\"quoted\"`,
		"line\nbreak": `line\nbreak`,
		"tab\there":   `tab\there`,
		"":            ``,
	}
	for in, want := range cases {
		if got := jsonEscape(in); got != want {
			t.Errorf("jsonEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
