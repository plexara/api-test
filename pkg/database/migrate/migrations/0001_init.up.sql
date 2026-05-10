-- Initial schema for api-test.
--
-- Two-table audit model: audit_events carries the indexable summary; the
-- sibling audit_payloads carries the full HTTP request/response detail
-- (headers, body, query). Splits keep the summary row free of multi-KB
-- JSONB blobs so time/route/identity queries stay fast; the payload join
-- only runs when an operator drills into a single event in the portal.
--
-- Cascade delete keeps retention cleanup atomic.

CREATE TABLE IF NOT EXISTS api_keys (
    id            TEXT PRIMARY KEY,
    name          TEXT UNIQUE NOT NULL,
    hash          TEXT NOT NULL,
    description   TEXT,
    created_by    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ,
    last_used_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS audit_events (
    id              TEXT PRIMARY KEY,
    ts              TIMESTAMPTZ NOT NULL,
    duration_ms     BIGINT NOT NULL,
    request_id      TEXT,
    session_id      TEXT,
    user_subject    TEXT,
    user_email      TEXT,
    auth_type       TEXT,
    api_key_name    TEXT,

    -- HTTP request/response surface
    method          TEXT NOT NULL,
    path            TEXT NOT NULL,
    route_name      TEXT,                 -- "whoami", "sized", ... (group-relative name)
    endpoint_group  TEXT,                 -- "identity", "data", ...
    status          INTEGER NOT NULL,
    bytes_in        INTEGER NOT NULL DEFAULT 0,
    bytes_out       INTEGER NOT NULL DEFAULT 0,

    success         BOOLEAN NOT NULL,
    error_message   TEXT,
    error_category  TEXT,

    remote_addr     TEXT,
    user_agent      TEXT
);

CREATE INDEX IF NOT EXISTS audit_events_ts_idx       ON audit_events (ts DESC);
CREATE INDEX IF NOT EXISTS audit_events_route_idx    ON audit_events (route_name, ts DESC);
CREATE INDEX IF NOT EXISTS audit_events_path_idx     ON audit_events (path, ts DESC);
CREATE INDEX IF NOT EXISTS audit_events_user_idx     ON audit_events (user_subject, ts DESC);
CREATE INDEX IF NOT EXISTS audit_events_session_idx  ON audit_events (session_id, ts DESC);
CREATE INDEX IF NOT EXISTS audit_events_status_idx   ON audit_events (status, ts DESC);

CREATE TABLE IF NOT EXISTS audit_payloads (
    event_id              TEXT PRIMARY KEY REFERENCES audit_events(id) ON DELETE CASCADE,

    -- Request side
    request_headers       JSONB,
    request_query         JSONB,
    request_content_type  TEXT,
    request_body          BYTEA,
    request_size_bytes    INTEGER NOT NULL DEFAULT 0,
    request_truncated     BOOLEAN NOT NULL DEFAULT false,
    request_remote_addr   TEXT,

    -- Response side
    response_headers      JSONB,
    response_content_type TEXT,
    response_body         BYTEA,
    response_size_bytes   INTEGER NOT NULL DEFAULT 0,
    response_truncated    BOOLEAN NOT NULL DEFAULT false,

    -- Replay linkage; ON DELETE SET NULL so deleting the original doesn't
    -- cascade into the replay's payload row.
    replayed_from         TEXT REFERENCES audit_events(id) ON DELETE SET NULL,

    captured_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_payloads_replayed_from_idx
    ON audit_payloads (replayed_from)
    WHERE replayed_from IS NOT NULL;

-- jsonb_path_ops indexes: smaller and faster than the default GIN for the
-- @> containment operator the portal API uses to filter payload contents.
CREATE INDEX IF NOT EXISTS audit_payloads_request_headers_gin
    ON audit_payloads USING gin (request_headers jsonb_path_ops);
CREATE INDEX IF NOT EXISTS audit_payloads_response_headers_gin
    ON audit_payloads USING gin (response_headers jsonb_path_ops);
