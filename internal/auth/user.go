package auth

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// User is the authenticated caller as the rest of the application sees them.
//
// Deliberately not store.User. Handlers should not depend on the shape of the
// users table: adding a column there must not change the type every handler
// reads out of the request context.
//
// ID stays a pgtype.UUID because that is what the generated queries take as a
// parameter, and converting to and from a string on every request would buy
// nothing. Mapping it to a friendlier UUID type means adding a dependency, and
// that decision belongs to the task that first serialises one.
type User struct {
	ID          pgtype.UUID
	ClerkUserID string
	Email       string
	Role        ticket.Role
}

func userFromRow(row store.User) User {
	return User{
		ID:          row.ID,
		ClerkUserID: row.ClerkUserID,
		Email:       row.Email,
		Role:        row.Role,
	}
}

// contextKey is an unexported struct type, so no other package can collide with
// this key or overwrite the authenticated user in the context.
type contextKey struct{}

func contextWithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// UserFromContext returns the authenticated caller. The second result is false
// on any request that did not pass through RequireAuth, which is the only thing
// that puts a user there.
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(contextKey{}).(User)
	return u, ok
}
