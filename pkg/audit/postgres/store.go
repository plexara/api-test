// Package auditpg provides a pgx-backed implementation of audit.Logger
// shaped for api-test's HTTP request/response audit log.
package auditpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/plexara/api-test/pkg/audit"
)

// Store is a pgxpool-backed audit.Logger.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Log inserts a single event. When ev.Payload is non-nil, the matching row
// is also inserted into audit_payloads in the same transaction so summary
// and detail are committed atomically.
func (s *Store) Log(ctx context.Context, ev audit.Event) error {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, ts, duration_ms, request_id, session_id,
			user_subject, user_email, auth_type, api_key_name,
			method, path, route_name, endpoint_group, status,
			bytes_in, bytes_out,
			success, error_message, error_category,
			remote_addr, user_agent
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,$9,
			$10,$11,$12,$13,$14,
			$15,$16,
			$17,$18,$19,
			$20,$21
		)
	`,
		ev.ID, ev.Timestamp, ev.DurationMS, ev.RequestID, ev.SessionID,
		ev.UserSubject, ev.UserEmail, ev.AuthType, ev.APIKeyName,
		ev.Method, ev.Path, ev.RouteName, ev.EndpointGroup, ev.Status,
		ev.BytesIn, ev.BytesOut,
		ev.Success, ev.ErrorMessage, ev.ErrorCategory,
		ev.RemoteAddr, ev.UserAgent,
	)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}

	if ev.Payload != nil {
		if err := insertPayload(ctx, tx, ev.ID, ev.Payload); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit audit event: %w", err)
	}
	return nil
}

// insertPayload writes the audit_payloads row. Caller must hold an open
// tx; this function never commits or rolls back on its own.
func insertPayload(ctx context.Context, tx pgx.Tx, eventID string, p *audit.Payload) error {
	requestHeaders, err := marshalJSONB(p.RequestHeaders)
	if err != nil {
		return fmt.Errorf("marshal request_headers: %w", err)
	}
	requestQuery, err := marshalJSONB(p.RequestQuery)
	if err != nil {
		return fmt.Errorf("marshal request_query: %w", err)
	}
	responseHeaders, err := marshalJSONB(p.ResponseHeaders)
	if err != nil {
		return fmt.Errorf("marshal response_headers: %w", err)
	}
	var replayedFrom any
	if p.ReplayedFrom != "" {
		replayedFrom = p.ReplayedFrom
	}
	// pgx treats nil []byte as SQL NULL for BYTEA, which is what we want
	// when the body wasn't captured.
	var requestBody any
	if len(p.RequestBody) > 0 {
		requestBody = p.RequestBody
	}
	var responseBody any
	if len(p.ResponseBody) > 0 {
		responseBody = p.ResponseBody
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_payloads (
			event_id,
			request_headers, request_query, request_content_type, request_body,
			request_size_bytes, request_truncated, request_remote_addr,
			response_headers, response_content_type, response_body,
			response_size_bytes, response_truncated,
			replayed_from
		) VALUES (
			$1,
			$2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13,
			$14
		)
	`,
		eventID,
		requestHeaders, requestQuery, p.RequestContentType, requestBody,
		p.RequestSizeBytes, p.RequestTruncated, p.RequestRemoteAddr,
		responseHeaders, p.ResponseContentType, responseBody,
		p.ResponseSizeBytes, p.ResponseTruncated,
		replayedFrom,
	)
	if err != nil {
		return fmt.Errorf("insert audit payload: %w", err)
	}
	return nil
}

// marshalJSONB returns the JSON encoding of v, or nil for nil-or-empty
// inputs so the column stores SQL NULL.
func marshalJSONB(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if isEmptyValue(v) {
		return nil, nil
	}
	return json.Marshal(v)
}

