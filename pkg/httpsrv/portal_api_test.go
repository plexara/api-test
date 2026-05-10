package httpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth"
	"github.com/plexara/api-test/pkg/config"
	"github.com/plexara/api-test/pkg/endpoints"
)

// stubGroupForPortal implements endpoints.Endpoints with one route so the
// portal's endpoints catalog has something to enumerate. It's intentionally
// trivial — the portal only reads metadata.
type stubGroupForPortal struct{}

func (stubGroupForPortal) Name() string { return "stub" }
func (stubGroupForPortal) Routes() []endpoints.EndpointMeta {
	return []endpoints.EndpointMeta{
		{Name: "ping", Group: "stub", Method: "GET", Path: "/v1/ping", Description: "ping"},
		{Name: "echo", Group: "stub", Method: "POST", Path: "/v1/echo", Description: "echo"},
	}
}
func (stubGroupForPortal) Mount(*http.ServeMux, endpoints.Middleware) {}

// passthroughMW invokes the next handler with no additional auth — fine
// for tests that exercise the handlers themselves.
func passthroughMW(next http.Handler) http.Handler { return next }

// authedMW attaches a fixed identity, mimicking what PortalAuth would do.
func authedMW(id *auth.Identity) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
		})
	}
}

func newTestPortalAPI(t *testing.T, cfg *config.Config) (*PortalAPI, *audit.MemoryLogger, *endpoints.Registry) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
		cfg.Server.BaseURL = "http://localhost:8080"
	}
	reg := endpoints.NewRegistry()
	reg.Add(stubGroupForPortal{})
	auditLog := audit.NewMemoryLogger()
	return NewPortalAPI(cfg, reg, auditLog, nil), auditLog, reg
}

func TestPortalAPI_Me(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	id := &auth.Identity{Subject: "alice", AuthType: "oidc"}
	p.Mount(mux, authedMW(id))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/me", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got auth.Identity
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Subject != "alice" || got.AuthType != "oidc" {
		t.Errorf("identity = %+v, want subject=alice authtype=oidc", got)
	}
}

func TestPortalAPI_Server_RedactsAllSecrets(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.BaseURL = "http://localhost:8080"
	cfg.Portal.CookieSecret = "super-secret-cookie"
	cfg.OIDC.ClientSecret = "super-secret-client"
	cfg.Plexara.Register.AuthHeader = "Bearer admin-pat"
	cfg.APIKeys.File = []config.FileAPIKey{{Name: "f1", Key: "raw-file-key"}}
	cfg.Bearer.Tokens = []config.FileBearerToken{{Name: "b1", Token: "raw-bearer-tok"}}
	cfg.Database.URL = "postgres://api:s3cret@localhost:5432/db?sslmode=disable"

	p, _, _ := newTestPortalAPI(t, cfg)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/server", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, secret := range []string{"super-secret-cookie", "super-secret-client", "Bearer admin-pat", "raw-file-key", "raw-bearer-tok", "s3cret"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked secret %q in body: %s", secret, body)
		}
	}
	// And confirm the live config wasn't mutated.
	if cfg.Bearer.Tokens[0].Token != "raw-bearer-tok" {
		t.Error("live cfg.Bearer.Tokens was mutated by sanitization")
	}
	if cfg.APIKeys.File[0].Key != "raw-file-key" {
		t.Error("live cfg.APIKeys.File was mutated by sanitization")
	}
	if cfg.Plexara.Register.AuthHeader != "Bearer admin-pat" {
		t.Error("live cfg.Plexara.Register.AuthHeader was mutated by sanitization")
	}
}

