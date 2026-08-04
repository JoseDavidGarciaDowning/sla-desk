// Package http is the identity module's HTTP surface: the middleware that
// turns a verified Clerk session into one of our users, and the webhook Clerk
// posts user events to.
//
// It mounts adapters from this module's infrastructure — the Clerk JWT
// middleware, the Svix signature verifier — because those are protocol details
// of the system this transport speaks to. That dependency stays inside the
// module and never crosses a boundary, which is the rule that matters.
package http

import (
	"context"
	"net/http"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/clerk"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
)

// RequireAuth rejects any request that did not arrive with a verified Clerk
// session, and resolves the ones that did into one of our users.
//
// It exists because clerk.Middleware does not reject anything. It attaches
// claims when a token verifies and otherwise passes the request through
// untouched, so mounting that alone leaves an endpoint open.
func RequireAuth(svc *application.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, ok := clerk.SubjectFromContext(r.Context())
			if !ok {
				httperr.Write(w, http.StatusUnauthorized, "authentication required")
				return
			}

			user, err := svc.Resolve(r.Context(), subject)
			if err != nil {
				// WriteInternal, not Write with the cause. Resolve wraps
				// whatever pgx or Clerk's SDK returned, and those carry host
				// names, ports and table names.
				httperr.WriteInternal(w)
				return
			}

			next.ServeHTTP(w, r.WithContext(ContextWithUser(r.Context(), user)))
		})
	}
}

// contextKey is an unexported struct type, so no other package can collide with
// this key or overwrite the authenticated user in the context.
type contextKey struct{}

// ContextWithUser attaches the authenticated caller.
//
// Exported so the composition root can build a request for a test without
// standing up Clerk. It cannot be used to forge a caller in production: the
// key is unexported, so only this package can produce a context the reader
// below will accept, and nothing routes through here but RequireAuth.
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
