// Package postgres is the identity module's database adapter.
//
// It wraps the generated queries so nothing above it names a driver type, and
// so the module's sentinel errors mean the same thing whatever the driver does.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres/identitydb"
)

// UserRepository reads and writes the users table.
type UserRepository struct {
	q *identitydb.Queries
}

var _ application.UserRepository = (*UserRepository)(nil)

func NewUserRepository(db identitydb.DBTX) *UserRepository {
	return &UserRepository{q: identitydb.New(db)}
}

// ByClerkID translates pgx.ErrNoRows into the module's own sentinel.
//
// Without this the application layer would have to match on a driver error,
// which is the coupling the port exists to avoid — and every caller would have
// to remember that "no rows" is not a failure here but the ordinary first-visit
// case.
func (r *UserRepository) ByClerkID(ctx context.Context, clerkUserID string) (domain.User, error) {
	row, err := r.q.GetUserByClerkID(ctx, clerkUserID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.User{}, application.ErrNoSuchUser
	case err != nil:
		return domain.User{}, fmt.Errorf("reading the user: %w", err)
	}
	return userFrom(row), nil
}

// Upsert is the idempotent provisioning write from docs/spec.md §4.5.
//
// The role is not a parameter, and that is the point: the query writes it as a
// literal on insert and omits it from the conflict clause, so neither a webhook
// payload nor a first request can create an agent or demote one.
func (r *UserRepository) Upsert(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	// An empty name becomes NULL rather than a blank string. Clerk often holds
	// no name at all — a user who signed up with an email and a password has
	// given us nothing else.
	var name *string
	if id.Name != "" {
		name = &id.Name
	}

	row, err := r.q.UpsertUserFromClerk(ctx, identitydb.UpsertUserFromClerkParams{
		ClerkUserID: clerkUserID,
		Email:       id.Email,
		Name:        name,
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("provisioning the user: %w", err)
	}
	return userFrom(row), nil
}

// userFrom maps a row onto the domain entity.
//
// Deliberately not returning identitydb.User. Nothing above this package should
// depend on the shape of the table: adding a column must not change the type
// every handler reads out of the request context.
func userFrom(row identitydb.User) domain.User {
	var name string
	if row.Name != nil {
		name = *row.Name
	}

	return domain.User{
		ID:          row.ID,
		ClerkUserID: row.ClerkUserID,
		Email:       row.Email,
		Name:        name,
		Role:        row.Role,
	}
}
