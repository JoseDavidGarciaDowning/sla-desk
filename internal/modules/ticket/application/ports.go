// Package application holds the ticket use cases that have not moved into
// features yet, and the contracts they still need.
//
// It is on its way out. Four of its nine use cases now live in features/, each
// declaring the narrow port it needs beside the handler that uses it; what is
// left here are the agent's five, which move in the next slice. When they do,
// this package goes away and Service with it.
//
// Nothing here is a pattern to copy. New use cases go in features/.
package application

import (
	"context"
	"errors"
	"time"

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
var ErrNotAssignable = errors.New("ticket: that user may not hold tickets")

// AssigneeDirectory answers whether an id may be put on a ticket.
//
// Declared by the consumer and implemented in the composition root against the
// identity module (docs/adr/0005). assignee_id is a bare foreign key to users,
// so the database will happily accept a customer's id there; this module cannot
// check it, because it does not know the identity module exists.
//
// So it asks the question in its own words. "May this person hold tickets" is
// what this module needs to know; that the answer happens to be "their role is
// agent or admin" is the other module's business.
//
// Rejected: a CHECK constraint or a trigger joining users.role. It would
// enforce the rule at the right layer but freeze the answer — demoting an agent
// who still holds open tickets would then fail at write time, on unrelated
// updates to those rows.
type AssigneeDirectory interface {
	CanHoldTickets(ctx context.Context, id uuid.UUID) (bool, error)
}

// StatusChange is a request to move a ticket.
type StatusChange struct {
	TicketID  uuid.UUID
	Target    domain.Status
	ActorID   uuid.UUID
	ActorRole domain.Role
	Reason    *string
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
// Deliberately not the customer list's filter with the requester made optional.
// The two are different reads with different guarantees: the customer's always
// carries a requester and its query always scopes by one, and merging them
// would put the distinction in a field a caller can leave unset
// (tasks/slice-2/plan.md decision B).
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

// Repository is what the agent use cases still need from storage.
//
// Four methods shorter than it was: creating, listing, reading and reading the
// timeline of a customer's own ticket are now declared by the features that do
// them, one narrow port each. What remains is the agent's five plus PolicyIDOf,
// and it shrinks to nothing in the next slice.
type Repository interface {
	Transition(ctx context.Context, in StatusChange, clock ports.SLAClock) (domain.Ticket, error)

	// PolicyIDOf reads which policy a ticket was created under, and nothing
	// else. It exists so the clock can be resolved before Transition opens its
	// transaction.
	PolicyIDOf(ctx context.Context, id uuid.UUID) (int64, error)

	// ListForQueue reads every ticket, scoped by nothing. It is reachable only
	// from handlers mounted behind a role check, and that placement is the
	// authorization — there is no predicate here to forget.
	ListForQueue(ctx context.Context, f QueueFilter) ([]QueueEntry, error)

	// OneByID and Timeline are the unscoped reads of a single ticket, for a
	// caller who is not its requester. They sit beside their ForRequester
	// counterparts in the same adapter rather than replacing them: an agent
	// reading a ticket and a customer reading their own are different questions
	// with different answers, and one method that took an optional requester
	// would let a caller ask the wrong one by leaving a field unset.
	OneByID(ctx context.Context, id uuid.UUID) (domain.Ticket, error)
	Timeline(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error)

	// Assign writes the assignee and nothing else. A nil assignee unassigns.
	//
	// It writes no ticket_status_history row, and that is a decision rather
	// than an omission (tasks/slice-2/plan.md §E): the history is the fact the
	// SLA clock is rebuilt from, and assignment does not move a ticket's
	// status. A row for it would pad the timeline Reconstruct walks, and the
	// consistency test would be right to fail.
	Assign(ctx context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error)
}
