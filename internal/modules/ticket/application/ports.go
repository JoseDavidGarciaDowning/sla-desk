// Package application holds the ticket use cases and the contracts they need
// from everything outside this module.
//
// Every interface here is declared by the consumer — this package — and never
// by whoever implements it (docs/spec.md §8). That is what makes the module
// independently evolvable: it states what it needs in its own vocabulary, and
// the composition root is responsible for finding something that can provide
// it. Nothing in this module imports another business module.
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
// because this module does not know what that is. The adapter in the
// composition root translates the provider's own sentinel into this one, so a
// handler can tell "our seed data is wrong" apart from "the database is down"
// without importing another module to do it.
var ErrNoSLAPolicy = errors.New("ticket: no SLA policy serves that priority")

// ErrTicketNotFound means no ticket has that id, or it is not the caller's.
//
// The two are deliberately the same error. A 403 would confirm that an id names
// a real ticket, which is exactly what a caller must not be able to learn
// (docs/spec.md §11), and the queries return nothing for both cases so nothing
// here could tell them apart even if it wanted to.
var ErrTicketNotFound = errors.New("ticket: no ticket with that id")

// SLAClock is a resolved SLA policy: it computes clock state from a timeline
// with no further I/O.
//
// The split between resolving and computing is not incidental — it is what lets
// the repository do its read *before* opening its transaction. Calling out to
// another module mid-transaction would hold a second pooled connection for the
// transaction's duration, and pgxpool defaults to max(4, NumCPU), so enough
// concurrent writes would wait on each other.
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
// carries CHECK constraints saying the same thing, so an implementation that
// got this wrong would be refused by the database rather than stored.
type ClockState struct {
	BudgetUsed   time.Duration
	RunningSince *time.Time
	DueAt        *time.Time
}

// SLAPolicies resolves SLA clocks.
//
// Implemented in the composition root against the SLA module. This package does
// not know that module exists, and states its needs in its own Priority type.
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
	ActorRole   domain.Role
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
	ActorRole domain.Role
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

// AssigneeScope says which tickets a queue page is about, by assignee.
//
// Three questions, not one: any assignee, one in particular, or none at all. A
// nil assignee id cannot express all three — it already means "no filter", so
// it cannot also mean "unassigned" — and a filter that silently answers the
// wrong question is worse than one that does not exist.
type AssigneeScope string

const (
	AssigneeAny        AssigneeScope = "any"
	AssigneeUnassigned AssigneeScope = "unassigned"
	AssigneeOne        AssigneeScope = "one"
)

// QueueFilter is one page of every ticket, in deadline order.
//
// Deliberately not ListFilter with the requester made optional. The two are
// different reads with different guarantees: ListFilter always carries a
// requester and its query always scopes by one, and merging them would put the
// distinction in a field that a caller can leave unset (tasks/slice-2/plan.md
// decision B).
type QueueFilter struct {
	Status   *domain.Status
	Priority *domain.Priority

	// Assignee says which of the three questions this page asks. The zero value
	// is not AssigneeAny on purpose — an unset scope is a caller who forgot,
	// and the repository refuses it rather than quietly showing everything.
	Assignee   AssigneeScope
	AssigneeID *uuid.UUID

	// AfterDueAt and AfterID carry the last row of the previous page, in the
	// order this query sorts by. A keyset cursor is a position in the sort
	// order, so it carries the deadline and not created_at.
	//
	// AfterDueAt is already coalesced: a paused ticket's position is
	// 'infinity', because (NULL, id) > (x, id) is NULL and WHERE NULL drops the
	// row. See the query.
	AfterDueAt *time.Time
	AfterID    *uuid.UUID

	PageSize int32
}

// ErrUnsetAssigneeScope reports a QueueFilter built without one.
//
// A zero AssigneeScope means a caller forgot, and defaulting it to "any" would
// turn forgetting into "show every ticket" — which on this query is the whole
// unscoped set.
var ErrUnsetAssigneeScope = errors.New("ticket: the queue filter has no assignee scope")

// QueueEntry is a ticket as the agent queue shows it.
//
// It carries the requester's display name because a queue of uuids is not
// usable, and it is a separate type rather than a field on domain.Ticket: the
// name belongs to the identity module and is joined in for this one read. A
// Ticket that sometimes carried a name and sometimes did not would make every
// other caller check.
type QueueEntry struct {
	Ticket domain.Ticket

	// RequesterName is empty for anyone who signed up with an email and a
	// password, because Clerk holds no name for them. The email is carried
	// alongside so the transport can decide what to show rather than being
	// handed a blank it cannot recover from.
	RequesterName  string
	RequesterEmail string
}

// Repository stores and reads tickets.
//
// The write methods own their transactions, because the fact and the cache have
// to commit together (docs/spec.md §4.1). They take an already-resolved
// SLAClock for the reason given on that type: no I/O belonging to another
// module happens inside them.
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
	//
	// All three share the ForRequester suffix and differ only in what they
	// return. They used to read ListByRequester, GetForRequester and
	// HistoryForRequester: two prepositions for one relationship, and a Get
	// prefix on the only one that had it. Which was which had to be remembered
	// rather than worked out.
	ListForRequester(ctx context.Context, f ListFilter) ([]domain.Ticket, error)

	// ListForQueue reads every ticket, scoped by nothing. It is reachable only
	// from handlers mounted behind a role check, and that placement is the
	// authorization — there is no predicate here to forget.
	ListForQueue(ctx context.Context, f QueueFilter) ([]QueueEntry, error)
	OneForRequester(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error)
	HistoryForRequester(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error)
}