func TestRedactDatabaseURL(t *testing.T) {
	cases := map[string]string{
		"": "",
		// URL form: stdlib's (*url.URL).Redacted() replaces password with "xxxxx".
		"postgres://api:s3cret@localhost:5432/db":                  "postgres://api:xxxxx@localhost:5432/db",
		"postgresql://api:s3cret@host/db?sslmode=disable":          "postgresql://api:xxxxx@host/db?sslmode=disable",
		"Postgres://api:s3cret@host/db":                            "postgres://api:xxxxx@host/db", // url.Parse lowercases scheme
		"postgres://api:s3cret@host/db?application_name=u@example": "postgres://api:xxxxx@host/db?application_name=u@example",
		"postgres://api@host/db":                                   "postgres://api@host/db",             // userinfo, no password
		"postgres://localhost/db":                                  "postgres://localhost/db",            // no userinfo
		"postgres://api:s3cret@[::1]:5432/db":                      "postgres://api:xxxxx@[::1]:5432/db", // IPv6 host
		// DSN form: any string containing a `password=` keyword is blanket-redacted.
		// Operator loses the rest of the DSN on the Config page, but no
		// libpq quoting/escaping/whitespace edge case can leak the tail.
		"host=localhost user=api password=s3cret dbname=apitest": "[redacted]",
		"host=h user=u password = s3cret dbname=d":               "[redacted]",
		"password='spaced s3cret' dbname=d":                      "[redacted]",
		`password="dq spaced s3cret" dbname=d`:                   "[redacted]",
		`password='it\'s s3cret' dbname=d`:                       "[redacted]",
		`password='unterminated s3cret`:                          "[redacted]", // malformed quote — would leak tail under field-scan
		`password="unterminated s3cret`:                          "[redacted]", // ditto for double-quote
		"PASSWORD=Caps":                                          "[redacted]",
		// pgx accepts these forms too; round-6 review caught all three.
		"postgres://api@host/db?password=urlquery_s3cret":      "[redacted]", // URL form, password in query
		"postgres://api@host/db?sslpassword=urlssl_s3cret":     "[redacted]", // URL form, sslpassword in query
		"postgres://api@host/db?password=":                     "[redacted]", // URL form, key-presence (empty value)
		"postgres://api@host/db?password=&password=dup_s3cret": "[redacted]", // duplicate-key (round-8)
		"host=h sslpassword=ssl_s3cret":                        "[redacted]", // DSN sslpassword keyword
		"host=h\vpassword=vt_s3cret":                           "[redacted]", // \v separator before key (pgx accepts; Go \s does not)
		"host=h password\v=vt2_s3cret":                         "[redacted]", // \v between key and = (round-7)
		"host=h sslpassword\v=vt3_s3cret":                      "[redacted]", // \v between sslpassword key and = (round-7)
		// No password keyword anywhere: pass through unchanged.
		"host=h port=5432":                 "host=h port=5432",
		"weird-no-equals-no-at":            "weird-no-equals-no-at",
		"applicationname=foo dbname=bar":   "applicationname=foo dbname=bar",
		"applicationname=password_age=foo": "applicationname=password_age=foo", // no `password=` (matched key has trailing `_`)
	}
	for in, want := range cases {
		if got := redactDatabaseURL(in); got != want {
			t.Errorf("redactDatabaseURL(%q) = %q, want %q", in, got, want)
		}
		// Cross-check: the original password substring must never survive any
		// transformation. Catches future regressions automatically when new
		// inputs are added.
		if strings.Contains(in, "s3cret") && strings.Contains(redactDatabaseURL(in), "s3cret") {
			t.Errorf("redactDatabaseURL(%q) leaked s3cret in output: %q", in, redactDatabaseURL(in))
		}
	}
}

func TestPortalAPI_Wellknown(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.BaseURL = "http://localhost:8080/"
	cfg.OIDC.Enabled = true
	cfg.OIDC.Issuer = "http://idp"
	cfg.OIDC.Audience = "api-test"

	p, _, _ := newTestPortalAPI(t, cfg)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/wellknown", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["oidc_enabled"] != true || body["audience"] != "api-test" {
		t.Errorf("wellknown body = %+v", body)
	}
	if body["api_endpoint"] != "http://localhost:8080/v1/" {
		t.Errorf("api_endpoint = %v, want http://localhost:8080/v1/", body["api_endpoint"])
	}
}

func TestPortalAPI_EndpointsAndDetail(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/endpoints", nil))
	var listed map[string]any
	_ = json.NewDecoder(w.Body).Decode(&listed)
	if got, _ := listed["endpoints"].([]any); len(got) != 2 {
		t.Errorf("endpoint count = %d, want 2", len(got))
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/endpoints/ping", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d", w.Code)
	}
	var detail endpoints.EndpointMeta
	_ = json.NewDecoder(w.Body).Decode(&detail)
	if detail.Name != "ping" {
		t.Errorf("detail.Name = %q, want ping", detail.Name)
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/endpoints/no-such", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("missing detail status = %d, want 404", w.Code)
	}
}

func TestPortalAPI_AuditMeta_AdvertisesContract(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/meta", nil))
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	filters, _ := body["filters"].([]any)
	if len(filters) == 0 {
		t.Errorf("audit/meta filters empty")
	}
}

