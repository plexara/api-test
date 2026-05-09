package apikeys

import (
	"context"
	"errors"

	"github.com/plexara/api-test/pkg/auth/inbound"
)

// AsInboundStore adapts the bcrypt-backed Store to the inbound.APIKeyStore
// interface so the inbound auth chain can layer it under the file-backed
// store.
func (s *Store) AsInboundStore() inbound.APIKeyStore { return inboundAdapter{s: s} }

type inboundAdapter struct{ s *Store }

func (a inboundAdapter) LookupAPIKey(ctx context.Context, plaintext string) (string, error) {
	k, err := a.s.Authenticate(ctx, plaintext)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", inbound.ErrInvalidCredential
		}
		return "", err
	}
	return k.Name, nil
}
