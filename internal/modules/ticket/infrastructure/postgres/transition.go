package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/transition"
	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// PolicyIDOf reads which policy a ticket was created under, and nothing else.
//
// Unlocked on purpose: it runs before Transition opens its transaction, so the
// clock can be resolved without another module's read happening inside ours.
func (r *Repository) PolicyIDOf(ctx context.Context, id uuid.UUID) (int64, error) {
	policyID, err := r.q.GetTicketPolicyID(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, domain.ErrTicketNotFound
	case err != nil:
		return 0, fmt.Errorf("reading the ticket's policy: %w", err)
	}
	return policyID, nil
}

// Transition moves a ticket and rebuilds its SLA clock, in one transaction.
//
// The order is fixed by docs/adr/0001 and is the reason this method exists
// rather than a handler doing four calls:
//
//  1. insert the history row
//  2. read the ticket's full history — after the insert, never before
//  3. rebuild the clock from it
//  4. write the cache
//
// Reading the history before the insert leaves the cache exactly one event
// behind: a plausible-looking corruption that no unit test of the arithmetic
// would ever find, because the arithmetic is right and the input is stale.
func (r *Repository) Transition(ctx context.Context, in transition.Command, clock ports.SLAClock) (domain.Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := ticketdb.New(tx)

	now, err := q.TransactionTime(ctx)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("reading the transaction time: %w", err)
	}

	current, err := q.GetTicketForUpdate(ctx, in.TicketID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Ticket{}, domain.ErrTicketNotFound
		}
		return domain.Ticket{}, fmt.Errorf("locking the ticket: %w", err)
	}

	// The clock was resolved from a policy id read before this transaction
	// existed. Now that the row is locked, check the assumption rather than
	// trusting it: nothing updates sla_policy_id, so this cannot fire — and a
	// deadline computed from the wrong budget is the kind of wrong that looks
	// right.
	if current.SlaPolicyID != clock.PolicyID() {
		return domain.Ticket{}, fmt.Errorf("%w: resolved %d, locked row says %d",
			ErrPolicyChangedUnderUs, clock.PolicyID(), current.SlaPolicyID)
	}

	// Legality and permission are decided by the domain, which touches no
	// database and knows nothing about this transaction.
	target, err := domain.Transition(current.Status, in.Target, in.ActorRole)
	if err != nil {
		return domain.Ticket{}, err
	}

	// 1. The fact.
	from := current.Status
	if _, err := q.InsertTicketStatusHistory(ctx, ticketdb.InsertTicketStatusHistoryParams{
		TicketID:   in.TicketID,
		FromStatus: &from,
		ToStatus:   target,
		ActorID:    in.ActorID,
		ActorRole:  in.ActorRole,
		Reason:     in.Reason,
		CreatedAt:  now,
	}); err != nil {
		return domain.Ticket{}, fmt.Errorf("inserting the history row: %w", err)
	}

	// 2. The whole history, including the row just written.
	rows, err := q.ListTicketStatusHistory(ctx, in.TicketID)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("reading the history: %w", err)
	}

	// 3. The clock, from the fact rather than from the previous cache. Which
	//    statuses burn budget is decided by domain.Timeline; the SLA side sees
	//    booleans and no statuses at all.
	state, err := clock.Compute(domain.Timeline(historyFrom(rows)))
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("rebuilding the clock: %w", err)
	}

	// 4. The cache.
	updated, err := q.UpdateTicketClock(ctx, ticketdb.UpdateTicketClockParams{
		ID:                in.TicketID,
		Status:            target,
		SlaConsumedMicros: state.BudgetUsed.Microseconds(),
		SlaClockStartedAt: state.RunningSince,
		SlaDueAt:          state.DueAt,
		UpdatedAt:         now,
	})
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("updating the cache: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Ticket{}, fmt.Errorf("commit: %w", err)
	}
	return ticketFrom(updated), nil
}
