// Package transition moves a ticket and rebuilds its SLA clock.
//
// It is where the clock is paused and resumed, which is the headline behaviour
// of the whole domain. The write itself is one transaction the repository owns,
// and the order inside it is fixed by docs/adr/0001: insert the history row,
// read the history *after* the insert, rebuild the clock from it, write the
// cache. Reading before the insert leaves the cache exactly one event behind —
// a plausible-looking corruption no unit test of the arithmetic would find,
// because the arithmetic is right and the input is stale.
package transition

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// Command is a request to move a ticket.
type Command struct {
	TicketID  uuid.UUID
	Target    domain.Status
	ActorID   uuid.UUID
	ActorRole domain.Role
	Reason    *string
}

// Tickets is the persistence this use case needs.
type Tickets interface {
	Transition(ctx context.Context, in Command, clock ports.SLAClock) (domain.Ticket, error)

	// PolicyIDOf reads which policy a ticket was created under, and nothing
	// else. It exists so the clock can be resolved before Transition opens its
	// transaction.
	PolicyIDOf(ctx context.Context, id uuid.UUID) (int64, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
	sla     ports.SLAPolicies
}

func New(tickets Tickets, sla ports.SLAPolicies) *Handler {
	return &Handler{tickets: tickets, sla: sla}
}

// Handle resolves the clock, then moves the ticket.
//
// The policy id is read first, unlocked, so the clock can be resolved before
// the transaction opens. That read is safe because sla_policy_id is written
// once at creation and no query updates it — and the repository does not take
// it on trust: it asserts the locked row still agrees before writing anything.
func (h *Handler) Handle(ctx context.Context, cmd Command) (domain.Ticket, error) {
	policyID, err := h.tickets.PolicyIDOf(ctx, cmd.TicketID)
	if err != nil {
		return domain.Ticket{}, err
	}

	clock, err := h.sla.ForPolicy(ctx, policyID)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("resolving the SLA policy: %w", err)
	}

	return h.tickets.Transition(ctx, cmd, clock)
}
