// Package history reads the timeline of one of the caller's own tickets.
//
// The requester predicate lives in the query's JOIN, so there is no ownership
// check here to forget. An empty result is reported as the ticket not existing:
// every ticket has at least the entry recording its creation, so nothing comes
// back only when the ticket is absent or is not the caller's — and the query
// cannot tell those apart, which is what lets one answer serve both without
// confirming that the id names a real ticket (docs/spec.md §11).
package history

import (
	"context"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Tickets is the persistence this use case needs.
type Tickets interface {
	HistoryForRequester(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
}

func New(tickets Tickets) *Handler { return &Handler{tickets: tickets} }

func (h *Handler) Handle(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	return h.tickets.HistoryForRequester(ctx, ticketID, requesterID)
}
