package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

// OneForRequester returns one of a requester's tickets.
//
// The scope is in the query, not in a check here. A forgotten comparison in Go
// must not be enough to leak another customer's ticket (docs/spec.md §4.3).
func (r *Repository) OneForRequester(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	row, err := r.q.GetTicketForRequester(ctx, ticketdb.GetTicketForRequesterParams{
		ID:          id,
		RequesterID: requesterID,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Ticket{}, domain.ErrTicketNotFound
	case err != nil:
		return domain.Ticket{}, fmt.Errorf("reading the ticket: %w", err)
	}
	return ticketFrom(row), nil
}

// OneByID reads a ticket without asking whose it is.
//
// ErrTicketNotFound for an id that names nothing, which the handler turns into
// a 404. That rule survives slice 2 untouched: the agent group answers 403 to a
// customer because its path carries no id to confirm, and an id that names no
// ticket is a different question (docs/spec.md §11).
func (r *Repository) OneByID(ctx context.Context, id uuid.UUID) (domain.Ticket, error) {
	row, err := r.q.GetTicketByID(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Ticket{}, domain.ErrTicketNotFound
	case err != nil:
		return domain.Ticket{}, fmt.Errorf("reading the ticket: %w", err)
	}
	return ticketFrom(row), nil
}
