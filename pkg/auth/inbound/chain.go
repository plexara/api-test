package inbound

import (
	"context"
	"errors"
	"net/http"
)

// Authenticator validates a single credential type and returns the
// resolved Identity on success, ErrNoCredential when the request didn't
// carry the credential type at all (the chain should try the next), or
// any other error to abort with 401.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (*Identity, error)
}

// Chain is an ordered list of Authenticators. The first one that finds
// its credential type in the request decides the outcome:
//   - returns Identity on validation success
//   - returns ErrInvalidCredential on validation failure (no fallthrough)
//   - returns ErrNoCredential to advance to the next authenticator
//
// When every authenticator returns ErrNoCredential and AllowAnonymous is
// true, the chain returns Anonymous(); otherwise ErrNoCredential bubbles
// out and the caller should respond 401.
type Chain struct {
	authenticators []Authenticator
	allowAnonymous bool
}

// NewChain returns a Chain. allowAnonymous controls the no-credential
// fallback (true → anonymous Identity, false → ErrNoCredential).
func NewChain(allowAnonymous bool, auths ...Authenticator) *Chain {
	out := make([]Authenticator, 0, len(auths))
	for _, a := range auths {
		if a != nil {
			out = append(out, a)
		}
	}
	return &Chain{authenticators: out, allowAnonymous: allowAnonymous}
}

// Authenticate walks the chain. See Chain doc for semantics.
func (c *Chain) Authenticate(ctx context.Context, r *http.Request) (*Identity, error) {
	for _, a := range c.authenticators {
		id, err := a.Authenticate(ctx, r)
		if err == nil {
			return id, nil
		}
		if errors.Is(err, ErrNoCredential) {
			continue
		}
		return nil, err
	}
	if c.allowAnonymous {
		return Anonymous(), nil
	}
	return nil, ErrNoCredential
}

// AllowAnonymous reports whether the chain falls back to anonymous when
// no credential matches. Used by the inbound middleware to decide whether
// 401 is appropriate.
func (c *Chain) AllowAnonymous() bool { return c.allowAnonymous }