func isEmptyValue(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Chan, reflect.String:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

// GetPayload returns the audit_payloads row for the given event, or
// (nil, nil) if no payload was captured. Errors other than "no rows" are
// returned.
func (s *Store) GetPayload(ctx context.Context, eventID string) (*audit.Payload, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
			request_headers, request_query,
			COALESCE(request_content_type, ''), request_body,
			request_size_bytes, request_truncated,
			COALESCE(request_remote_addr, ''),
			response_headers,
			COALESCE(response_content_type, ''), response_body,
			response_size_bytes, response_truncated,
			COALESCE(replayed_from, '')
		FROM audit_payloads WHERE event_id = $1
	`, eventID)

	var (
		reqHeaders, reqQuery, respHeaders []byte
		reqContentType, respContentType   string
		reqBody, respBody                 []byte
		reqSize, respSize                 int
		reqTrunc, respTrunc               bool
		reqRemoteAddr, replayedFrom       string
	)
	if err := row.Scan(
		&reqHeaders, &reqQuery,
		&reqContentType, &reqBody,
		&reqSize, &reqTrunc,
		&reqRemoteAddr,
		&respHeaders,
		&respContentType, &respBody,
		&respSize, &respTrunc,
		&replayedFrom,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query audit_payloads: %w", err)
	}

	p := &audit.Payload{
		RequestContentType:  reqContentType,
		RequestBody:         reqBody,
		RequestSizeBytes:    reqSize,
		RequestTruncated:    reqTrunc,
		RequestRemoteAddr:   reqRemoteAddr,
		ResponseContentType: respContentType,
		ResponseBody:        respBody,
		ResponseSizeBytes:   respSize,
		ResponseTruncated:   respTrunc,
		ReplayedFrom:        replayedFrom,
	}
	if len(reqHeaders) > 0 {
		_ = json.Unmarshal(reqHeaders, &p.RequestHeaders)
	}
	if len(reqQuery) > 0 {
		_ = json.Unmarshal(reqQuery, &p.RequestQuery)
	}
	if len(respHeaders) > 0 {
		_ = json.Unmarshal(respHeaders, &p.ResponseHeaders)
	}
	return p, nil
}

// Query returns matching events ordered by timestamp DESC, id ASC.
func (s *Store) Query(ctx context.Context, f audit.QueryFilter) ([]audit.Event, error) {
	q, args := buildSelect(f, false)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit_events: %w", err)
	}
	defer rows.Close()

	var out []audit.Event
	for rows.Next() {
		var ev audit.Event
		if err := scanEvent(rows, &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Count returns the number of matching events. Limit/Offset are ignored.
func (s *Store) Count(ctx context.Context, f audit.QueryFilter) (int64, error) {
	q, args := buildSelect(f, true)
	var n int64
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count audit_events: %w", err)
	}
	return n, nil
}

// buildSelect emits the WHERE/ORDER/LIMIT clauses for either a row select
// or a count(*). The two paths share predicate construction so the count
// number always matches the result set.
func buildSelect(f audit.QueryFilter, count bool) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	add := func(clause string, val any) {
		args = append(args, val)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if !f.From.IsZero() {
		add("ts >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add("ts <= $%d", f.To)
	}
	if f.Method != "" {
		add("method = $%d", f.Method)
	}
	if f.Path != "" {
		add("path = $%d", f.Path)
	}
	if f.RouteName != "" {
		add("route_name = $%d", f.RouteName)
	}
	if f.UserID != "" {
		add("user_subject = $%d", f.UserID)
	}
	if f.SessionID != "" {
		add("session_id = $%d", f.SessionID)
	}
	if f.EventID != "" {
		add("id = $%d", f.EventID)
	}
	if f.Status != 0 {
		add("status = $%d", f.Status)
	}
	if f.Success != nil {
		add("success = $%d", *f.Success)
	}
	if f.Search != "" {
		// Light search across path + error_message; the portal can wire a
		// proper trgm/fts index later. Bind the value once, reference it
		// from two placeholders.
		args = append(args, "%"+f.Search+"%")
		idx := len(args)
		clauses = append(clauses, fmt.Sprintf("(path ILIKE $%d OR error_message ILIKE $%d)", idx, idx))
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	if count {
		return "SELECT count(*) FROM audit_events " + where, args
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > audit.MaxQueryLimit {
		limit = audit.MaxQueryLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	q := `
		SELECT
			id, ts, duration_ms, COALESCE(request_id,''), COALESCE(session_id,''),
			COALESCE(user_subject,''), COALESCE(user_email,''),
			COALESCE(auth_type,''), COALESCE(api_key_name,''),
			method, path, COALESCE(route_name,''), COALESCE(endpoint_group,''),
			status, bytes_in, bytes_out,
			success, COALESCE(error_message,''), COALESCE(error_category,''),
			COALESCE(remote_addr,''), COALESCE(user_agent,'')
		FROM audit_events ` + where + `
		ORDER BY ts DESC, id ASC
		LIMIT ` + itoa(limit) + ` OFFSET ` + itoa(offset)
	return q, args
}

// scanEvent reads one row into ev. Caller controls rows.Next.
func scanEvent(rows pgx.Rows, ev *audit.Event) error {
	var ts time.Time
	if err := rows.Scan(
		&ev.ID, &ts, &ev.DurationMS, &ev.RequestID, &ev.SessionID,
		&ev.UserSubject, &ev.UserEmail, &ev.AuthType, &ev.APIKeyName,
		&ev.Method, &ev.Path, &ev.RouteName, &ev.EndpointGroup,
		&ev.Status, &ev.BytesIn, &ev.BytesOut,
		&ev.Success, &ev.ErrorMessage, &ev.ErrorCategory,
		&ev.RemoteAddr, &ev.UserAgent,
	); err != nil {
		return fmt.Errorf("scan audit row: %w", err)
	}
	ev.Timestamp = ts
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
