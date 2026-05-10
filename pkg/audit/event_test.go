package audit

import (
	"net/http"
	"testing"
)

func TestSanitizeHeaders_Redacts(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("X-API-Key", "key-abc")
	h.Set("Cookie", "session=xyz")
	h.Set("X-Trace-Id", "trace-1")
	h.Add("Accept-Language", "en")
	h.Add("Accept-Language", "fr")

	// "api-key" needed because "X-API-Key" lowercases to "x-api-key" (dashes,
	// not underscores). The default redact list in pkg/config includes both.
	out := SanitizeHeaders(h, []string{"authorization", "api-key", "cookie"})

	for _, name := range []string{"Authorization", "X-Api-Key", "Cookie"} {
		v, ok := out[name]
		if !ok {
			t.Errorf("missing header %q", name)
			continue
		}
		if len(v) != 1 || v[0] != "[redacted]" {
			t.Errorf("%s not redacted: %v", name, v)
		}
	}
	if v := out["X-Trace-Id"]; len(v) != 1 || v[0] != "trace-1" {
		t.Errorf("non-secret header altered: %v", v)
	}
	if v := out["Accept-Language"]; len(v) != 2 {
		t.Errorf("multi-value header lost values: %v", v)
	}
}

func TestSanitizeHeaders_EmptyKeysReturnsCopy(t *testing.T) {
	h := http.Header{"X": {"y"}}
	out := SanitizeHeaders(h, nil)
	if got := out["X"]; len(got) != 1 || got[0] != "y" {
		t.Errorf("got %v", got)
	}
}

func TestSanitizeQuery_Redacts(t *testing.T) {
	q := map[string][]string{
		"api_key": {"k123"},
		"foo":     {"bar"},
	}
	out := SanitizeQuery(q, []string{"api_key"})
	if v := out["api_key"]; len(v) != 1 || v[0] != "[redacted]" {
		t.Errorf("api_key not redacted: %v", v)
	}
	if v := out["foo"]; len(v) != 1 || v[0] != "bar" {
		t.Errorf("foo altered: %v", v)
	}
}

func TestNewEvent(t *testing.T) {
	ev := NewEvent("GET", "/v1/foo")
	if ev.Method != "GET" {
		t.Errorf("method = %q", ev.Method)
	}
	if ev.Path != "/v1/foo" {
		t.Errorf("path = %q", ev.Path)
	}
	if ev.Timestamp.IsZero() {
		t.Error("timestamp not set")
	}
}
