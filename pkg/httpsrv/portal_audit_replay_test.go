package httpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/auth/inbound"
	"github.com/plexara/api-test/pkg/config"
	"github.com/plexara/api-test/pkg/httpmw"
)

// payloadLoggingMemory wraps audit.MemoryLogger and adds a per-ID
// payload store so the replay handler can fetch RequestHeaders /
// RequestBody / RequestQuery for events. MemoryLogger itself doesn't
// persist payloads; this stand-in keeps the test self-contained.
type payloadLoggingMemory struct {
	*audit.MemoryLogger
	payloads map[string]*audit.Payload
}

func newPayloadLoggingMemory() *payloadLoggingMemory {
	return &payloadLoggingMemory{
		MemoryLogger: audit.NewMemoryLogger(),
		payloads:     map[string]*audit.Payload{},
	}
}

func (m *payloadLoggingMemory) GetPayload(_ context.Context, id string) (*audit.Payload, error) {
	if p, ok := m.payloads[id]; ok {
		return p, nil
	}
	return nil, nil
}

func (m *payloadLoggingMemory) seedEventWithPayload(t *testing.T, ev audit.Event, payload *audit.Payload) {
	t.Helper()
	if err := m.MemoryLogger.Log(context.Background(), ev); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m.payloads[ev.ID] = payload
}

// stubV1Mux returns a *http.ServeMux registered with one /v1/* route
// that echoes its method+path+body so a replay can be observed.
func stubV1Mux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
			"body":   string(body),
			"replay": r.Header.Get(replayHeaderMarker),
		})
	})
	return mux
}

