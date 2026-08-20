// Package assign puts an agent on a ticket, or takes whoever is there off it.
//
// It writes no ticket_status_history row, and that is a decision rather than an
// omission (tasks/slice-2/plan.md §E): the history is the fact the SLA clock is
// rebuilt from, and assignment does not move a ticket's status. A row for it
// would pad the timeline Reconstruct walks, and the consistency test would be
// right to fail.
package assign

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// ErrNotAssignable means the id offered as an assignee may not hold tickets.
//
// One error for two situations — a customer's id, and an id that names nobody —
// and that is deliberate. Two different refusals would let an agent enumerate
// which uuids name real users, which is the same reasoning that makes a ticket
// that is not yours a 404 rather than a 403 (docs/spec.md §11).
//
// It stays in this package rather than moving to the domain because only this
// use case can raise it and only this use case answers it. The two errors that
// did move are the ones half the module shares.
var ErrNotAssignable = errors.New("ticket: that user may not hold tickets")

// Tickets is the persistence this use case needs.
type Tickets interface {
	// Assign writes the assignee and nothing else. A nil assignee unassigns.
	Assign(ctx context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
	dir     ports.AssigneeDirectory
}

func New(tickets Tickets, dir ports.AssigneeDirectory) *Handler {
	return &Handler{tickets: tickets, dir: dir}
}

// Handle checks the candidate may hold tickets, then writes them onto it.
//
// The directory is consulted only when there is somebody to ask about.
// Unassigning has no candidate, and asking anyway would make "take this off my
// plate" fail whenever the directory is unreachable.
//
// A directory that cannot answer is not a refusal. Reporting an outage as "that
// user may not hold tickets" would be a lie the caller acts on, so the cause
// travels up instead and the adapter answers 500.
func (h *Handler) Handle(ctx context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error) {
	if assignee != nil {
		ok, err := h.dir.CanHoldTickets(ctx, *assignee)
		if err != nil {
			return domain.Ticket{}, fmt.Errorf("checking whether the assignee may hold tickets: %w", err)
		}
		if !ok {
			return domain.Ticket{}, ErrNotAssignable
		}
	}

	return h.tickets.Assign(ctx, ticketID, assignee)
}
