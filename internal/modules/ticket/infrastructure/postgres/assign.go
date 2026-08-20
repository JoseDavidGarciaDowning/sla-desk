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

// Assign writes the assignee column, and only that column.
//
// No transaction: it is one statement, and there is no second fact to keep in
// step with it. Every other write in this repository spans a row and its
// history entry; this one deliberately does not (tasks/slice-2/plan.md §E).
func (r *Repository) Assign(ctx context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error) {
	row, err := r.q.AssignTicket(ctx, ticketdb.AssignTicketParams{
		ID:         ticketID,
		AssigneeID: assignee,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Ticket{}, domain.ErrTicketNotFound
	case err != nil:
		return domain.Ticket{}, fmt.Errorf("assigning the ticket: %w", err)
	}
	return ticketFrom(row), nil
}
