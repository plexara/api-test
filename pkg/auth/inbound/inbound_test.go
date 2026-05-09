package inbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plexara/api-test/pkg/config"
)

func TestFileAPIKeyStore_HitAndMiss(t *testing.T) {
	s := NewFileAPIKeyStore([]config.FileAPIKey{
		{Name: "alpha", Key: "AAA"},
		{Name: "beta", Key: "BBB"},
	})
	if name, err := s.LookupAPIKey(context.Background(), "AAA"); err != nil || name != "alpha" {
		t.Errorf("hit AAA: name=%q err=%v", name, err)
	}
	if _, err := s.LookupAPIKey(context.Background(), "ZZZ"); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("miss: err=%v want ErrInvalidCredential", err)
	}
	if _, err := s.LookupAPIKey(context.Background(), ""); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("empty: err=%v", err)
	}
}

func TestAPIKeyAuthenticator_HeaderAndQuery(t *testing.T) {
	store := NewFileAPIKeyStore([]config.FileAPIKey{{Name: "k1", Key: "secret"}})
	a := NewAPIKey(store, "X-API-Key", "api_key")

	t.Run("header hit", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-API-Key", "secret")
		id, err := a.Authenticate(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if id.Subject != "k1" || id.AuthType != "apikey" {
			t.Errorf("identity = %+v", id)
		}
	})
	t.Run("query hit", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/?api_key=secret", nil)
		id, err := a.Authenticate(context.Background(), r)
		if err != nil || id.Subject != "k1" {
			t.Errorf("got id=%+v err=%v", id, err)
		}
	})
	t.Run("header wins", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/?api_key=wrong", nil)
		r.Header.Set("X-API-Key", "secret")
		if _, err := a.Authenticate(context.Background(), r); err != nil {
			t.Errorf("header-wins err: %v", err)
		}
	})
	t.Run("no credential", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		_, err := a.Authenticate(context.Background(), r)
		if !errors.Is(err, ErrNoCredential) {
			t.Errorf("got err=%v want ErrNoCredential", err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-API-Key", "wrong")
		_, err := a.Authenticate(context.Background(), r)
		if !errors.Is(err, ErrInvalidCredential) {
			t.Errorf("got err=%v want ErrInvalidCredential", err)
		}
	})
}

func TestBearer_HitAndMiss(t *testing.T) {
	a := NewBearer([]config.FileBearerToken{{Name: "tok", Token: "abc123"}})

	t.Run("hit", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer abc123")
		id, err := a.Authenticate(context.Background(), r)
		if err != nil || id.Subject != "tok" || id.AuthType != "bearer" {
			t.Errorf("id=%+v err=%v", id, err)
		}
	})
	t.Run("case-insensitive scheme", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "bearer abc123")
		if _, err := a.Authenticate(context.Background(), r); err != nil {
			t.Errorf("err %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		_, err := a.Authenticate(context.Background(), r)
		if !errors.Is(err, ErrNoCredential) {
			t.Errorf("err %v", err)
		}
	})
	t.Run("non-bearer scheme", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		_, err := a.Authenticate(context.Background(), r)
		if !errors.Is(err, ErrNoCredential) {
			t.Errorf("err %v", err)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer nope")
		_, err := a.Authenticate(context.Background(), r)
		if !errors.Is(err, ErrInvalidCredential) {
			t.Errorf("err %v", err)
		}
	})
}

type stubAuth struct {
	id  *Identity
	err error
}

func (s stubAuth) Authenticate(context.Context, *http.Request) (*Identity, error) {
	return s.id, s.err
}

func TestChain_FirstMatchWins(t *testing.T) {
	c := NewChain(false,
		stubAuth{err: ErrNoCredential},
		stubAuth{id: &Identity{Subject: "x", AuthType: "apikey"}},
		stubAuth{id: &Identity{Subject: "y", AuthType: "bearer"}},
	)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id, err := c.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "x" {
		t.Errorf("got subject %q want x", id.Subject)
	}
}

func TestChain_InvalidStops(t *testing.T) {
	c := NewChain(true,
		stubAuth{err: ErrInvalidCredential},
		stubAuth{id: &Identity{Subject: "shouldNotReach"}},
	)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := c.Authenticate(context.Background(), r)
	if !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("err %v", err)
	}
}

func TestChain_AnonymousFallback(t *testing.T) {
	c := NewChain(true, stubAuth{err: ErrNoCredential})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id, err := c.Authenticate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if id.AuthType != "anonymous" {
		t.Errorf("authType = %q want anonymous", id.AuthType)
	}
}

func TestChain_NoAnonymousReturnsErr(t *testing.T) {
	c := NewChain(false, stubAuth{err: ErrNoCredential})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := c.Authenticate(context.Background(), r)
	if !errors.Is(err, ErrNoCredential) {
		t.Errorf("err %v want ErrNoCredential", err)
	}
}

func TestContext_Roundtrip(t *testing.T) {
	want := &Identity{Subject: "z", AuthType: "apikey"}
	ctx := WithIdentity(context.Background(), want)
	got := FromContext(ctx)
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
	if FromContext(context.Background()) != nil {
		t.Error("FromContext on bare ctx should return nil")
	}
}

func TestCombineAPIKeyStores(t *testing.T) {
	s1 := NewFileAPIKeyStore([]config.FileAPIKey{{Name: "a", Key: "AAA"}})
	s2 := NewFileAPIKeyStore([]config.FileAPIKey{{Name: "b", Key: "BBB"}})
	c := CombineAPIKeyStores(s1, s2)
	if name, err := c.LookupAPIKey(context.Background(), "AAA"); err != nil || name != "a" {
		t.Errorf("AAA: name=%q err=%v", name, err)
	}
	if name, err := c.LookupAPIKey(context.Background(), "BBB"); err != nil || name != "b" {
		t.Errorf("BBB: name=%q err=%v", name, err)
	}
	if _, err := c.LookupAPIKey(context.Background(), "ZZZ"); !errors.Is(err, ErrInvalidCredential) {
		t.Errorf("miss: err=%v", err)
	}
}
