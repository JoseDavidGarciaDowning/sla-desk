// Package get reads one of the caller's own tickets.
//
// A ticket belonging to someone else is reported as not existing, never as
// forbidden (docs/spec.md §11): a 403 would confirm that an id names a real
// ticket, which is exactly what the caller must not be able to learn. The query
// returns no rows for both cases, so nothing here could tell them apart even if
// it wanted to.
//
// The agent's unscoped read of the same ticket arrives in this package too, as
// agent.go, in the slice that adds it. Same concept, two guarantees, side by
// side — which makes the difference between them harder to miss than two
// directories would.
package get

import (
	"context"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Tickets is the persistence this use case needs.
type Tickets interface {
	OneForRequester(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
}

func New(tickets Tickets) *Handler { return &Handler{tickets: tickets} }

// Handle returns one of the caller's tickets, or domain.ErrTicketNotFound.
func (h *Handler) Handle(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	return h.tickets.OneForRequester(ctx, id, requesterID)
}
