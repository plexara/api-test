package httpmw

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth/inbound"
	"github.com/plexara/api-test/pkg/config"
	"github.com/plexara/api-test/pkg/endpoints"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- RequestID ---

func TestRequestID_PreservesInbound(t *testing.T) {
	var got string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestIDFromContext(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderRequestID, "abc-123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got != "abc-123" {
		t.Errorf("got %q want abc-123", got)
	}
	if w.Header().Get(HeaderRequestID) != "abc-123" {
		t.Errorf("response header not echoed")
	}
}

func TestRequestID_GeneratesNew(t *testing.T) {
	var got string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestIDFromContext(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got == "" {
		t.Error("no request id generated")
	}
	if w.Header().Get(HeaderRequestID) != got {
		t.Error("response header doesn't match context value")
	}
}

// --- Identity ---

func TestIdentity_401WhenNoCredAndNoAnonymous(t *testing.T) {
	chain := inbound.NewChain(false, inbound.NewBearer([]config.FileBearerToken{{Name: "x", Token: "t"}}))
	h := Identity(chain, discardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler should not be reached")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d want 401", w.Code)
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("WWW-Authenticate missing: %q", w.Header().Get("WWW-Authenticate"))
	}
}

func TestIdentity_PreSetSkipsChain(t *testing.T) {
	// Try-It dispatch path: the portal handler has already authed the
	// operator and attached an inbound.Identity to the request context
	// before re-entering the mux. The chain — which would otherwise 401
	// because no credential is on the wire — must yield.
	chain := inbound.NewChain(false, inbound.NewBearer([]config.FileBearerToken{{Name: "x", Token: "t"}}))
	var saw *inbound.Identity
	h := Identity(chain, discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		saw = inbound.FromContext(r.Context())
	}))
	pre := &inbound.Identity{Subject: "alice", AuthType: "portal", KeyName: "session"}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(inbound.WithIdentity(r.Context(), pre))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status %d want 200", w.Code)
	}
	if saw == nil || saw.Subject != "alice" || saw.AuthType != "portal" {
		t.Errorf("identity = %+v, want pre-set alice/portal", saw)
	}
}

func TestIdentity_WireCredentialOverridesPreSet(t *testing.T) {
	// Operator types X-API-Key into the Try-It headers field to test
	// "does this key resolve to the right principal?". The chain must
	// still run so the resolved Identity reflects the wire credential,
	// not the portal session that planted the pre-identity.
	store := inbound.NewFileAPIKeyStore([]config.FileAPIKey{{Name: "ci-runner", Key: "secret"}})
	apikey := inbound.NewAPIKey(store, "X-API-Key", "api_key")
	chain := inbound.NewChain(false, apikey)

	var saw *inbound.Identity
	h := Identity(chain, discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		saw = inbound.FromContext(r.Context())
	}))

	portalPre := &inbound.Identity{Subject: "alice", AuthType: "portal", KeyName: "session"}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "secret")
	r = r.WithContext(inbound.WithIdentity(r.Context(), portalPre))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status %d want 200", w.Code)
	}
	if saw == nil || saw.KeyName != "ci-runner" || saw.AuthType != "apikey" {
		t.Errorf("identity = %+v, want wire creds (ci-runner/apikey) not portal pre-set", saw)
	}
}

