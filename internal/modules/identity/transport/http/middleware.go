// Package http is the identity module's HTTP surface: the middleware that
// turns a verified token into one of our users, and the webhook Clerk posts
// user events to.
package http

import (
	"context"
	"net/http"

	clerksdk "github.com/clerk/clerk-sdk-go/v2"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
)

// UserSource is the slice of the module this middleware needs.
//
// Declared here rather than taking *application.Service, so a test can run the
// real middleware against a stub without building a service around a database.
//
// Named for the role it plays rather than with the usual -er suffix, because
// the method it carries is EnsureUser and "UserEnsurer" reads worse than the
// thing it names. The standard library does the same where the verb is awkward:
// see math/rand.Source.
type UserSource interface {
	EnsureUser(ctx context.Context, subject string) (domain.User, error)
}

// RequireAuth rejects any request that did not arrive with a verified Clerk
// session, and resolves the ones that did into one of our users.
//
// It exists because clerkhttp.WithHeaderAuthorization does not reject anything.
// It attaches claims when a token verifies and otherwise passes the request
// through untouched, so mounting it alone leaves an endpoint open.
func RequireAuth(users UserSource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			claims, ok := clerksdk.SessionClaimsFromContext(req.Context())
			if !ok || claims == nil {
				httperr.Write(w, http.StatusUnauthorized, "authentication required")
				return
			}

			user, err := users.EnsureUser(req.Context(), claims.Subject)
			if err != nil {
				// WriteInternal, not Write with the cause. EnsureUser wraps
				// whatever the driver or Clerk's SDK returned, and those carry
				// host names, ports and table names.
				httperr.WriteInternal(w)
				return
			}

			next.ServeHTTP(w, req.WithContext(ContextWithUser(req.Context(), user)))
		})
	}
}

// contextKey is an unexported struct type, so no other package can collide with
// this key or overwrite the authenticated user in the context.
type contextKey struct{}

// ContextWithUser attaches an authenticated user. Exported so a test can build
// a request that has already passed authentication without running Clerk.
func ContextWithUser(ctx context.Context, u domain.User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// UserFromContext returns the authenticated caller. The second result is false
// on any request that did not pass through RequireAuth, which is the only thing
// that puts a user there.
func UserFromContext(ctx context.Context) (domain.User, bool) {
	u, ok := ctx.Value(contextKey{}).(domain.User)
	return u, ok
}
