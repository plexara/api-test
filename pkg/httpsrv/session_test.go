package httpsrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plexara/api-test/pkg/auth"
)

const testSecret = "0123456789abcdef-test"

func TestNewSessionStore_RejectsShortSecret(t *testing.T) {
	if _, err := NewSessionStore("c", "short", false, 0); err == nil {
		t.Fatal("short secret: want error")
	}
}

func TestNewSessionStore_DefaultsCookieNameAndMaxAge(t *testing.T) {
	s, err := NewSessionStore("", testSecret, false, 0)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	if s.CookieName() != "api_test_session" {
		t.Errorf("default cookie name = %q, want api_test_session", s.CookieName())
	}
}

func TestSessionStore_IssueAndReadRoundTrip(t *testing.T) {
	s, err := NewSessionStore("api_test_session", testSecret, false, time.Hour)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	id := &auth.Identity{Subject: "alice", Email: "a@x", AuthType: "oidc"}

	w := httptest.NewRecorder()
	if err := s.Issue(w, id); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != "api_test_session" || !cookie.HttpOnly {
		t.Errorf("cookie attrs unexpected: %+v", cookie)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	got := s.Read(r)
	if got == nil || got.Subject != "alice" || got.Email != "a@x" {
		t.Errorf("Read returned %+v, want subject=alice email=a@x", got)
	}
}

func TestSessionStore_ReadNoCookie(t *testing.T) {
	s, _ := NewSessionStore("c", testSecret, false, time.Hour)
	if got := s.Read(httptest.NewRequest(http.MethodGet, "/", nil)); got != nil {
		t.Errorf("Read with no cookie = %+v, want nil", got)
	}
}

func TestSessionStore_ReadEmptyCookie(t *testing.T) {
	s, _ := NewSessionStore("c", testSecret, false, time.Hour)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "c", Value: ""})
	if got := s.Read(r); got != nil {
		t.Errorf("Read with empty cookie = %+v, want nil", got)
	}
}

func TestSessionStore_ReadRejectsTamperedSignature(t *testing.T) {
	s, _ := NewSessionStore("c", testSecret, false, time.Hour)
	w := httptest.NewRecorder()
	if err := s.Issue(w, &auth.Identity{Subject: "alice"}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := w.Result().Cookies()[0]
	parts := strings.SplitN(cookie.Value, ".", 2)
	cookie.Value = parts[0] + "." + strings.Repeat("A", len(parts[1])) // signature flipped to garbage

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if got := s.Read(r); got != nil {
		t.Errorf("Read with tampered sig = %+v, want nil", got)
	}
}

func TestSessionStore_ReadRejectsMalformedCookie(t *testing.T) {
	s, _ := NewSessionStore("c", testSecret, false, time.Hour)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "c", Value: "no-dot-separator"})
	if got := s.Read(r); got != nil {
		t.Errorf("Read with malformed cookie = %+v, want nil", got)
	}
}

func TestSessionStore_ReadRejectsExpired(t *testing.T) {
	s, _ := NewSessionStore("c", testSecret, false, time.Hour)
	pl := SessionPayload{Identity: &auth.Identity{Subject: "alice"}, Expires: time.Now().Add(-time.Minute)}
	enc, err := s.encode(pl)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "c", Value: enc})
	if got := s.Read(r); got != nil {
		t.Errorf("Read expired = %+v, want nil", got)
	}
}

func TestSessionStore_ReadRejectsForeignSecret(t *testing.T) {
	a, _ := NewSessionStore("c", testSecret, false, time.Hour)
	b, _ := NewSessionStore("c", "different-secret-value-1234", false, time.Hour)
	w := httptest.NewRecorder()
	if err := a.Issue(w, &auth.Identity{Subject: "alice"}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := w.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if got := b.Read(r); got != nil {
		t.Errorf("Read across stores with different secrets = %+v, want nil", got)
	}
}

func TestSessionStore_Clear(t *testing.T) {
	s, _ := NewSessionStore("c", testSecret, true, time.Hour)
	w := httptest.NewRecorder()
	s.Clear(w)
	cookie := w.Result().Cookies()[0]
	if cookie.MaxAge >= 0 {
		t.Errorf("Clear should set MaxAge<0, got %d", cookie.MaxAge)
	}
	if cookie.Value != "" {
		t.Errorf("Clear should empty value, got %q", cookie.Value)
	}
}
