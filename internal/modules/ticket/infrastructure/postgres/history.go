package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

// HistoryForRequester returns a ticket's timeline as its requester may see it.
//
// An empty result is ErrTicketNotFound rather than an empty timeline: every
// ticket has at least the entry recording its creation, so nothing comes back
// only when the ticket does not exist or is not the caller's — and those two
// must be indistinguishable (docs/spec.md §11).
func (r *Repository) HistoryForRequester(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	rows, err := r.q.ListTicketStatusHistoryForRequester(ctx, ticketdb.ListTicketStatusHistoryForRequesterParams{
		TicketID:    ticketID,
		RequesterID: requesterID,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the history: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.ErrTicketNotFound
	}
	return historyFrom(rows), nil
}

// Timeline returns a ticket's full history, for a caller who is not its
// requester.
//
// It reuses ListTicketStatusHistory, which has never had a requester predicate:
// it is the input to sla.Reconstruct, running inside a transaction that has
// already established which ticket it is working on. The comment on that query
// anticipated this exact caller — "an agent transitions tickets that are not
// theirs" — so slice 2 adds no query here, only a way to reach it.
//
// Empty means the ticket does not exist, not that it has no timeline. Every
// ticket carries at least the entry recording its creation.
func (r *Repository) Timeline(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error) {
	rows, err := r.q.ListTicketStatusHistory(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("reading the history: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.ErrTicketNotFound
	}
	return historyFrom(rows), nil
}