func TestAuditReplay_DispatchesThroughTarget(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)
	// Inject a tiny dispatch target with one /v1/echo route.
	target := stubV1Mux(t)
	p.WithReplayTarget(target)

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{
			ID:        id,
			Timestamp: time.Now().UTC().Add(-1 * time.Minute),
			Method:    http.MethodPost,
			Path:      "/v1/echo",
			Status:    200,
			Success:   true,
		},
		&audit.Payload{
			RequestHeaders:     map[string][]string{"X-Custom": {"value"}},
			RequestBody:        []byte(`{"hi":1}`),
			RequestContentType: "application/json",
		},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest") // bypass requireCSRFHeader
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		ReplayedFrom string      `json:"replayed_from"`
		Status       int         `json:"status"`
		Body         string      `json:"body"`
		Headers      http.Header `json:"headers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, w.Body.String())
	}
	if resp.ReplayedFrom != id {
		t.Errorf("replayed_from = %q, want %q", resp.ReplayedFrom, id)
	}
	if resp.Status != 200 {
		t.Errorf("dispatched status = %d, want 200", resp.Status)
	}

	// Echo body should reflect the captured method/path/body AND the
	// replay marker header.
	var echoed map[string]string
	if err := json.Unmarshal([]byte(resp.Body), &echoed); err != nil {
		t.Fatalf("echoed body not JSON: %v (body=%q)", err, resp.Body)
	}
	if echoed["method"] != "POST" || echoed["path"] != "/v1/echo" ||
		echoed["body"] != `{"hi":1}` {
		t.Errorf("echoed shape wrong: %+v", echoed)
	}
	if echoed["replay"] != id {
		t.Errorf("X-Plexara-Replay-From header = %q, want %q", echoed["replay"], id)
	}
}

// TestAuditReplay_RedactedCredentialsThroughRealIdentityMW exercises
// the full replay path through httpmw.Identity to guard against the
// regression where redacted-credential headers ("Authorization:
// [redacted]" / "X-API-Key: [redacted]" / "?api_key=[redacted]") on a
// captured request would be re-emitted by the replay handler, then
// trip httpmw.Identity's wire-credential check, run the inbound chain
// against the redaction sentinel, and 401 the replay.
func TestAuditReplay_RedactedCredentialsThroughRealIdentityMW(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)

	// v1 mux wrapped with the REAL identity middleware. The chain has
	// one bearer authenticator that would reject "[redacted]" — so if
	// the bypass is broken, this test 401s instead of reaching the
	// inner handler.
	innerReached := false
	v1 := http.NewServeMux()
	v1.HandleFunc("POST /v1/echo", func(w http.ResponseWriter, r *http.Request) {
		innerReached = true
		id := inbound.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"auth_type": id.AuthType,
			"subject":   id.Subject,
		})
	})
	chain := inbound.NewChain(false,
		inbound.NewBearer([]config.FileBearerToken{{Name: "real", Token: "real-token"}}))
	target := httpmw.Identity(chain, slog.New(slog.NewTextHandler(io.Discard, nil)))(v1)
	p.WithReplayTarget(target)

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{
			ID: id, Timestamp: time.Now().UTC(),
			Method: http.MethodPost, Path: "/v1/echo", Status: 200, Success: true,
		},
		&audit.Payload{
			RequestHeaders: map[string][]string{
				"Authorization": {"[redacted]"},
				"X-Api-Key":     {"[redacted]"},
				"X-Custom":      {"keep-me"},
			},
			RequestQuery:       map[string][]string{"api_key": {"[redacted]"}},
			RequestBody:        []byte(`{"hi":1}`),
			RequestContentType: "application/json",
		},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	// Carry an auth.Identity in context so the replay handler's
	// portal-identity bridge fires (production sets this via
	// PortalAuth.Middleware; we mimic the post-auth state).
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+id, nil).WithContext(
		auth.WithIdentity(context.Background(),
			&auth.Identity{Subject: "alice", AuthType: "session", APIKeyID: "portal"}))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("portal replay returned %d (body=%s)", w.Code, w.Body.String())
	}
	var env struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode portal envelope: %v", err)
	}
	if env.Status != http.StatusOK {
		t.Fatalf("dispatched status = %d, want 200 — redacted creds re-emitted into v1 mux probably ate the bypass", env.Status)
	}
	if !innerReached {
		t.Fatal("v1 handler never reached — replay 401'd somewhere upstream")
	}
}

func TestAuditReplay_DisabledWhenNoTarget(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)
	// Do NOT call WithReplayTarget; do NOT Mount onto a real mux.
	// We mount onto an empty mux to exercise the "no target" branch.

	mux := http.NewServeMux()
	// Skip the Mount-side auto-wiring by replicating the route registration:
	mux.Handle("POST /api/v1/portal/audit/replay/{id}",
		passthroughMW(requireCSRFHeader(http.HandlerFunc(p.auditReplay))))

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+uuid.NewString(), nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 (replay disabled without target)", w.Code)
	}
}

func TestAuditReplay_RejectsInvalidUUID(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)
	p.WithReplayTarget(stubV1Mux(t))

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/not-a-uuid", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestAuditReplay_RejectsNonV1Path(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)
	p.WithReplayTarget(stubV1Mux(t))

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{ID: id, Method: "POST", Path: "/api/v1/portal/audit/events", Status: 200},
		&audit.Payload{},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 (non-/v1 path must be refused)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not replay-eligible") {
		t.Errorf("error body should mention 'not replay-eligible': %s", w.Body.String())
	}
}

func TestAuditReplay_RefusesReplayOfReplay(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)
	p.WithReplayTarget(stubV1Mux(t))

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{ID: id, Method: "POST", Path: "/v1/echo", Status: 200},
		&audit.Payload{
			RequestHeaders: map[string][]string{
				replayHeaderMarker: {"prior-replay-id"},
			},
		},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 (replay-of-replay must be refused)", w.Code)
	}
}

func TestAuditReplay_PayloadMissing(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)
	p.WithReplayTarget(stubV1Mux(t))

	id := uuid.NewString()
	// Log the event but skip the payload sibling.
	_ = log.MemoryLogger.Log(context.Background(), audit.Event{
		ID:     id,
		Method: "POST",
		Path:   "/v1/echo",
	})

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 (no payload captured)", w.Code)
	}
}

func TestAuditReplay_PreservesQueryParameters(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)

	target := http.NewServeMux()
	target.HandleFunc("GET /v1/query-echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": r.URL.Query(),
		})
	})
	p.WithReplayTarget(target)

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{ID: id, Method: "GET", Path: "/v1/query-echo"},
		&audit.Payload{
			RequestQuery: map[string][]string{
				"a": {"1", "2"},
				"b": {"x"},
			},
		},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
	}
	var outer struct {
		Body string `json:"body"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &outer)
	var inner struct {
		Query map[string][]string `json:"query"`
	}
	_ = json.Unmarshal([]byte(outer.Body), &inner)
	if got := inner.Query["a"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("query a = %v, want [1 2]", got)
	}
	if got := inner.Query["b"]; len(got) != 1 || got[0] != "x" {
		t.Errorf("query b = %v, want [x]", got)
	}
}

