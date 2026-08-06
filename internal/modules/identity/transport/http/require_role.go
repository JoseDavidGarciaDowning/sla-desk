package http

import (
	"net/http"
	"slices"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
)

// RequireRole refuses any caller whose role is not one of the listed ones.
//
// It is the second half of the authorization story, and it exists because the
// first half stops working in slice 2. Until now every read answered one
// question — is this yours? — and the answer was a requester_id predicate in
// the SQL, so a forgotten check in Go could not leak another customer's ticket
// (docs/spec.md §4.3). An agent reads tickets that are not theirs, so those
// queries have no predicate to carry the guarantee, and something else has to.
//
// That something is where the route is mounted. The queries with no predicate
// are reachable only from handlers inside a group carrying this middleware, and
// the alternative — passing the role down into the query and skipping the
// predicate for agents — was rejected: a boolean that disables a security
// predicate is a boolean that can be wrong, and T14a's mutation testing already
// caught that exact shape once. See tasks/slice-2/plan.md decisions B and C.
//
// It must run after RequireAuth. The role it reads is the one on our users row,
// put in the context by that middleware; the session claims are not consulted
// here and a role claim in a token is not a role (§4.3).
func RequireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user, ok := UserFromContext(req.Context())
			if !ok {
				// 401 rather than 403. No user in the context means the request
				// never passed through RequireAuth, which is a wiring mistake
				// and not a permission problem — answering 403 would tell an
				// unauthenticated caller they are signed in as the wrong
				// person. Failing closed on it is what makes a route mounted
				// outside the authenticated group refuse rather than admit.
				httperr.Write(w, http.StatusUnauthorized, "authentication required")
				return
			}

			// An empty list admits nobody. RequireRole() with the arguments
			// forgotten has to fail closed: a guard that lets everything
			// through while looking mounted is precisely the trap
			// clerkhttp.WithHeaderAuthorization sets (§4.3), and this package
			// exists because of it.
			if !slices.Contains(roles, user.Role) {
				// The detail names neither the caller's role nor the ones that
				// would have worked. It tells someone how to describe an
				// account worth attacking, and withholding it costs the honest
				// caller nothing: they cannot pass either way.
				httperr.Write(w, http.StatusForbidden, "this endpoint is not available to you")
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}
