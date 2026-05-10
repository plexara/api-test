package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plexara/api-test/pkg/endpoints"
)

func newTestMux(t *testing.T, redact []string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	New(redact).Mount(mux, endpoints.PassthroughMiddleware)
	return mux
}

func TestWhoami_Anonymous(t *testing.T) {
	mux := newTestMux(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body WhoamiResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AuthType != "anonymous" {
		t.Errorf("auth_type = %q want anonymous", body.AuthType)
	}
}

func TestHeaders_RedactsConfigured(t *testing.T) {
	mux := newTestMux(t, []string{"authorization", "x-api-key", "cookie"})

	req := httptest.NewRequest(http.MethodGet, "/v1/headers", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	req.Header.Set("X-API-Key", "key-abc")
	req.Header.Set("Cookie", "session=xyz")
	req.Header.Set("X-Trace-Id", "trace-789")
	req.Header.Add("Accept-Language", "en-US")
	req.Header.Add("Accept-Language", "fr-FR")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body HeadersResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	checkRedacted := func(name string) {
		t.Helper()
		v, ok := body.Headers[name]
		if !ok {
			t.Errorf("missing header %q", name)
			return
		}
		if len(v) != 1 || v[0] != "[redacted]" {
			t.Errorf("%s not redacted: %v", name, v)
		}
	}
	checkRedacted("Authorization")
	checkRedacted("X-Api-Key") // canonicalized by net/http
	checkRedacted("Cookie")

	if v := body.Headers["X-Trace-Id"]; len(v) != 1 || v[0] != "trace-789" {
		t.Errorf("X-Trace-Id altered: %v", v)
	}
	if v := body.Headers["Accept-Language"]; len(v) != 2 {
		t.Errorf("Accept-Language not preserved: %v", v)
	}
	// Sanity: response must not contain the secret value verbatim.
	if strings.Contains(w.Body.String(), "secret-token-123") {
		t.Error("response leaks secret token")
	}
}
