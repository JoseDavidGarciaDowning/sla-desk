package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla"
	slaapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	sladomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// ErrNoPolicyForPriority means no active SLA policy serves that priority.
// A ticket cannot be created without one: the budget is data, and there is
// no default hiding in the code to fall back on.
var ErrNoPolicyForPriority = errors.New("store: no active SLA policy for that priority")

// TicketRepo holds the writes that span more than one statement.
//
// The generated queries take a DBTX, so they work inside a transaction; what
// they cannot do is decide where a transaction begins and ends. That is this
// type's only job.
type TicketRepo struct {
	pool *pgxpool.Pool
}

func NewTicketRepo(pool *pgxpool.Pool) *TicketRepo {
	return &TicketRepo{pool: pool}
}

// NewTicket is everything the caller supplies. The status, the clock and the
// policy are not in it — they are consequences, not inputs.
type NewTicket struct {
	RequesterID pgtype.UUID
	ActorRole   ticket.Role
	Title       string
	Description string
	Category    ticket.Category
	Priority    ticket.Priority
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
func (r *TicketRepo) Create(ctx context.Context, in NewTicket) (Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("begin: %w", err)
	}
	// A rollback after a successful commit is a no-op that returns
	// pgx.ErrTxClosed, so this is safe on every path.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

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
		return Ticket{}, fmt.Errorf("reading the transaction time: %w", err)
	}

	// The SLA module is built on this transaction's handle, so the policy read
	// runs on the connection already held rather than taking a second one from
	// the pool. pgxpool defaults to max(4, NumCPU); enough concurrent creates
	// each holding two would wait on each other.
	//
	// Resolving before the transaction opens is the better shape and is what
	// the ticket module will do once it owns this write. Today this package is
	// still both the composition point and the transaction owner.
	policy, err := sla.New(tx).Calculator.ForPriority(ctx, sladomain.Priority(in.Priority))
	if err != nil {
		// Translated rather than passed through. internal/api matches on this
		// package's sentinel to answer 422 instead of 500, and making it match
		// the SLA module's would mean importing that module to name an error —
		// which is the coupling the contract exists to avoid. The original is
		// kept wrapped so the cause still reaches the logs.
		if errors.Is(err, slaapp.ErrNoPolicyForPriority) {
			return Ticket{}, errors.Join(ErrNoPolicyForPriority, err)
		}
		return Ticket{}, err
	}

	// A new ticket is open, so its history is one entry long and the clock has
	// been running since that instant. Going through Reconstruct rather than
	// adding the budget here keeps the promise in §4.2 that deadline arithmetic
	// exists in exactly one place — including this, its simplest case.
	// The status decides whether the clock runs; the SLA package is only told
	// the answer. That translation is this package's job today, and moves to
	// the composition root once the modules are split.
	state, err := sladomain.Reconstruct(policy, []sladomain.Phase{
		{At: now, Running: ticket.StatusOpen.RunsClock()},
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("starting the clock: %w", err)
	}

	created, err := q.CreateTicket(ctx, CreateTicketParams{
		RequesterID:       in.RequesterID,
		Title:             in.Title,
		Description:       in.Description,
		Category:          in.Category,
		Priority:          in.Priority,
		SlaPolicyID:       policy.ID,
		SlaClockStartedAt: state.RunningSince,
		SlaDueAt:          state.DueAt,
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("inserting the ticket: %w", err)
	}

	// from_status is NULL: there is no status to come from. created_at is the
	// same instant the deadline was computed from, which is what lets the
	// reconstruction in §9 reproduce the cache exactly.
	if _, err := q.InsertTicketStatusHistory(ctx, InsertTicketStatusHistoryParams{
		TicketID:   created.ID,
		FromStatus: nil,
		ToStatus:   ticket.StatusOpen,
		ActorID:    in.RequesterID,
		ActorRole:  in.ActorRole,
		CreatedAt:  now,
	}); err != nil {
		return Ticket{}, fmt.Errorf("inserting the opening history row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Ticket{}, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

// ErrTicketNotFound means no ticket has that id.
var ErrTicketNotFound = errors.New("store: no ticket with that id")

// StatusChange is a request to move a ticket.
type StatusChange struct {
	TicketID  pgtype.UUID
	Target    ticket.Status
	ActorID   pgtype.UUID
	ActorRole ticket.Role
	Reason    *string
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
func (r *TicketRepo) Transition(ctx context.Context, in StatusChange) (Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	now, err := q.TransactionTime(ctx)
	if err != nil {
		return Ticket{}, fmt.Errorf("reading the transaction time: %w", err)
	}

	current, err := q.GetTicketForUpdate(ctx, in.TicketID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Ticket{}, ErrTicketNotFound
		}
		return Ticket{}, fmt.Errorf("locking the ticket: %w", err)
	}

	// Legality and permission are decided by the domain, which touches no
	// database and knows nothing about this transaction.
	target, err := ticket.Transition(current.Status, in.Target, in.ActorRole)
	if err != nil {
		return Ticket{}, err
	}

	policy, err := sla.New(tx).Calculator.ForPolicy(ctx, current.SlaPolicyID)
	if err != nil {
		return Ticket{}, err
	}

	// 1. The fact.
	from := current.Status
	if _, err := q.InsertTicketStatusHistory(ctx, InsertTicketStatusHistoryParams{
		TicketID:   in.TicketID,
		FromStatus: &from,
		ToStatus:   target,
		ActorID:    in.ActorID,
		ActorRole:  in.ActorRole,
		Reason:     in.Reason,
		CreatedAt:  now,
	}); err != nil {
		return Ticket{}, fmt.Errorf("inserting the history row: %w", err)
	}

	// 2. The whole history, including the row just written.
	rows, err := q.ListTicketStatusHistory(ctx, in.TicketID)
	if err != nil {
		return Ticket{}, fmt.Errorf("reading the history: %w", err)
	}

	// 3. The clock, from the fact rather than from the previous cache.
	timeline := make([]sladomain.Phase, len(rows))
	for i, row := range rows {
		timeline[i] = sladomain.Phase{At: row.CreatedAt, Running: row.ToStatus.RunsClock()}
	}
	state, err := sladomain.Reconstruct(policy, timeline)
	if err != nil {
		return Ticket{}, fmt.Errorf("rebuilding the clock: %w", err)
	}

	// 4. The cache.
	updated, err := q.UpdateTicketClock(ctx, UpdateTicketClockParams{
		ID:                in.TicketID,
		Status:            target,
		SlaConsumedMicros: state.BudgetUsed.Microseconds(),
		SlaClockStartedAt: state.RunningSince,
		SlaDueAt:          state.DueAt,
		UpdatedAt:         now,
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("updating the cache: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Ticket{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}
