// Package postgres is the identity module's database adapter.
//
// It wraps the generated queries so nothing above it names a driver type, and
// so the module's sentinel errors mean the same thing whatever the driver does.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
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

// ByID reads by our own primary key, translating "no rows" the same way
// ByClerkID does.
func (r *UserRepository) ByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.User{}, application.ErrNoSuchUser
	case err != nil:
		return domain.User{}, fmt.Errorf("reading the user: %w", err)
	}
	return userFrom(row), nil
}

// Assignable lists the users a ticket may be handed to.
//
// An empty result is an empty slice and not an error: a deploy with no agents
// configured is the default state (T17), and that is a fact about the roster
// rather than a failure to read it.
func (r *UserRepository) Assignable(ctx context.Context) ([]domain.User, error) {
	rows, err := r.q.ListAssignableUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the assignable users: %w", err)
	}

	out := make([]domain.User, len(rows))
	for i, row := range rows {
		out[i] = userFrom(row)
	}
	return out, nil
}

// Upsert is the idempotent provisioning write from docs/spec.md §4.5.
//
// The role applies to the insert only — the query omits it from the conflict
// clause — so refreshing an existing user's name can change neither what they
// are nor what they may do. What fills the parameter is our own configuration
// and nothing else: a webhook payload has no role field to be read from, and a
// token claim never reaches this package.
func (r *UserRepository) Upsert(ctx context.Context, clerkUserID string, id domain.Identity, role domain.Role) (domain.User, error) {
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
		Role:        role,
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("provisioning the user: %w", err)
	}
	return userFrom(row), nil
}

// GrantRole raises an existing user to a granted role.
//
// The query refuses to write 'customer', so this method structurally cannot
// demote anyone — see the SQL for why that guarantee is there rather than in
// the caller. A refused write returns no rows, which reads exactly like a
// missing user, and both surface as ErrNoSuchUser: in either case nothing was
// written, and handing back a row would say otherwise.
func (r *UserRepository) GrantRole(ctx context.Context, clerkUserID string, role domain.Role) (domain.User, error) {
	row, err := r.q.GrantUserRole(ctx, identitydb.GrantUserRoleParams{
		ClerkUserID: clerkUserID,
		Role:        role,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.User{}, application.ErrNoSuchUser
	case err != nil:
		return domain.User{}, fmt.Errorf("granting the role: %w", err)
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
