// Package postgres stores and reads our users.
//
// It is the only package in this module that knows users is a table.
// Everything above it receives domain.User values, so adding a column stops
// here rather than changing the type every handler reads.
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

func (r *UserRepository) ByClerkID(ctx context.Context, clerkUserID string) (domain.User, error) {
	row, err := r.q.GetUserByClerkID(ctx, clerkUserID)
	if err != nil {
		// Translated at the boundary rather than passed through. Callers above
		// must not have to know that pgx exists in order to tell "never seen
		// this person" apart from "the database is broken".
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf("%w: %s", application.ErrUserNotFound, clerkUserID)
		}
		return domain.User{}, fmt.Errorf("reading user %s: %w", clerkUserID, err)
	}
	return userFrom(row), nil
}

func (r *UserRepository) Upsert(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	// An empty name becomes NULL rather than a blank string: Clerk often holds
	// no name at all, and "" is not a name anyone has.
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
		return domain.User{}, fmt.Errorf("upserting user %s: %w", clerkUserID, err)
	}
	return userFrom(row), nil
}

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
