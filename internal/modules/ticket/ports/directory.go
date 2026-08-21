package ports

import (
	"context"

	"github.com/google/uuid"
)

// AssigneeDirectory answers whether an id may be put on a ticket.
//
// Declared here, by the consumer, and implemented in the composition root
// against the identity module (docs/adr/0005). assignee_id is a bare foreign
// key to users, so the database will happily accept a customer's id there; this
// module cannot check it, because it does not know the identity module exists.
//
// So it asks the question in its own words. "May this person hold tickets" is
// what this module needs to know; that the answer happens to be "their role is
// agent or admin" is the other module's business.
//
// In ports rather than in features/assign because it crosses a module boundary
// rather than a feature one: the composition root supplies it, and a second
// feature that needed the same answer would ask the same question.
//
// Rejected: a CHECK constraint or a trigger joining users.role. It would
// enforce the rule at the right layer but freeze the answer — demoting an agent
// who still holds open tickets would then fail at write time, on unrelated
// updates to those rows.
type AssigneeDirectory interface {
	CanHoldTickets(ctx context.Context, id uuid.UUID) (bool, error)
}
