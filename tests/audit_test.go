//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/plexara/api-test/pkg/audit"
	auditpg "github.com/plexara/api-test/pkg/audit/postgres"
)

// TestIntegration_AuditCapture verifies that a successful API call lands
// as a row in audit_events with the right identity, status, route, and
// duration, and that the audit_payloads sibling row carries redacted
// headers + the response body.
func TestIntegration_AuditCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pgURL := startPostgres(ctx, t)
	url, _ := boot(t, pgURL)

	client := authenticatedClient()
	resp, err := client.Get(url + "/v1/whoami")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whoami status %d", resp.StatusCode)
	}

	// Audit pipeline is async-buffered (AsyncLogger). Poll until visible.
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	t.Cleanup(pool.Close)
	store := auditpg.New(pool)

	events := pollEvents(t, store, ctx, 1, 3*time.Second)
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	ev := events[0]
	if ev.Method != "GET" || ev.Path != "/v1/whoami" {
		t.Errorf("method/path = %s %s", ev.Method, ev.Path)
	}
	if ev.Status != 200 || !ev.Success {
		t.Errorf("status=%d success=%v", ev.Status, ev.Success)
	}
	if ev.AuthType != "apikey" || ev.UserSubject != "intkey" {
		t.Errorf("identity not captured: %+v", ev)
	}
	if ev.RouteName == "" {
		t.Errorf("route_name empty (registry didn't tag route)")
	}
	if ev.DurationMS < 0 {
		t.Errorf("duration %d", ev.DurationMS)
	}

	// Payload row: headers redacted, response body present.
	pl, err := store.GetPayload(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetPayload: %v", err)
	}
	if pl == nil {
		t.Fatal("payload nil")
	}
	if v := pl.RequestHeaders["X-Api-Key"]; len(v) != 1 || v[0] != "[redacted]" {
		t.Errorf("X-API-Key not redacted in payload: %v", v)
	}
	var body map[string]any
	if err := json.Unmarshal(pl.ResponseBody, &body); err != nil {
		t.Fatalf("response body not JSON: %v\n%q", err, pl.ResponseBody)
	}
	if body["auth_type"] != "apikey" {
		t.Errorf("payload body auth_type = %v", body["auth_type"])
	}
	if pl.ResponseContentType != "application/json" {
		t.Errorf("response content type = %q", pl.ResponseContentType)
	}
}

// TestIntegration_AuditFailureMarked verifies that a 5xx response writes
// success=false in the audit row.
func TestIntegration_AuditFailureMarked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pgURL := startPostgres(ctx, t)
	url, _ := boot(t, pgURL)

	client := authenticatedClient()
	resp, err := client.Get(url + "/v1/status/503")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := auditpg.New(pool)

	events := pollEvents(t, store, ctx, 1, 3*time.Second)
	var failure *audit.Event
	for i := range events {
		if events[i].Path == "/v1/status/503" {
			failure = &events[i]
			break
		}
	}
	if failure == nil {
		t.Fatalf("no audit row for /v1/status/503 in %d events", len(events))
	}
	if failure.Status != 503 || failure.Success {
		t.Errorf("status=%d success=%v", failure.Status, failure.Success)
	}
}

// TestIntegration_HealthzNotAudited confirms /healthz doesn't produce an
// audit row (it sits outside the endpoint middleware stack).
func TestIntegration_HealthzNotAudited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pgURL := startPostgres(ctx, t)
	url, _ := boot(t, pgURL)

	for i := 0; i < 3; i++ {
		resp, err := http.Get(url + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	// Give the (nonexistent) audit pipeline a moment.
	time.Sleep(200 * time.Millisecond)

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := auditpg.New(pool)
	cnt, err := store.Count(ctx, audit.QueryFilter{Path: "/healthz"})
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Errorf("/healthz audited %d times, want 0", cnt)
	}
}

// TestIntegration_QueryFilters checks that the Postgres store honors the
// QueryFilter predicates used by the (M3) portal API.
func TestIntegration_QueryFilters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pgURL := startPostgres(ctx, t)
	url, _ := boot(t, pgURL)

	client := authenticatedClient()
	for _, path := range []string{
		"/v1/whoami",
		"/v1/status/200",
		"/v1/status/500",
		"/v1/whoami",
	} {
		resp, err := client.Get(url + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := auditpg.New(pool)

	// Wait until at least 4 audited events.
	pollEvents(t, store, ctx, 4, 3*time.Second)

	// Filter by status.
	cnt, err := store.Count(ctx, audit.QueryFilter{Status: 500})
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("status=500 count = %d, want 1", cnt)
	}

	// Filter by user.
	cnt, err = store.Count(ctx, audit.QueryFilter{UserID: "intkey"})
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 4 {
		t.Errorf("user=intkey count = %d, want 4", cnt)
	}

	// Search across path.
	cnt, err = store.Count(ctx, audit.QueryFilter{Search: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 2 {
		t.Errorf("search=whoami count = %d, want 2", cnt)
	}
}

func pollEvents(t *testing.T, store *auditpg.Store, ctx context.Context, atLeast int, timeout time.Duration) []audit.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var events []audit.Event
	var lastErr error
	for time.Now().Before(deadline) {
		events, lastErr = store.Query(ctx, audit.QueryFilter{Limit: 100})
		if lastErr == nil && len(events) >= atLeast {
			return events
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("audit query: %v", lastErr)
	}
	return events
}

// Sanity check that strings package is referenced (audit_payloads test
// uses Contains in the response-body assertion).
var _ = strings.Contains
