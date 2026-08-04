// Package application holds the ticket use cases and the contracts they need
// from everything outside this module.
//
// Every interface here is declared by the consumer — this package — and never
// by whoever implements it (docs/spec.md §8). That is what makes the ticket
// module independently evolvable: it states what it needs in its own
// vocabulary, and internal/app is responsible for finding something that can
// provide it. Nothing in this module imports another business module.
package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// ErrNoSLAPolicy means nothing serves that priority.
//
// Declared here rather than re-exported from whatever implements SLAPolicies,
// because this module does not know what that is. The adapter in internal/app
// translates the provider's own sentinel into this one, so a handler can tell
// "our seed data is wrong" apart from "the database is down" without importing
// another module to do it.
var ErrNoSLAPolicy = errors.New("ticket: no SLA policy serves that priority")

// SLAClock is a resolved SLA policy: it computes clock state from a timeline
// with no further I/O.
//
// The split between resolving and computing is not incidental — it is what lets
// the repository do its database read *before* opening its transaction. Calling
// out to another module mid-transaction would hold a second pooled connection
// for the transaction's duration, and pgxpool defaults to max(4, NumCPU), so
// enough concurrent writes would deadlock waiting on each other. See
// docs/adr/0006.
type SLAClock interface {
	// PolicyID is the policy this clock was resolved from, snapshotted onto the
	// ticket so that editing a policy does not move existing deadlines.
	PolicyID() int64

	// Compute is pure. It touches nothing and may be called inside a
	// transaction.
	Compute(timeline []domain.Phase) (ClockState, error)
}

// ClockState is what an SLA calculation yields.
//
// RunningSince and DueAt are both nil or both set: a paused ticket has no
// deadline and therefore cannot breach (docs/spec.md §4.2). The tickets table
// has CHECK constraints saying the same thing, so an implementation that got
// this wrong would be refused by the database rather than stored.
type ClockState struct {
	BudgetUsed   time.Duration
	RunningSince *time.Time
	DueAt        *time.Time
}

// SLAPolicies resolves SLA clocks.
//
// Implemented in internal/app against the SLA module. This package does not
// know that module exists, and states its needs in its own Priority type.
type SLAPolicies interface {
	// ForPriority resolves the policy a new ticket should be created under.
	ForPriority(ctx context.Context, p domain.Priority) (SLAClock, error)

	// ForPolicy reads the policy a ticket was already snapshotted with.
	ForPolicy(ctx context.Context, id int64) (SLAClock, error)
}

// NewTicket is everything a caller supplies to create one.
//
// The status, the clock and the policy are not in it — they are consequences,
// not inputs. Neither is the requester's identity trusted from a client: it
// arrives from the authenticated caller (docs/spec.md §4.3).
type NewTicket struct {
	RequesterID uuid.UUID
	ActorRole   domain.ActorRole
	Title       string
	Description string
	Category    domain.Category
	Priority    domain.Priority
}

// StatusChange is a request to move a ticket.
type StatusChange struct {
	TicketID  uuid.UUID
	Target    domain.Status
	ActorID   uuid.UUID
	ActorRole domain.ActorRole
	Reason    *string
}

// ListFilter is one page of a requester's tickets.
//
// Status and Priority are pointers because absent and "the empty value" are
// different questions, and a filter that cannot express "no filter" silently
// returns nothing.
type ListFilter struct {
	RequesterID uuid.UUID
	Status      *domain.Status
	Priority    *domain.Priority

	// AfterCreatedAt and AfterID carry the last row of the previous page.
	// Keyset pagination, not OFFSET: see the query for why.
	AfterCreatedAt *time.Time
	AfterID        *uuid.UUID

	PageSize int32
}

// Repository stores and reads tickets.
//
// The write methods own their transactions, because the fact and the cache have
// to commit together (docs/spec.md §4.1). They take an already-resolved
// SLAClock for the reason given above: no I/O from another module happens
// inside them.
type Repository interface {
	Create(ctx context.Context, in NewTicket, clock SLAClock) (domain.Ticket, error)
	Transition(ctx context.Context, in StatusChange, clock SLAClock) (domain.Ticket, error)

	// PolicyIDOf reads which policy a ticket was created under, and nothing
	// else. It exists so the clock can be resolved before Transition opens its
	// transaction.
	PolicyIDOf(ctx context.Context, id uuid.UUID) (int64, error)

	// The read methods scope by requester in the query rather than filtering
	// afterwards. A forgotten check in Go must not be enough to leak another
	// customer's ticket (docs/spec.md §4.3).
	ListByRequester(ctx context.Context, f ListFilter) ([]domain.Ticket, error)
	GetForRequester(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error)
	HistoryForRequester(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error)
}
