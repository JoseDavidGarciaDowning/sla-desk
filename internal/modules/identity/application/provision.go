// Package application turns a verified Clerk subject into one of our users.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
)

// ErrUserNotFound means we hold no row for that Clerk subject.
//
// A sentinel rather than pgx.ErrNoRows leaking upward: Resolve has to tell "we
// have never seen this person" apart from "the database is broken", and only
// the first is worth a round trip to Clerk.
var ErrUserNotFound = errors.New("identity: no user for that clerk subject")

// UserRepository stores and reads our users.
//
// Declared here because this package is the consumer (docs/spec.md §8), and it
// deals in domain values so nothing above it ever sees a database row.
type UserRepository interface {
	// ByClerkID returns ErrUserNotFound when we hold no row.
	ByClerkID(ctx context.Context, clerkUserID string) (domain.User, error)

	// Upsert creates the row or refreshes its details, idempotently.
	//
	// The role is not a parameter, and that is the point: the query writes it
	// as a literal on insert and omits it from the conflict clause.
	Upsert(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error)
}

// IdentityProvider reads a user from Clerk.
//
// Only ever called for a subject we have no row for, which is once per user in
// the life of the system, so the round trip does not sit on the hot path.
type IdentityProvider interface {
	Fetch(ctx context.Context, clerkUserID string) (domain.Identity, error)
}

// Service provisions users.
type Service struct {
	users      UserRepository
	identities IdentityProvider
}

func NewService(users UserRepository, identities IdentityProvider) *Service {
	return &Service{users: users, identities: identities}
}

// Provision writes the users row for a Clerk identity, creating it if this is
// the first time we have seen them and refreshing the details if it is not.
//
// Both paths in docs/spec.md §4.5 end here: the Clerk webhook, which is the
// primary one, and the lazy fallback in Resolve that covers the window where a
// browser already holds a valid token and the webhook has not landed yet.
// Whichever arrives first creates the row; the other updates it.
func (s *Service) Provision(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	user, err := s.users.Upsert(ctx, clerkUserID, id)
	if err != nil {
		return domain.User{}, fmt.Errorf("provisioning %s: %w", clerkUserID, err)
	}
	return user, nil
}

// Resolve turns a verified Clerk subject into one of our users, creating the
// row if this is the first time we have seen them.
//
// This is the fallback half of docs/spec.md §4.5. The webhook is the primary
// path, but the browser holds a valid token the instant signup completes and
// the webhook may still be seconds away, so every new user's first request
// would otherwise fail. Both paths call the same idempotent upsert; whichever
// arrives first wins and the other is a no-op.
func (s *Service) Resolve(ctx context.Context, subject string) (domain.User, error) {
	user, err := s.users.ByClerkID(ctx, subject)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return domain.User{}, err
	}

	// Only reached once per user. The session token carries the subject and
	// nothing else, so the address has to come from Clerk itself.
	id, err := s.identities.Fetch(ctx, subject)
	if err != nil {
		return domain.User{}, err
	}

	return s.Provision(ctx, subject, id)
}