func TestPortalAPI_AuditEventsAndDetail(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	id := uuid.NewString()
	ev := audit.Event{ID: id, Method: "GET", Path: "/v1/ping", Status: 200, Timestamp: time.Now(), Success: true}
	if err := log.Log(context.Background(), ev); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/events", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var listed map[string]any
	_ = json.NewDecoder(w.Body).Decode(&listed)
	events, _ := listed["events"].([]any)
	if len(events) != 1 {
		t.Errorf("events len = %d, want 1", len(events))
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/events/"+id, nil))
	if w.Code != http.StatusOK {
		t.Errorf("detail status = %d, want 200", w.Code)
	}

	// invalid UUID -> 400
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/events/not-a-uuid", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid uuid status = %d, want 400", w.Code)
	}

	// well-formed but unknown id -> 404
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/audit/events/"+uuid.NewString(), nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("missing detail status = %d, want 404", w.Code)
	}
}

func TestPortalAPI_Dashboard(t *testing.T) {
	p, log, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	if err := log.Log(context.Background(), audit.Event{ID: uuid.NewString(), Method: "GET", Path: "/v1/a", Status: 200, Timestamp: time.Now(), Success: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := log.Log(context.Background(), audit.Event{ID: uuid.NewString(), Method: "GET", Path: "/v1/b", Status: 500, Timestamp: time.Now(), Success: false}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/portal/dashboard", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(w.Body).Decode(&body)
	if total, _ := body["total"].(float64); total < 2 {
		t.Errorf("total = %v, want >= 2", body["total"])
	}
	if succ, _ := body["success_count"].(float64); succ < 1 {
		t.Errorf("success_count = %v, want >= 1", body["success_count"])
	}
}

func TestPortalAPI_KeysHandlersReturn503WhenStoreNil(t *testing.T) {
	p, _, _ := newTestPortalAPI(t, nil)
	mux := http.NewServeMux()
	p.Mount(mux, passthroughMW)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/keys"},
		{http.MethodDelete, "/api/v1/admin/keys/dev"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-Requested-With", "XMLHttpRequest") // satisfies CSRF check
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503 (no DB store)", tc.method, tc.path, w.Code)
		}
	}

	// POST with body
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/keys", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("POST status = %d, want 503", w.Code)
	}
}

func TestParseQueryFilter(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?from=2024-01-01T00:00:00Z&to=2024-01-02T00:00:00Z&method=GET&path=/v1/x&route_name=ping&user=u1&session=s1&q=err&status=500&success=false&limit=25&offset=10", nil)
	f := parseQueryFilter(r)
	if f.Method != "GET" || f.Path != "/v1/x" || f.RouteName != "ping" || f.UserID != "u1" || f.SessionID != "s1" || f.Search != "err" {
		t.Errorf("parsed filter = %+v", f)
	}
	if f.Status != 500 || f.Limit != 25 || f.Offset != 10 {
		t.Errorf("numeric fields = %+v", f)
	}
	if f.Success == nil || *f.Success != false {
		t.Errorf("Success = %v, want false", f.Success)
	}
	if f.From.IsZero() || f.To.IsZero() {
		t.Errorf("From/To = %v/%v, want parsed", f.From, f.To)
	}

	// success=true branch
	r2 := httptest.NewRequest(http.MethodGet, "/?success=true", nil)
	if f := parseQueryFilter(r2); f.Success == nil || *f.Success != true {
		t.Errorf("success=true: Success = %v", f.Success)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want x", got)
	}
	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("firstNonEmpty all empty = %q, want empty", got)
	}
}

func TestRedactIfSet(t *testing.T) {
	if got := redactIfSet(""); got != "" {
		t.Errorf("redactIfSet(\"\") = %q, want empty", got)
	}
	if got := redactIfSet("anything"); got != "[redacted]" {
		t.Errorf("redactIfSet = %q, want [redacted]", got)
	}
}

func TestWriteJSONAndError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"k": "v"})
	if w.Code != http.StatusCreated || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("writeJSON header/status wrong: %d %q", w.Code, w.Header().Get("Content-Type"))
	}

	w = httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, errInvalid)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), errInvalid.Error()) {
		t.Errorf("writeError body = %q, status %d", w.Body.String(), w.Code)
	}
}

var errInvalid = constErr("invalid")

type constErr string

func (e constErr) Error() string { return string(e) }
