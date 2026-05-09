package inbound

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/plexara/api-test/pkg/config"
)

// ErrNoCredential is returned when the request carried no credential the
// authenticator recognizes (no Authorization header, no X-API-Key header,
// no api_key query). Distinct from a credential that was supplied but
// wrong; chain.go decides what to do with each (anonymous fallback vs
// 401).
var ErrNoCredential = errors.New("inbound: no credential")

// ErrInvalidCredential is returned when a credential was supplied but did
// not validate (wrong key, expired token, bad signature). The auth chain
// converts this into a 401.
var ErrInvalidCredential = errors.New("inbound: invalid credential")

// APIKeyStore is the lookup interface the API-key authenticator uses.
// Both the file-backed map and the bcrypt-backed Postgres store
// (pkg/apikeys) implement it.
type APIKeyStore interface {
	// LookupAPIKey returns the key name on success, ErrInvalidCredential
	// when the plaintext doesn't match anything, or another error for
	// transport failures. Implementations must be safe under concurrent
	// use.
	LookupAPIKey(ctx context.Context, plaintext string) (name string, err error)
}

// APIKeyAuthenticator validates X-API-Key (header) and ?api_key= (query)
// against an APIKeyStore.
type APIKeyAuthenticator struct {
	store      APIKeyStore
	headerName string
	queryParam string
}

// NewAPIKey returns an APIKeyAuthenticator. headerName and queryParam
// default to "X-API-Key" and "api_key" when empty.
func NewAPIKey(store APIKeyStore, headerName, queryParam string) *APIKeyAuthenticator {
	if headerName == "" {
		headerName = "X-API-Key"
	}
	if queryParam == "" {
		queryParam = "api_key"
	}
	return &APIKeyAuthenticator{store: store, headerName: headerName, queryParam: queryParam}
}

// Authenticate implements Authenticator.
//
// Preference order: header first, query second. If both are present and
// differ, the header wins (a malicious or curious caller can't override
// the credential by appending a query param).
func (a *APIKeyAuthenticator) Authenticate(ctx context.Context, r *http.Request) (*Identity, error) {
	candidate := strings.TrimSpace(r.Header.Get(a.headerName))
	if candidate == "" {
		candidate = strings.TrimSpace(r.URL.Query().Get(a.queryParam))
	}
	if candidate == "" {
		return nil, ErrNoCredential
	}
	name, err := a.store.LookupAPIKey(ctx, candidate)
	if err != nil {
		return nil, err
	}
	return &Identity{
		Subject:  name,
		AuthType: "apikey",
		KeyName:  name,
	}, nil
}

// FileAPIKeyStore is an in-memory APIKeyStore backed by config.FileAPIKey
// entries. Lookup is constant-time (subtle.ConstantTimeCompare per row)
// to avoid timing-side-channel guesses.
type FileAPIKeyStore struct {
	keys []config.FileAPIKey
}

// NewFileAPIKeyStore returns a FileAPIKeyStore over the provided keys.
func NewFileAPIKeyStore(keys []config.FileAPIKey) *FileAPIKeyStore {
	cp := make([]config.FileAPIKey, len(keys))
	copy(cp, keys)
	return &FileAPIKeyStore{keys: cp}
}

// LookupAPIKey implements APIKeyStore.
func (s *FileAPIKeyStore) LookupAPIKey(_ context.Context, candidate string) (string, error) {
	if candidate == "" {
		return "", ErrInvalidCredential
	}
	cb := []byte(candidate)
	// Walk every entry so timing doesn't leak which entry matched.
	matched := ""
	for _, k := range s.keys {
		if subtle.ConstantTimeCompare(cb, []byte(k.Key)) == 1 {
			matched = k.Name
		}
	}
	if matched == "" {
		return "", ErrInvalidCredential
	}
	return matched, nil
}

// CombineAPIKeyStores returns an APIKeyStore that consults each store in
// order; the first non-error hit wins. Used by the chain to layer the
// file store + the DB-backed bcrypt store.
func CombineAPIKeyStores(stores ...APIKeyStore) APIKeyStore {
	return &combined{stores: stores}
}

type combined struct {
	stores []APIKeyStore
}

func (c *combined) LookupAPIKey(ctx context.Context, plaintext string) (string, error) {
	var lastErr error
	for _, s := range c.stores {
		name, err := s.LookupAPIKey(ctx, plaintext)
		if err == nil {
			return name, nil
		}
		// Both ErrInvalidCredential and transport errors continue the
		// loop; only ErrInvalidCredential is treated as "next store can
		// still match" — transport errors are bubbled up after the loop.
		if !errors.Is(err, ErrInvalidCredential) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrInvalidCredential
}
