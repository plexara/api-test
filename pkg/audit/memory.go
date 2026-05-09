package audit

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// MemoryLogger is an in-memory Logger used by tests. Implements
// PayloadLogger so the in-memory test path mirrors the Postgres path's
// detail-fetch contract.
type MemoryLogger struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryLogger returns an empty logger.
func NewMemoryLogger() *MemoryLogger { return &MemoryLogger{} }

// Log appends the event. Auto-assigns ev.ID when empty so test fixtures
// see a stable id without setting one explicitly (matches Postgres
// store's behavior).
func (m *MemoryLogger) Log(_ context.Context, ev Event) error {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
	return nil
}

// Query returns matching events. ts DESC, id ASC tiebreaker.
//
// Pagination semantics match the Postgres store: Offset is applied first
// (clamped to zero on negative input), then Limit (defaulting to 100,
// clamped to MaxQueryLimit).
func (m *MemoryLogger) Query(_ context.Context, f QueryFilter) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.matchAll(f)

	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Count returns the number of matching events. Ignores Limit/Offset.
func (m *MemoryLogger) Count(_ context.Context, f QueryFilter) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.matchAll(f))), nil
}

// GetPayload returns the in-memory event's Payload pointer. Mirrors the
// PayloadLogger contract used by the portal (M3+).
func (m *MemoryLogger) GetPayload(_ context.Context, eventID string) (*Payload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.ID == eventID {
			return ev.Payload, nil
		}
	}
	return nil, nil
}

// Snapshot returns a copy of all events in insertion order, for assertions.
func (m *MemoryLogger) Snapshot() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// matchAll applies every QueryFilter predicate EXCEPT Limit/Offset.
// Caller must hold m.mu.
func (m *MemoryLogger) matchAll(f QueryFilter) []Event {
	out := make([]Event, 0, len(m.events))
	for _, ev := range m.events {
		if !matchesFilter(ev, f) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].Timestamp.After(out[j].Timestamp)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func matchesFilter(ev Event, f QueryFilter) bool {
	if f.EventID != "" && ev.ID != f.EventID {
		return false
	}
	if f.Method != "" && ev.Method != f.Method {
		return false
	}
	if f.Path != "" && ev.Path != f.Path {
		return false
	}
	if f.RouteName != "" && ev.RouteName != f.RouteName {
		return false
	}
	if f.UserID != "" && ev.UserSubject != f.UserID {
		return false
	}
	if f.SessionID != "" && ev.SessionID != f.SessionID {
		return false
	}
	if f.Status != 0 && ev.Status != f.Status {
		return false
	}
	if !f.From.IsZero() && ev.Timestamp.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && ev.Timestamp.After(f.To) {
		return false
	}
	if f.Success != nil && ev.Success != *f.Success {
		return false
	}
	if f.Search != "" {
		// Mirrors the Postgres store's `path ILIKE %q% OR error_message
		// ILIKE %q%` predicate (case-insensitive substring on either).
		needle := strings.ToLower(f.Search)
		if !strings.Contains(strings.ToLower(ev.Path), needle) &&
			!strings.Contains(strings.ToLower(ev.ErrorMessage), needle) {
			return false
		}
	}
	return true
}
