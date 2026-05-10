package audit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryLogger_LogAndQuery(t *testing.T) {
	ml := NewMemoryLogger()
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		ev := Event{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Method:    "GET",
			Path:      "/v1/whoami",
			Status:    200,
			Success:   true,
		}
		if err := ml.Log(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := ml.Query(ctx, QueryFilter{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 {
		t.Errorf("got %d events, want 5", len(evs))
	}
	// Newest first.
	if !evs[0].Timestamp.After(evs[4].Timestamp) {
		t.Errorf("ordering: %v then %v", evs[0].Timestamp, evs[4].Timestamp)
	}
	for _, ev := range evs {
		if ev.ID == "" {
			t.Error("Log did not auto-assign ID")
		}
	}
}

func TestMemoryLogger_Filters(t *testing.T) {
	ml := NewMemoryLogger()
	ctx := context.Background()

	for _, ev := range []Event{
		{Method: "GET", Path: "/a", Status: 200, Success: true, UserSubject: "alice"},
		{Method: "POST", Path: "/b", Status: 500, Success: false, UserSubject: "bob"},
		{Method: "GET", Path: "/a", Status: 200, Success: true, UserSubject: "bob"},
	} {
		_ = ml.Log(ctx, ev)
	}

	cnt, _ := ml.Count(ctx, QueryFilter{Method: "GET"})
	if cnt != 2 {
		t.Errorf("GET count = %d want 2", cnt)
	}
	cnt, _ = ml.Count(ctx, QueryFilter{UserID: "bob"})
	if cnt != 2 {
		t.Errorf("bob count = %d want 2", cnt)
	}
	cnt, _ = ml.Count(ctx, QueryFilter{Status: 500})
	if cnt != 1 {
		t.Errorf("status=500 count = %d want 1", cnt)
	}
	tr := false
	cnt, _ = ml.Count(ctx, QueryFilter{Success: &tr})
	if cnt != 1 {
		t.Errorf("success=false count = %d want 1", cnt)
	}
}

func TestMemoryLogger_GetPayload(t *testing.T) {
	ml := NewMemoryLogger()
	ctx := context.Background()
	ev := Event{
		Method:  "GET",
		Path:    "/v1/x",
		Status:  200,
		Payload: &Payload{RequestContentType: "application/json"},
	}
	if err := ml.Log(ctx, ev); err != nil {
		t.Fatal(err)
	}
	stored := ml.Snapshot()
	id := stored[0].ID
	got, err := ml.GetPayload(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("payload nil")
	}
	if got.RequestContentType != "application/json" {
		t.Errorf("content type = %q", got.RequestContentType)
	}
	miss, err := ml.GetPayload(ctx, "no-such-id")
	if err != nil {
		t.Fatal(err)
	}
	if miss != nil {
		t.Errorf("expected nil for missing id, got %+v", miss)
	}
}

func TestMemoryLogger_SearchAndOffset(t *testing.T) {
	ml := NewMemoryLogger()
	ctx := context.Background()
	for _, ev := range []Event{
		{Method: "GET", Path: "/v1/whoami", Status: 200, Success: true},
		{Method: "GET", Path: "/v1/sized", Status: 200, Success: true},
		{Method: "GET", Path: "/v1/whoami", Status: 401, Success: false, ErrorMessage: "missing credential"},
		{Method: "GET", Path: "/v1/status/500", Status: 500, Success: false},
	} {
		_ = ml.Log(ctx, ev)
	}

	// Search matches path substring (case-insensitive).
	cnt, _ := ml.Count(ctx, QueryFilter{Search: "whoami"})
	if cnt != 2 {
		t.Errorf("search=whoami count = %d, want 2", cnt)
	}
	cnt, _ = ml.Count(ctx, QueryFilter{Search: "WHOAMI"})
	if cnt != 2 {
		t.Errorf("search case-insensitive: %d want 2", cnt)
	}
	// Search also matches error_message.
	cnt, _ = ml.Count(ctx, QueryFilter{Search: "missing"})
	if cnt != 1 {
		t.Errorf("search=missing (error_message) count = %d, want 1", cnt)
	}

	// Offset skips matching rows.
	all, _ := ml.Query(ctx, QueryFilter{})
	if len(all) != 4 {
		t.Fatalf("baseline events = %d, want 4", len(all))
	}
	page1, _ := ml.Query(ctx, QueryFilter{Limit: 2, Offset: 0})
	page2, _ := ml.Query(ctx, QueryFilter{Limit: 2, Offset: 2})
	if len(page1) != 2 || len(page2) != 2 {
		t.Fatalf("pages: %d, %d (want 2,2)", len(page1), len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Errorf("Offset didn't skip: page2[0]=%s same as page1[0]", page2[0].ID)
	}
	// Offset past the end returns empty.
	tail, _ := ml.Query(ctx, QueryFilter{Limit: 2, Offset: 999})
	if len(tail) != 0 {
		t.Errorf("offset past end returned %d events", len(tail))
	}
	// Negative Offset is clamped.
	clamped, _ := ml.Query(ctx, QueryFilter{Limit: 2, Offset: -5})
	if len(clamped) != 2 {
		t.Errorf("negative offset returned %d, want 2", len(clamped))
	}
}

func TestMemoryLogger_LimitClamped(t *testing.T) {
	ml := NewMemoryLogger()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = ml.Log(ctx, Event{Method: "GET", Path: "/x", Status: 200, Success: true})
	}
	evs, _ := ml.Query(ctx, QueryFilter{Limit: 2})
	if len(evs) != 2 {
		t.Errorf("limit=2 returned %d", len(evs))
	}
	evs, _ = ml.Query(ctx, QueryFilter{Limit: 10000})
	if len(evs) != 5 {
		t.Errorf("limit too high returned %d, want 5", len(evs))
	}
}
