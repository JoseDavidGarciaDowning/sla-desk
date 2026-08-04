package app

import (
	"context"

	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
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
// It satisfies tickethttp.CallerResolver, which is the contract the ticket
// module declared for exactly this. That module never learns where a caller
// comes from, and the identity module never learns what one is used for.
func callerFromContext(ctx context.Context) (tickethttp.Caller, bool) {
	user, ok := identityhttp.UserFromContext(ctx)
	if !ok {
		return tickethttp.Caller{}, false
	}

	return tickethttp.Caller{
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