// TestAuditReplay_CapsResponseBodyWithoutBuffering regresses the
// unbounded-buffering bug. Stub target emits ~5 MiB; the replay
// must cap at replayMaxBodyBytes (1 MiB).
func TestAuditReplay_CapsResponseBodyWithoutBuffering(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)

	target := http.NewServeMux()
	target.HandleFunc("GET /v1/large", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := bytes.Repeat([]byte("A"), 32*1024)
		for i := 0; i < 160; i++ {
			_, _ = w.Write(chunk)
		}
	})
	p.WithReplayTarget(target)

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{ID: id, Method: "GET", Path: "/v1/large"},
		&audit.Payload{},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Body          string `json:"body"`
		BodyTruncated bool   `json:"body_truncated"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.BodyTruncated {
		t.Errorf("body_truncated = false; expected true for >1 MiB response")
	}
	if len(resp.Body) > replayMaxBodyBytes {
		t.Errorf("captured body is %d bytes; cap is %d", len(resp.Body), replayMaxBodyBytes)
	}
	if len(resp.Body) < replayMaxBodyBytes/2 {
		t.Errorf("captured body suspiciously small: %d bytes", len(resp.Body))
	}
}

// TestAuditReplay_SetsReplayMarkerOnDispatchedRequest regresses the
// missing-lineage bug. The replay handler attaches the marker header
// to the dispatched request; the audit middleware uses it to populate
// Payload.ReplayedFrom on the new audit row.
func TestAuditReplay_SetsReplayMarkerOnDispatchedRequest(t *testing.T) {
	log := newPayloadLoggingMemory()
	p := NewPortalAPI(nil, nil, log, nil)

	var observedMarker string
	target := http.NewServeMux()
	target.HandleFunc("GET /v1/check", func(_ http.ResponseWriter, r *http.Request) {
		observedMarker = r.Header.Get(audit.ReplayHeaderName)
	})
	p.WithReplayTarget(target)

	id := uuid.NewString()
	log.seedEventWithPayload(t,
		audit.Event{ID: id, Method: "GET", Path: "/v1/check"},
		&audit.Payload{},
	)

	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/audit/replay/"+id, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if observedMarker != id {
		t.Errorf("dispatched request did not carry %s = %q (got %q)",
			audit.ReplayHeaderName, id, observedMarker)
	}
}

// noPayloadLogger implements audit.Logger but NOT audit.PayloadLogger,
// so the replay-feature-flag gate can be exercised. Production's
// NoopLogger doesn't implement PayloadLogger either; this stub mirrors
// that shape for the test.
type noPayloadLogger struct{}

func (noPayloadLogger) Log(context.Context, audit.Event) error { return nil }
func (noPayloadLogger) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (noPayloadLogger) Count(context.Context, audit.QueryFilter) (int64, error) { return 0, nil }

// TestAuditReplay_FeatureFlagFalseWithoutPayloadLogger regresses the
// misleading-flag bug: features.replay must be false when the Logger
// can't actually serve payloads.
func TestAuditReplay_FeatureFlagFalseWithoutPayloadLogger(t *testing.T) {
	p := NewPortalAPI(nil, nil, noPayloadLogger{}, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/meta", nil))
	var body struct {
		Features map[string]bool `json:"features"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Features["replay"] {
		t.Errorf("features.replay should be false when audit Logger is not a PayloadLogger")
	}
}
