package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// This file is where the identity module's answers are translated into the
// vocabulary the ticket handlers and the store work in. It is the only place in
// this package that names the identity module at all.
//
// It exists here because internal/api is still the composition point. When
// internal/app takes that job, this file moves there unchanged in substance —
// which is the test of whether the translation is real work or ceremony.

// Caller is the authenticated user as this package's handlers need them.
type Caller struct {
	// ID in the driver's shape, because that is what the generated queries take
	// as a parameter. The identity module hands back a uuid.UUID so that its
	// own domain does not have to import a database driver; converting here is
	// the cost of that, and it is one line.
	ID pgtype.UUID

	// Role is what this actor was at the moment they acted, denormalised onto
	// the audit trail. See actorRole.
	Role ticket.Role
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
		ID:   pgUUID(user.ID),
		Role: actorRole(user.Role),
	}, true
}

// pgUUID converts a domain identifier into the driver's representation.
//
// Valid is unconditionally true: a uuid.UUID has no null state, so a value that
// arrived here is a value. The zero UUID is a legitimate value the database
// will reject on its own if it is wrong, and marking it invalid instead would
// turn a foreign key violation into a confusing NULL.
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// actorRole maps an identity role onto a ticket actor role.
//
// Exhaustive rather than a bare string conversion, and that is the point. The
// two types hold the same three strings today, but they answer different
// questions: identity.Role is what someone *is*, and ticket.Role is what they
// *were when they acted*. A role added to the identity module and not accounted
// for here would otherwise flow into ticket_status_history as a string the
// CHECK constraint rejects — a 500 at write time, on a path only exercised by
// whoever holds the new role.
//
// Failing to the least privileged role keeps that a permission error instead,
// which is a bug someone reports rather than one that corrupts a write.
func actorRole(r identitydomain.Role) ticket.Role {
	switch r {
	case identitydomain.RoleAdmin:
		return ticket.RoleAdmin
	case identitydomain.RoleAgent:
		return ticket.RoleAgent
	case identitydomain.RoleCustomer:
		return ticket.RoleCustomer
	default:
		return ticket.RoleCustomer
	}
}
