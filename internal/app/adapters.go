package app

import (
	"context"

	"github.com/google/uuid"

	identityapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	ticketports "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// This file is where the identity module's answers are translated into the
// vocabulary the ticket handlers work in. It is the only place in this package
// that names both modules.
//
// It moved here from internal/api unchanged in substance, which was the test
// set for it when it was written: a translation that had to be rewritten when
// the composition point moved would have been ceremony rather than work.

// callerFromContext reads the authenticated user and converts them.
//
// The second result is false on any request that did not pass through the
// identity module's middleware, which is the only thing that puts a user there.
// It satisfies ticketports.CallerResolver, which is the contract the ticket
// module declared for exactly this. That module never learns where a caller
// comes from, and the identity module never learns what one is used for.
func callerFromContext(ctx context.Context) (ticketports.Caller, bool) {
	user, ok := identityhttp.UserFromContext(ctx)
	if !ok {
		return ticketports.Caller{}, false
	}

	return ticketports.Caller{
		ID:   user.ID,
		Role: actorRole(user.Role),
	}, true
}

// actorRole maps an identity role onto a ticket actor role.
//
// Exhaustive rather than a bare string conversion, and that is the point. The
// two types hold the same three strings today, but they answer different
// questions: identity.Role is what someone *is*, and ticketdomain.Role is what they
// *were when they acted*. A role added to the identity module and not accounted
// for here would otherwise flow into ticket_status_history as a string the
// CHECK constraint rejects — a 500 at write time, on a path only exercised by
// whoever holds the new role.
//
// Failing to the least privileged role keeps that a permission error instead,
// which is a bug someone reports rather than one that corrupts a write.
func actorRole(r identitydomain.Role) ticketdomain.Role {
	switch r {
	case identitydomain.RoleAdmin:
		return ticketdomain.RoleAdmin
	case identitydomain.RoleAgent:
		return ticketdomain.RoleAgent
	case identitydomain.RoleCustomer:
		return ticketdomain.RoleCustomer
	default:
		return ticketdomain.RoleCustomer
	}
}

// AssigneeDirectory answers the ticket module's question about who may hold a
// ticket, using the identity module's answer about roles.
//
// The same shape as SLAPolicies above, and for the same reason: the ticket
// module states what it needs in its own words — "may this person hold
// tickets" — and never learns that the answer is a role, or that a module
// called identity exists (docs/adr/0005). This file is the one place allowed to
// know both.
type AssigneeDirectory struct {
	Users *identityapp.Service
}

var _ ticketports.AssigneeDirectory = AssigneeDirectory{}

func (d AssigneeDirectory) CanHoldTickets(ctx context.Context, id uuid.UUID) (bool, error) {
	return d.Users.MayHoldTickets(ctx, id)
}
