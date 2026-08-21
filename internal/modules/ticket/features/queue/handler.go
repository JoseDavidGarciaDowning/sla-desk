// Package queue reads every ticket, in deadline order.
//
// There is no requester predicate in the query and none here either, and that
// is the decision recorded in tasks/slice-2/plan.md §B: the alternative was
// passing the caller's role into the customer's query and skipping its
// predicate for agents, and a boolean in charge of a security predicate is a
// boolean that can be wrong.
//
// What stands in for the predicate is where the route is mounted — under the
// agent prefix, behind a role check (docs/adr/0011). That is why nothing in
// this package takes a caller for authorization purposes.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Scope says which tickets a page is about, by assignee.
//
// Three questions, not one: any assignee, one in particular, or none at all. A
// nil assignee id cannot express all three — it already means "no filter", so
// it cannot also mean "unassigned" — and a filter that silently answers the
// wrong question is worse than one that does not exist.
type Scope string

const (
	Any        Scope = "any"
	Unassigned Scope = "unassigned"
	One        Scope = "one"
)

// Filter is one page of every ticket, in deadline order.
//
// Deliberately not the customer list's filter with the requester made optional.
// The two are different reads with different guarantees: the customer's always
// carries a requester and its query always scopes by one, and merging them
// would put the distinction in a field a caller can leave unset
// (tasks/slice-2/plan.md decision B).
type Filter struct {
	Status   *domain.Status
	Priority *domain.Priority

	// Assignee says which of the three questions this page asks. The zero value
	// is not Any on purpose — an unset scope is a caller who forgot, and the
	// handler refuses it rather than quietly showing everything.
	Assignee   Scope
	AssigneeID *uuid.UUID

	// AfterDueAt and AfterID carry the last row of the previous page, in the
	// order this query sorts by. A keyset cursor is a position in the sort
	// order, so it carries the deadline and not created_at.
	//
	// AfterDueAt is already coalesced: a paused ticket's position is the
	// sentinel, because (NULL, id) > (x, id) is NULL and WHERE NULL drops the
	// row. See the query.
	AfterDueAt *time.Time
	AfterID    *uuid.UUID

	PageSize int32
}

// Entry is a ticket as the agent queue shows it.
//
// It carries the requester's display name because a queue of uuids is not
// usable, and it is a separate type rather than a field on domain.Ticket: the
// name belongs to the identity module and is joined in for this one read. A
// Ticket that sometimes carried a name and sometimes did not would make every
// other caller check.
type Entry struct {
	Ticket domain.Ticket

	// RequesterName is empty for anyone who signed up with an email and a
	// password, because Clerk holds no name for them. The email is carried
	// alongside so the transport can decide what to show rather than being
	// handed a blank it cannot recover from.
	RequesterName  string
	RequesterEmail string
}

// ErrUnsetScope reports a Filter built without one.
//
// A zero Scope means a caller forgot, and defaulting it to Any would turn
// forgetting into "show every ticket" — which on this query is the whole
// unscoped set.
var ErrUnsetScope = errors.New("ticket: the queue filter has no assignee scope")

// Tickets is the persistence this use case needs.
type Tickets interface {
	ListForQueue(ctx context.Context, f Filter) ([]Entry, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
}

func New(tickets Tickets) *Handler { return &Handler{tickets: tickets} }

// Handle returns one page of every ticket.
//
// It takes no caller. The identity of whoever is reading changes nothing about
// what comes back — which is exactly what makes this the unscoped read, and why
// the route it hangs off has to be the one carrying the role check.
//
// Passing a caller in would be worse than useless: it would look like the
// answer depends on them, and the next person to read this would assume a
// predicate exists somewhere.
func (h *Handler) Handle(ctx context.Context, f Filter) ([]Entry, error) {
	// A zero scope is a caller who forgot to set one. Defaulting it to Any
	// would turn forgetting into "return every ticket", on the one query in
	// this module that has no predicate to fall back on.
	if f.Assignee == "" {
		return nil, ErrUnsetScope
	}
	return h.tickets.ListForQueue(ctx, f)
}
