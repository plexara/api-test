// Package audit defines the audit event shape and the Logger interface for
// api-test's HTTP request/response audit log.
//
// Event captures the indexable summary of one inbound HTTP request; the
// sibling Payload struct (joined 1:1 by ID) carries the full request and
// response envelope (headers, body, query). The two-table layout keeps the
// summary row small for time-range queries while letting operators drill
// into the full envelope on demand from the portal.
package audit

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// ReplayHeaderName is the header attached to replayed requests so the
// audit middleware can populate Payload.ReplayedFrom on the new event
// row. Lives in this package so both the portal handler that sets it
// and the middleware that reads it share one source of truth.
const ReplayHeaderName = "X-Plexara-Replay-From"

// Event is the indexable summary written to audit_events.
type Event struct {
	ID            string    `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	DurationMS    int64     `json:"duration_ms"`
	RequestID     string    `json:"request_id,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	UserSubject   string    `json:"user_subject,omitempty"`
	UserEmail     string    `json:"user_email,omitempty"`
	AuthType      string    `json:"auth_type,omitempty"`
	APIKeyName    string    `json:"api_key_name,omitempty"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	RouteName     string    `json:"route_name,omitempty"`
	EndpointGroup string    `json:"endpoint_group,omitempty"`
	Status        int       `json:"status"`
	BytesIn       int       `json:"bytes_in"`
	BytesOut      int       `json:"bytes_out"`
	Success       bool      `json:"success"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	ErrorCategory string    `json:"error_category,omitempty"`
	RemoteAddr    string    `json:"remote_addr,omitempty"`
	UserAgent     string    `json:"user_agent,omitempty"`

	// Payload, when non-nil, is the full request/response envelope. Written
	// to audit_payloads in the same transaction as the summary. Nil means
	// "no detail captured" (capture disabled, or the event predates capture).
	Payload *Payload `json:"payload,omitempty"`
}

// Payload is the full HTTP request/response envelope joined 1:1 with an
// Event by ID. Each side carries a byte size and a truncation flag so
// operators can tell whether they're looking at the whole body or a
// capped prefix.
type Payload struct {
	// Request side
	RequestHeaders     map[string][]string `json:"request_headers,omitempty"`
	RequestQuery       map[string][]string `json:"request_query,omitempty"`
	RequestContentType string              `json:"request_content_type,omitempty"`
	RequestBody        []byte              `json:"-"`
	RequestSizeBytes   int                 `json:"request_size_bytes,omitempty"`
	RequestTruncated   bool                `json:"request_truncated,omitempty"`
	RequestRemoteAddr  string              `json:"request_remote_addr,omitempty"`

	// Response side
	ResponseHeaders     map[string][]string `json:"response_headers,omitempty"`
	ResponseContentType string              `json:"response_content_type,omitempty"`
	ResponseBody        []byte              `json:"-"`
	ResponseSizeBytes   int                 `json:"response_size_bytes,omitempty"`
	ResponseTruncated   bool                `json:"response_truncated,omitempty"`

	// ReplayedFrom links a replayed call back to the original event's ID.
	// Set by the portal replay endpoint (M3+).
	ReplayedFrom string `json:"replayed_from,omitempty"`
}

// MarshalJSON renders Payload so the portal SPA and any human reader see
// request/response bodies as utf-8 strings, not the base64 dump Go's
// default []byte encoder produces. When a body isn't valid utf-8 we fall
// back to base64 and flag it via a sibling `_encoding` field so callers
// can decode unambiguously.
func (p Payload) MarshalJSON() ([]byte, error) {
	type alias Payload
	out := struct {
		alias
		RequestBody          string `json:"request_body,omitempty"`
		RequestBodyEncoding  string `json:"request_body_encoding,omitempty"`
		ResponseBody         string `json:"response_body,omitempty"`
		ResponseBodyEncoding string `json:"response_body_encoding,omitempty"`
	}{alias: alias(p)}

	if len(p.RequestBody) > 0 {
		if utf8.Valid(p.RequestBody) {
			out.RequestBody = string(p.RequestBody)
		} else {
			out.RequestBody = base64.StdEncoding.EncodeToString(p.RequestBody)
			out.RequestBodyEncoding = "base64"
		}
	}
	if len(p.ResponseBody) > 0 {
		if utf8.Valid(p.ResponseBody) {
			out.ResponseBody = string(p.ResponseBody)
		} else {
			out.ResponseBody = base64.StdEncoding.EncodeToString(p.ResponseBody)
			out.ResponseBodyEncoding = "base64"
		}
	}
	return json.Marshal(out)
}

// NewEvent constructs an Event with sensible defaults filled in.
func NewEvent(method, path string) *Event {
	return &Event{
		Timestamp: time.Now().UTC(),
		Method:    method,
		Path:      path,
	}
}

// SanitizeHeaders returns a deep copy of h with values for any header whose
// name contains a redact substring (case-insensitive) replaced by
// "[redacted]". Used by the audit middleware so the persisted payload row
// never carries Authorization or X-API-Key in plaintext.
//
// Fast path: when redactKeys is empty, the input map is returned by
// reference. Callers needing a defensive copy should make one themselves.
func SanitizeHeaders(h http.Header, redactKeys []string) map[string][]string {
	if len(redactKeys) == 0 {
		// Convert to plain map[string][]string but share the underlying
		// slices; the caller is responsible for not mutating header values.
		out := make(map[string][]string, len(h))
		for k, v := range h {
			out[k] = v
		}
		return out
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		if matchesRedactKey(k, redactKeys) {
			out[k] = []string{"[redacted]"}
			continue
		}
		// Copy slice so a downstream mutation of out can't propagate back
		// into the request's Header map.
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// SanitizeQuery returns a deep copy of q with values redacted by the same
// rule SanitizeHeaders uses. Used for ?api_key=... and similar.
func SanitizeQuery(q map[string][]string, redactKeys []string) map[string][]string {
	if len(redactKeys) == 0 {
		return q
	}
	out := make(map[string][]string, len(q))
	for k, v := range q {
		if matchesRedactKey(k, redactKeys) {
			out[k] = []string{"[redacted]"}
			continue
		}
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

func matchesRedactKey(key string, redactKeys []string) bool {
	lk := strings.ToLower(key)
	for _, rk := range redactKeys {
		if rk == "" {
			continue
		}
		if strings.Contains(lk, strings.ToLower(rk)) {
			return true
		}
	}
	return false
}
