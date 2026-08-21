package postgres

import (
	"context"
	"fmt"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/list"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

// ListForRequester returns one page of a requester's tickets.
func (r *Repository) ListForRequester(ctx context.Context, f list.Filter) ([]domain.Ticket, error) {
	rows, err := r.q.ListTicketsByRequester(ctx, ticketdb.ListTicketsByRequesterParams{
		RequesterID: f.RequesterID,
		// The query casts these to text, so the generated params are *string.
		// A pointer conversion rather than a copy: Status and Priority are
		// string underneath, and the nil that means "no filter" has to survive.
		Status:         (*string)(f.Status),
		Priority:       (*string)(f.Priority),
		AfterCreatedAt: f.AfterCreatedAt,
		AfterID:        f.AfterID,
		PageSize:       f.PageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("listing tickets: %w", err)
	}

	out := make([]domain.Ticket, len(rows))
	for i, row := range rows {
		out[i] = ticketFrom(row)
	}
	return out, nil
}
