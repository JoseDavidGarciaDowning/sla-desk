// Package auth turns a Clerk identity into one of our users.
//
// The split is the point (docs/spec.md §4.3): Clerk answers who you are, this
// package answers what you may do by reading the role from our own users table.
// A role claim coming from a client is not a role.
package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/clerk/clerk-sdk-go/v2"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
)

// Provisioner is the slice of the store this package needs. Declared here
// rather than in store because the consumer owns the interface (docs/spec.md
// §8); *store.Queries satisfies it as generated.
type Provisioner interface {
	GetUserByClerkID(ctx context.Context, clerkUserID string) (store.User, error)
	UpsertUserFromClerk(ctx context.Context, arg store.UpsertUserFromClerkParams) (store.User, error)
}

// Identity is what Clerk knows about a person that their session token does not
// carry. The token holds the subject and nothing else we need.
type Identity struct {
	Email string
	Name  string
}

// IdentityFetcher reads a user from Clerk's Backend API. Only ever called for a
// subject we have no row for, which is once per user in the life of the system.
type IdentityFetcher interface {
	FetchIdentity(ctx context.Context, clerkUserID string) (Identity, error)
}

// RequireAuth rejects any request that did not arrive with a verified Clerk
// session, and resolves the ones that did into one of our users.
//
// It exists because clerkhttp.WithHeaderAuthorization does not reject anything.
// It attaches claims when a token verifies and otherwise passes the request
// through untouched, so mounting it alone leaves an endpoint open.
func RequireAuth(p Provisioner, f IdentityFetcher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := clerk.SessionClaimsFromContext(r.Context())
			if !ok || claims == nil {
				httperr.Write(w, http.StatusUnauthorized, "authentication required")
				return
			}

			row, err := resolve(r.Context(), p, f, claims.Subject)
			if err != nil {
				// WriteInternal, not Write with the cause. resolve wraps
				// whatever pgx or Clerk's SDK returned, and those carry host
				// names, ports and table names.
				httperr.WriteInternal(w)
				return
			}

			next.ServeHTTP(w, r.WithContext(contextWithUser(r.Context(), userFromRow(row))))
		})
	}
}

// resolve turns a verified Clerk subject into one of our users, creating the
// row if this is the first time we have seen them.
//
// This is the fallback half of docs/spec.md §4.5. The webhook is the primary
// path, but the browser holds a valid token the instant signup completes and
// the webhook may still be seconds away, so every new user's first request
// would otherwise fail. Both paths call the same idempotent upsert; whichever
// arrives first wins and the other is a no-op.
func resolve(ctx context.Context, p Provisioner, f IdentityFetcher, subject string) (store.User, error) {
	row, err := p.GetUserByClerkID(ctx, subject)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, err
	}

	// Only reached once per user. The session token carries the subject and
	// nothing else, so the address has to come from Clerk itself.
	identity, err := f.FetchIdentity(ctx, subject)
	if err != nil {
		return store.User{}, err
	}

	return Provision(ctx, p, subject, identity)
}
