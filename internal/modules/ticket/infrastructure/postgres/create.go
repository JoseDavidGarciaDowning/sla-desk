package postgres

import (
	"context"
	"fmt"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/create"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// Create writes a ticket and its opening history row in one transaction.
//
// The two are inseparable by docs/spec.md §4.1: no history row, no transition.
// A ticket without one is a ticket whose SLA clock cannot be rebuilt, which is
// exactly the corruption the fact-versus-cache design exists to avoid.
//
// The order is fixed by the foreign key — the ticket has to exist before a
// history row can point at it — and both rows are stamped with the same
// instant, read once from the database at the top.
//
// The clock arrives already resolved. Nothing in this method does I/O belonging
// to another module, which is what keeps it to one pooled connection.
func (r *Repository) Create(ctx context.Context, in create.Command, clock ports.SLAClock) (domain.Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("begin: %w", err)
	}
	// A rollback after a successful commit is a no-op that returns
	// pgx.ErrTxClosed, so this is safe on every path.
	defer func() { _ = tx.Rollback(ctx) }()

	q := ticketdb.New(tx)

	// The instant every row in this transaction is stamped with, read from the
	// database rather than taken from time.Now().
	//
	// No test covers this choice, and swapping it for time.Now() leaves the
	// suite green — including the consistency test, because that compares the
	// cache against the history and both would move together. What the app
	// clock breaks is something a test here cannot see: the breach worker
	// selects WHERE sla_due_at < now(), evaluated by Postgres, so a deadline
	// derived from an API instance's clock is shifted by that instance's drift.
	// Measured at 928µs against a database on the same machine; across Cloud
	// Run instances and a managed Postgres there is no bound on it, and two
	// identical tickets created a second apart on different instances would
	// breach at different times.
	now, err := q.TransactionTime(ctx)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("reading the transaction time: %w", err)
	}

	// A new ticket is open, so its timeline is one phase long and the clock has
	// been running since that instant. Going through the resolved clock rather
	// than adding the budget here keeps the promise in §4.2 that deadline
	// arithmetic exists in exactly one place — including this, its simplest case.
	state, err := clock.Compute([]domain.Phase{
		{At: now, Running: domain.StatusOpen.RunsClock()},
	})
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("starting the clock: %w", err)
	}

	created, err := q.CreateTicket(ctx, ticketdb.CreateTicketParams{
		RequesterID:       in.RequesterID,
		Title:             in.Title,
		Description:       in.Description,
		Category:          in.Category,
		Priority:          in.Priority,
		SlaPolicyID:       clock.PolicyID(),
		SlaClockStartedAt: state.RunningSince,
		SlaDueAt:          state.DueAt,
	})
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("inserting the ticket: %w", err)
	}

	// from_status is NULL: there is no status to come from. created_at is the
	// same instant the deadline was computed from, which is what lets the
	// reconstruction in §9 reproduce the cache exactly.
	if _, err := q.InsertTicketStatusHistory(ctx, ticketdb.InsertTicketStatusHistoryParams{
		TicketID:   created.ID,
		FromStatus: nil,
		ToStatus:   domain.StatusOpen,
		ActorID:    in.RequesterID,
		ActorRole:  in.ActorRole,
		CreatedAt:  now,
	}); err != nil {
		return domain.Ticket{}, fmt.Errorf("inserting the opening history row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Ticket{}, fmt.Errorf("commit: %w", err)
	}
	return ticketFrom(created), nil
}
