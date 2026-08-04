package api

import (
	"context"

	"github.com/google/uuid"

	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// This file is where the identity module's answers are translated into the
// vocabulary the ticket handlers work in. It is the only place in this package
// that names both modules.
//
// It exists here because internal/api is still the composition point. When
// internal/app takes that job, this file moves there unchanged in substance —
// which is the test of whether the translation is real work or ceremony.

// Caller is the authenticated user as this package's handlers need them.
type Caller struct {
	// ID needs no conversion any more. Both modules carry uuid.UUID, because
	// neither domain may import a database driver — the pgtype.UUID that used
	// to be translated here was a property of the generated code, and it stopped
	// leaking out of it when the ticket module got its own sqlc config.
	ID uuid.UUID

	// Role is what this actor was at the moment they acted, denormalised onto
	// the audit trail. See actorRole.
	Role ticketdomain.Role
}

// callerFromContext reads the authenticated user and converts them.
//
// The second result is false on any request that did not pass through the
// identity module's middleware, which is the only thing that puts a user there.
func callerFromContext(ctx context.Context) (Caller, bool) {
	user, ok := identityhttp.UserFromContext(ctx)
	if !ok {
		return Caller{}, false
	}

	return Caller{
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