func TestIdentity_AnonymousFallthrough(t *testing.T) {
	chain := inbound.NewChain(true)
	var saw *inbound.Identity
	h := Identity(chain, discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		saw = inbound.FromContext(r.Context())
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
	if saw == nil || saw.AuthType != "anonymous" {
		t.Errorf("identity = %+v", saw)
	}
}

func TestIdentity_401OnInvalid(t *testing.T) {
	chain := inbound.NewChain(true, inbound.NewBearer([]config.FileBearerToken{{Name: "x", Token: "good"}}))
	h := Identity(chain, discardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler should not be reached")
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("WWW-Authenticate missing invalid_token: %q", w.Header().Get("WWW-Authenticate"))
	}
}

// --- Audit ---

func TestAudit_WritesEventAndRedactsHeaders(t *testing.T) {
	ml := audit.NewMemoryLogger()
	registry := endpoints.NewRegistry()

	h := Audit(ml, registry, discardLogger(), AuditOptions{
		CapturePayloads: true,
		CaptureHeaders:  true,
		MaxPayloadBytes: 1024,
		// Both "api_key" (underscore — matches the query param) and "api-key"
		// (dash — matches the X-API-Key header) are needed; matchesRedactKey
		// is exact substring, not regex. The default config carries both.
		RedactKeys: []string{"authorization", "api-key", "api_key"},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/test?api_key=keep_secret", strings.NewReader(""))
	r.Header.Set("Authorization", "Bearer secret-tok")
	r.Header.Set("X-Trace-Id", "trace-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	snap := ml.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d", len(snap))
	}
	ev := snap[0]
	if ev.Method != "GET" || ev.Path != "/v1/test" {
		t.Errorf("method/path = %s %s", ev.Method, ev.Path)
	}
	if ev.Status != http.StatusOK {
		t.Errorf("status = %d", ev.Status)
	}
	if !ev.Success {
		t.Error("success should be true for 200")
	}
	if ev.Payload == nil {
		t.Fatal("payload nil")
	}
	if v := ev.Payload.RequestHeaders["Authorization"]; len(v) != 1 || v[0] != "[redacted]" {
		t.Errorf("Authorization not redacted: %v", v)
	}
	if v := ev.Payload.RequestQuery["api_key"]; len(v) != 1 || v[0] != "[redacted]" {
		t.Errorf("api_key query not redacted: %v", v)
	}
	if string(ev.Payload.ResponseBody) != `{"ok":true}` {
		t.Errorf("response body = %q", string(ev.Payload.ResponseBody))
	}
	if ev.Payload.ResponseContentType != "application/json" {
		t.Errorf("response content type = %q", ev.Payload.ResponseContentType)
	}
}

func TestAudit_CapturesIdentity(t *testing.T) {
	ml := audit.NewMemoryLogger()
	registry := endpoints.NewRegistry()

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := inbound.WithIdentity(r.Context(), &inbound.Identity{
				Subject: "alice", AuthType: "apikey", KeyName: "devkey",
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	h := mw(Audit(ml, registry, discardLogger(), AuditOptions{
		CapturePayloads: false,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/x", nil))

	snap := ml.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d", len(snap))
	}
	ev := snap[0]
	if ev.UserSubject != "alice" || ev.AuthType != "apikey" || ev.APIKeyName != "devkey" {
		t.Errorf("identity not captured: %+v", ev)
	}
}

func TestAudit_FailureMarkedOnNon2xx(t *testing.T) {
	ml := audit.NewMemoryLogger()
	registry := endpoints.NewRegistry()

	h := Audit(ml, registry, discardLogger(), AuditOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/y", nil))

	ev := ml.Snapshot()[0]
	if ev.Status != 500 || ev.Success {
		t.Errorf("status=%d success=%v", ev.Status, ev.Success)
	}
}

func TestAudit_ResponseTruncated(t *testing.T) {
	ml := audit.NewMemoryLogger()
	h := Audit(ml, endpoints.NewRegistry(), discardLogger(), AuditOptions{
		CapturePayloads: true, MaxPayloadBytes: 16,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("X", 64)))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	ev := ml.Snapshot()[0]
	if ev.Payload == nil || !ev.Payload.ResponseTruncated {
		t.Errorf("expected truncated, payload=%+v", ev.Payload)
	}
	if len(ev.Payload.ResponseBody) != 16 {
		t.Errorf("captured body len = %d, want 16", len(ev.Payload.ResponseBody))
	}
	if ev.BytesOut != 64 {
		t.Errorf("BytesOut = %d, want 64", ev.BytesOut)
	}
}

func TestAudit_RequestBodyCaptured(t *testing.T) {
	ml := audit.NewMemoryLogger()
	h := Audit(ml, endpoints.NewRegistry(), discardLogger(), AuditOptions{
		CapturePayloads: true, MaxPayloadBytes: 1024,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain body so the tee pulls everything.
		buf, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	}))
	body := strings.NewReader(`hello world`)
	r := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	ev := ml.Snapshot()[0]
	if string(ev.Payload.RequestBody) != "hello world" {
		t.Errorf("request body = %q", string(ev.Payload.RequestBody))
	}
	if ev.BytesIn != 11 {
		t.Errorf("BytesIn = %d, want 11", ev.BytesIn)
	}
}

// --- AccessLog ---

func TestAccessLog_PassesThrough(t *testing.T) {
	called := false
	h := AccessLog(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("tea"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("handler not called")
	}
	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d", w.Code)
	}
}

// TestAccessLog_SeesIdentitySetByPerRouteMiddleware locks down the
// coordination between RequestID, AccessLog, and Identity. AccessLog
// wraps the mux from the outside; Identity runs inside the per-route
// chain. Without the per-request identityHolder seeded by RequestID,
// AccessLog would always see nil because `r.WithContext(...)` only
// flows downward.
func TestAccessLog_SeesIdentitySetByPerRouteMiddleware(t *testing.T) {
	chain := inbound.NewChain(false, inbound.NewBearer([]config.FileBearerToken{{Name: "tok", Token: "good"}}))

	var captured []string
	logger := slog.New(slog.NewTextHandler(&captureWriter{lines: &captured}, &slog.HandlerOptions{Level: slog.LevelInfo}))

	endpointMW := func(next http.Handler) http.Handler {
		return Identity(chain, discardLogger())(next)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/x", endpointMW(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	stack := RequestID(AccessLog(logger)(mux))

	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	r.Header.Set("Authorization", "Bearer good")
	w := httptest.NewRecorder()
	stack.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if len(captured) == 0 {
		t.Fatal("no access log line emitted")
	}
	line := captured[len(captured)-1]
	if !strings.Contains(line, "auth_type=bearer") {
		t.Errorf("access log missing auth_type=bearer: %s", line)
	}
	if !strings.Contains(line, "subject=tok") {
		t.Errorf("access log missing subject=tok: %s", line)
	}
}

type captureWriter struct{ lines *[]string }

func (c *captureWriter) Write(p []byte) (int, error) {
	*c.lines = append(*c.lines, string(p))
	return len(p), nil
}

// Sanity check that the middleware stack composes without panicking.
func TestComposition(t *testing.T) {
	chain := inbound.NewChain(true)
	stack := func(next http.Handler) http.Handler {
		return RequestID(AccessLog(discardLogger())(Identity(chain, discardLogger())(
			Audit(audit.NoopLogger{}, endpoints.NewRegistry(), discardLogger(), AuditOptions{})(next),
		)))
	}
	h := stack(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Identity must have populated.
		if id := inbound.FromContext(r.Context()); id == nil {
			t.Error("identity missing")
		}
		// Request id must be set.
		if RequestIDFromContext(r.Context()) == "" {
			t.Error("request id missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("status %d", w.Code)
	}
	_ = context.Background()
}
