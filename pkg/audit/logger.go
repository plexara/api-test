package audit

import (
	"context"
	"time"
)

// Logger writes events and queries them back for the portal. Loggers that
// capture the audit_payloads sibling row implement PayloadLogger for the
// detail-fetch path; basic implementations (memory, noop) only hold the
// indexable summary.
//
// M2 surface is intentionally narrow: Log + Query + Count cover the
// integration-test needs. M3 expands with TimeSeries/Breakdown/Stats for
// the portal dashboard, Subscribe for the SSE live tail, and Stream for
// the NDJSON export.
type Logger interface {
	Log(ctx context.Context, ev Event) error
	Query(ctx context.Context, f QueryFilter) ([]Event, error)
	Count(ctx context.Context, f QueryFilter) (int64, error)
}

// PayloadLogger is the optional capability for detail fetch. Stores that
// persist the audit_payloads sibling row implement it; consumers type-
// assert for it before calling GetPayload.
type PayloadLogger interface {
	GetPayload(ctx context.Context, eventID string) (*Payload, error)
}

// MaxQueryLimit is the largest LIMIT any backend will honor on a single
// SELECT. Larger values get silently reduced.
const MaxQueryLimit = 1000

// QueryFilter narrows audit_events results. Filters are AND-combined.
//
// M2 implements the time, route, status, user, session, and search
// fields. JSONFilters/HasKeys (payload-row introspection) land in M3.
type QueryFilter struct {
	From      time.Time
	To        time.Time
	Method    string
	Path      string
	RouteName string
	UserID    string
	SessionID string
	EventID   string // exact-match on audit_events.id (single-event fetch)
	Status    int    // exact match on response status; 0 means "any"
	Success   *bool
	Search    string
	Limit     int
	Offset    int
	OrderDesc bool
}
