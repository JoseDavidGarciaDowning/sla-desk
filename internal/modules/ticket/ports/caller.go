package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Caller is who is making the request, as this module needs to know them.
//
// Deliberately not the identity module's User. This module does not know that
// module exists: it says what it needs — an id to scope reads by, and a role to
// check transitions against — and the composition root is responsible for
// producing one. That contract is what keeps the two independently evolvable
// (docs/adr/0005).
//
// The role is the ticket module's own, not identity's. They hold the same three
// strings and answer different questions: what someone *is*, versus what they
// *were when they acted*. The conversion happens once, in internal/app.
type Caller struct {
	ID   uuid.UUID
	Role domain.Role
}

// CallerResolver reads the authenticated caller out of a request context.
//
// A function rather than an interface: it has one method, and a test can supply
// one inline without declaring a stub type. It returns false when the request
// did not pass through authentication, which is a wiring mistake rather than a
// bad request — and every handler answers 401 on it, so a route mounted outside
// the authenticated group fails closed.
//
// It sits in ports rather than in a feature because every endpoint in the
// module reads it, and in ports rather than in transport because it names no
// HTTP type: what a caller is, is this module's question, and where one comes
// from is somebody else's.
type CallerResolver func(ctx context.Context) (Caller, bool)
