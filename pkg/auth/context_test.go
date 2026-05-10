package auth

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestWithAndGetIdentity(t *testing.T) {
	ctx := context.Background()
	if got := GetIdentity(ctx); got != nil {
		t.Errorf("GetIdentity on empty ctx = %v, want nil", got)
	}
	id := &Identity{Subject: "alice", AuthType: "oidc"}
	ctx = WithIdentity(ctx, id)
	got := GetIdentity(ctx)
	if got != id {
		t.Errorf("GetIdentity = %v, want %v (same pointer)", got, id)
	}
}

func TestRedactHeaders_StripsSensitiveValuesPreservesNames(t *testing.T) {
	h := http.Header{
		"Authorization":       {"Bearer abc.def.ghi"},
		"Proxy-Authorization": {"Basic xxx"},
		"Cookie":              {"sid=secret"},
		"Set-Cookie":          {"sid=secret; HttpOnly"},
		"X-Api-Key":           {"plaintext-key"},
		"Content-Type":        {"application/json"},
		"X-Request-Id":        {"req-1", "req-2"},
	}
	out := RedactHeaders(h)
	for _, k := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key"} {
		if got := out.Get(k); got != "[redacted]" {
			t.Errorf("%s header = %q, want \"[redacted]\"", k, got)
		}
	}
	if got := out.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want preserved", got)
	}
	if got := out["X-Request-Id"]; !reflect.DeepEqual(got, []string{"req-1", "req-2"}) {
		t.Errorf("X-Request-Id = %v, want both values preserved", got)
	}
}

func TestRedactHeaders_NilInputReturnsNil(t *testing.T) {
	if got := RedactHeaders(nil); got != nil {
		t.Errorf("RedactHeaders(nil) = %v, want nil", got)
	}
}

func TestRedactHeaders_DoesNotMutateInput(t *testing.T) {
	h := http.Header{"Authorization": {"Bearer secret"}}
	_ = RedactHeaders(h)
	if got := h.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("input header mutated: got %q", got)
	}
}

func TestWithAndGetHeaders(t *testing.T) {
	ctx := context.Background()
	if got := GetHeaders(ctx); got != nil {
		t.Errorf("GetHeaders on empty ctx = %v, want nil", got)
	}
	in := http.Header{"Authorization": {"Bearer s"}, "X-Trace": {"t1"}}
	ctx = WithHeaders(ctx, in)
	got := GetHeaders(ctx)
	if got.Get("Authorization") != "[redacted]" {
		t.Errorf("stashed Authorization = %q, want [redacted]", got.Get("Authorization"))
	}
	if got.Get("X-Trace") != "t1" {
		t.Errorf("stashed X-Trace = %q, want t1", got.Get("X-Trace"))
	}
}

func TestWithAndGetRequestID(t *testing.T) {
	if got := GetRequestID(context.Background()); got != "" {
		t.Errorf("GetRequestID on empty ctx = %q, want empty", got)
	}
	ctx := WithRequestID(context.Background(), "req-42")
	if got := GetRequestID(ctx); got != "req-42" {
		t.Errorf("GetRequestID = %q, want req-42", got)
	}
}

func TestWithAndGetRemoteAddr(t *testing.T) {
	if got := GetRemoteAddr(context.Background()); got != "" {
		t.Errorf("GetRemoteAddr on empty ctx = %q, want empty", got)
	}
	ctx := WithRemoteAddr(context.Background(), "10.0.0.1:9999")
	if got := GetRemoteAddr(ctx); got != "10.0.0.1:9999" {
		t.Errorf("GetRemoteAddr = %q, want 10.0.0.1:9999", got)
	}
}

func TestAnonymousIdentity(t *testing.T) {
	a := Anonymous()
	if a.Subject != "anonymous" || a.AuthType != "anonymous" {
		t.Errorf("Anonymous() = %+v, want subject/authtype = anonymous", a)
	}
}
