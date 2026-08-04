// Package postgres stores and reads tickets.
//
// It is the only package in this module that knows tickets and
// ticket_status_history are tables. Everything above it receives domain values,
// so a new column stops here.
//
// It owns the transactions, because the fact and the cache have to commit
// together (docs/spec.md §4.1). What it does not do is any I/O belonging to
// another module: the SLA clock arrives already resolved. See docs/adr/0006.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/ticketdb"
)

// ErrPolicyChangedUnderfoot means a ticket's SLA policy changed between the
// unlocked read that resolved it and the locked read inside the transaction.
//
// Unreachable while nothing updates sla_policy_id, which is the point: if that
// ever stops being true, the deadline would be computed against a budget the
// ticket was not created under, and that is worth refusing loudly rather than
// storing.
var ErrPolicyChangedUnderfoot = errors.New("ticket: the SLA policy changed mid-transition")

// Repository holds the writes that span more than one statement.
//
// The generated queries take a DBTX, so they work inside a transaction; what
// they cannot do is decide where a transaction begins and ends. That is this
// type's job.
type Repository struct {
	pool *pgxpool.Pool
	q    *ticketdb.Queries
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: ticketdb.New(pool)}
}

// Create writes a ticket and its opening history row in one transaction.
//
// The two are inseparable by docs/spec.md §4.1: no history row, no transition.
// A ticket without one is a ticket whose SLA clock cannot be rebuilt, which is
// exactly the corruption the fact-versus-cache design exists to avoid.
//
// The order is fixed by the foreign key — the ticket has to exist before a
// history row can point at it — and both rows are stamped with the same
// instant, read once from the database at the top.
func (r *Repository) Create(ctx context.Context, in application.NewTicket, clock application.SLAClock) (domain.Ticket, error) {
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
	// been running since that instant. Going through the calculation rather than
	// adding the budget here keeps the promise in §4.2 that deadline arithmetic
	// exists in exactly one place — including this, its simplest case.
	//
	// Pure: the policy was resolved before this transaction opened.
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

// PolicyIDOf reads which policy a ticket was created under.
func (r *Repository) PolicyIDOf(ctx context.Context, id uuid.UUID) (int64, error) {
	policyID, err := r.q.GetTicketSLAPolicyID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, application.ErrTicketNotFound
		}
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
func (r *Repository) Transition(ctx context.Context, in application.StatusChange, clock application.SLAClock) (domain.Ticket, error) {
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
			return domain.Ticket{}, application.ErrTicketNotFound
		}
		return domain.Ticket{}, fmt.Errorf("locking the ticket: %w", err)
	}

	// The clock was resolved from an unlocked read of sla_policy_id. Nothing
	// updates that column, so this cannot differ — and if it ever does, the
	// deadline would be computed against the wrong budget.
	if current.SlaPolicyID != clock.PolicyID() {
		return domain.Ticket{}, fmt.Errorf("%w: ticket %s is on policy %d, the clock is for %d",
			ErrPolicyChangedUnderfoot, in.TicketID, current.SlaPolicyID, clock.PolicyID())
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

	// 3. The clock, from the fact rather than from the previous cache.
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

func (r *Repository) ListByRequester(ctx context.Context, f application.ListFilter) ([]domain.Ticket, error) {
	// The two filter parameters come back out of sqlc as *string rather than as
	// the domain types: a nullable parameter gets either the right type or the
	// right nullability, not both. The conversion is here rather than in the
	// caller, so nothing above this package has to know.
	var status, priority *string
	if f.Status != nil {
		status = new(string(*f.Status))
	}
	if f.Priority != nil {
		priority = new(string(*f.Priority))
	}

	rows, err := r.q.ListTicketsByRequester(ctx, ticketdb.ListTicketsByRequesterParams{
		RequesterID:    f.RequesterID,
		Status:         status,
		Priority:       priority,
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

func (r *Repository) GetForRequester(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	row, err := r.q.GetTicketForRequester(ctx, ticketdb.GetTicketForRequesterParams{
		ID:          id,
		RequesterID: requesterID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Ticket{}, application.ErrTicketNotFound
		}
		return domain.Ticket{}, fmt.Errorf("reading the ticket: %w", err)
	}
	return ticketFrom(row), nil
}

func (r *Repository) HistoryForRequester(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	rows, err := r.q.ListTicketStatusHistoryForRequester(ctx,
		ticketdb.ListTicketStatusHistoryForRequesterParams{
			TicketID:    ticketID,
			RequesterID: requesterID,
		})
	if err != nil {
		return nil, fmt.Errorf("reading the ticket history: %w", err)
	}
	return historyFrom(rows), nil
}

func ticketFrom(row ticketdb.Ticket) domain.Ticket {
	return domain.Ticket{
		ID:                row.ID,
		RequesterID:       row.RequesterID,
		AssigneeID:        row.AssigneeID,
		Title:             row.Title,
		Description:       row.Description,
		Category:          row.Category,
		Priority:          row.Priority,
		Status:            row.Status,
		SLAPolicyID:       row.SlaPolicyID,
		SLAConsumed:       time.Duration(row.SlaConsumedMicros) * time.Microsecond,
		SLAClockStartedAt: row.SlaClockStartedAt,
		SLADueAt:          row.SlaDueAt,
		SLABreachedAt:     row.SlaBreachedAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func historyFrom(rows []ticketdb.TicketStatusHistory) []domain.HistoryEntry {
	out := make([]domain.HistoryEntry, len(rows))
	for i, row := range rows {
		out[i] = domain.HistoryEntry{
			FromStatus: row.FromStatus,
			ToStatus:   row.ToStatus,
			ActorID:    row.ActorID,
			ActorRole:  row.ActorRole,
			Reason:     row.Reason,
			CreatedAt:  row.CreatedAt,
		}
	}
	return out
}
