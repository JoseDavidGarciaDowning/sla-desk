// Package provision turns a Clerk subject into one of our users.
//
// Two ways in, one row out. The webhook is the primary path (docs/spec.md
// §4.5) and calls Provision with the identity already in the payload; the
// authentication middleware is the fallback and calls EnsureUser, which
// fetches from Clerk only on a miss.
//
// They are one feature rather than two because they are racing paths onto the
// same upsert: whichever arrives first wins and the other is a no-op. Splitting
// them would put the grant logic they share in a third place, and if only one
// of them consulted the grants then whether a listed agent got their role would
// depend on which request arrived first.
package provision

import (
	"context"
	"errors"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
)

// Users is the persistence this use case needs.
type Users interface {
	// ByClerkID returns domain.ErrNoSuchUser when there is no row, and only
	// then.
	ByClerkID(ctx context.Context, clerkUserID string) (domain.User, error)

	// Upsert is the idempotent provisioning write.
	//
	// The role applies to the insert only, and the query leaves the column out
	// of its conflict clause. That is what keeps re-running this on an existing
	// user harmless: refreshing someone's name must not be able to change what
	// they are allowed to do. Promotion is GrantRole's job and nothing else's.
	Upsert(ctx context.Context, clerkUserID string, id domain.Identity, role domain.Role) (domain.User, error)

	// GrantRole raises an existing user to a role the operator granted them.
	//
	// It cannot take one away: the query refuses to write 'customer', so the
	// only thing this method can do is promote. That guarantee lives in the SQL
	// rather than in the caller, because a rule enforced by an if statement is
	// a rule that the next caller can forget to write.
	GrantRole(ctx context.Context, clerkUserID string, role domain.Role) (domain.User, error)
}

// Identities reads what Clerk knows about a person.
//
// Only ever called for a subject we have no row for, which is once per user in
// the life of the system: a session token carries the subject and nothing else
// we need.
type Identities interface {
	FetchIdentity(ctx context.Context, clerkUserID string) (domain.Identity, error)
}

// Handler executes the use case.
type Handler struct {
	users  Users
	ids    Identities
	grants domain.RoleGrants
}

// New builds the use case. The grants are a value rather than a collaborator
// because they answer from memory and never fail: they are read once from the
// environment at startup, and a lookup on every authenticated request must not
// be able to add a network hop or an error path.
func New(users Users, ids Identities, grants domain.RoleGrants) *Handler {
	return &Handler{users: users, ids: ids, grants: grants}
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
//
// It is what satisfies the middleware's UserSource. That interface is declared
// in transport, names only domain types, and is satisfied structurally — so
// transport never imports this package and there is no cycle to unpick. Where
// the ticket module's ports name feature-owned command structs, this one does
// not, which is the whole difference (docs/adr/0012).
func (h *Handler) EnsureUser(ctx context.Context, subject string) (domain.User, error) {
	user, err := h.users.ByClerkID(ctx, subject)
	if err == nil {
		return h.applyGrant(ctx, user)
	}
	if !errors.Is(err, domain.ErrNoSuchUser) {
		return domain.User{}, err
	}

	// Only reached once per user.
	id, err := h.ids.FetchIdentity(ctx, subject)
	if err != nil {
		return domain.User{}, err
	}

	return h.users.Upsert(ctx, subject, id, h.grants.RoleFor(subject))
}

// Provision writes the users row for a Clerk identity, creating it if this is
// the first time we have seen them and refreshing the details if it is not.
//
// The webhook calls this directly, having been handed the identity in the event
// payload rather than having to fetch it.
//
// It applies the same grant EnsureUser does, and that is not duplication for
// its own sake: the two are racing paths onto the same row (docs/spec.md §4.5),
// and if only one of them consulted the grants then whether a listed agent got
// their role would depend on which request arrived first.
//
// The grant is read from our configuration and not from id, which is why the
// role cannot be forged: domain.Identity has no role field for a webhook
// payload to put one in.
func (h *Handler) Provision(ctx context.Context, clerkUserID string, id domain.Identity) (domain.User, error) {
	user, err := h.users.Upsert(ctx, clerkUserID, id, h.grants.RoleFor(clerkUserID))
	if err != nil {
		return domain.User{}, err
	}
	return h.applyGrant(ctx, user)
}

// applyGrant raises a user to the role the operator granted them, and is a
// no-op for everyone else.
//
// It exists because a Clerk subject cannot be known before that person signs
// up, so the ordinary case is granting a role to someone who already has a row.
// EnsureUser runs on every authenticated request, which is what makes the
// promotion land on their next one rather than requiring a deploy.
//
// The two early returns are not an optimisation. The first is what stops every
// request an agent makes from becoming a write; the second is what stops an id
// removed from the list — or a typo in it — from demoting anyone. Taking a role
// away is an explicit action, and it belongs to the admin surface in slice 9.
func (h *Handler) applyGrant(ctx context.Context, user domain.User) (domain.User, error) {
	granted := h.grants.RoleFor(user.ClerkUserID)
	if granted == domain.RoleCustomer || granted == user.Role {
		return user, nil
	}
	return h.users.GrantRole(ctx, user.ClerkUserID, granted)
}
