// Package application turns a verified Clerk subject into one of our users.
//
// Every interface here is declared by the consumer — this package — and never
// by whoever implements it (docs/spec.md §8). That is what keeps the module
// independently evolvable: it states what it needs in its own vocabulary, and
// the composition root finds something that can provide it.
package application

import (
	"context"
	"errors"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
)

// ErrNoSuchUser means we hold no row for that Clerk subject.
//
// Declared here rather than re-exported from the driver, because this package
// does not know what the store is. The repository translates pgx.ErrNoRows into
// this, so the provisioning path can tell "we have never seen them" apart from
// "the database is down" without importing a driver to do it.
var ErrNoSuchUser = errors.New("identity: no user for that Clerk subject")

// UserRepository stores and reads users.
type UserRepository interface {
	// ByClerkID returns ErrNoSuchUser when there is no row, and only then.
	ByClerkID(ctx context.Context, clerkUserID string) (domain.User, error)

	// Upsert is the idempotent provisioning write. It never sets or changes a
	// role: the query writes one literal on insert and leaves the column out of
	// its conflict clause.
	Upsert(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error)
}

// IdentityProvider reads what Clerk knows about a person.
//
// Only ever called for a subject we have no row for, which is once per user in
// the life of the system: a session token carries the subject and nothing else
// we need.
type IdentityProvider interface {
	FetchIdentity(ctx context.Context, clerkUserID string) (domain.Identity, error)
}

// Service is this module's use cases.
type Service struct {
	users UserRepository
	ids   IdentityProvider
}

func NewService(users UserRepository, ids IdentityProvider) *Service {
	return &Service{users: users, ids: ids}
}

// EnsureUser turns a verified Clerk subject into one of our users, creating the
// row if this is the first time we have seen them.
//
// Named for the write it may perform, not for the read it usually is. On the
// miss it calls Clerk over the network and inserts a row, and the middleware
// runs it on every authenticated request — so a caller reasoning about cost or
// about side effects has to be told, by the name, that both are on the table.
//
// This is the fallback half of docs/spec.md §4.5. The webhook is the primary
// path, but the browser holds a valid token the instant signup completes and
// the webhook may still be seconds away, so every new user's first request
// would otherwise fail. Both paths end at the same idempotent upsert; whichever
// arrives first wins and the other is a no-op.
func (s *Service) EnsureUser(ctx context.Context, subject string) (domain.User, error) {
	user, err := s.users.ByClerkID(ctx, subject)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, ErrNoSuchUser) {
		return domain.User{}, err
	}

	// Only reached once per user.
	id, err := s.ids.FetchIdentity(ctx, subject)
	if err != nil {
		return domain.User{}, err
	}

	return s.users.Upsert(ctx, subject, id)
}

// Provision writes the users row for a Clerk identity, creating it if this is
// the first time we have seen them and refreshing the details if it is not.
//
// The webhook calls this directly, having been handed the identity in the event
// payload rather than having to fetch it.
func (s *Service) Provision(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	return s.users.Upsert(ctx, clerkUserID, id)
}
