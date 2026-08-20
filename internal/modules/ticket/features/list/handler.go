// Package list returns one page of the caller's own tickets.
//
// The scope is the query's WHERE clause and not a check in Go: another
// customer's row never arrives to be filtered out, so there is no ownership
// test here to forget (docs/spec.md §4.3). Filters are further predicates on
// that same query rather than a replacement for it, which is what keeps the
// guarantee true with any combination of them applied.
package list

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Filter is one page of a requester's tickets.
//
// Status and Priority are pointers because absent and "the empty value" are
// different questions, and a filter that cannot express "no filter" silently
// returns nothing.
type Filter struct {
	RequesterID uuid.UUID
	Status      *domain.Status
	Priority    *domain.Priority

	// AfterCreatedAt and AfterID carry the last row of the previous page.
	// Keyset pagination, not OFFSET: see the query for why.
	AfterCreatedAt *time.Time
	AfterID        *uuid.UUID

	PageSize int32
}

// Tickets is the persistence this use case needs.
//
// The method carries the ForRequester suffix because that is the guarantee, not
// only the argument: an unscoped read of the same rows exists for agents and is
// a different method on the same adapter. One method taking an optional
// requester would let a caller ask the wrong question by leaving a field unset.
type Tickets interface {
	ListForRequester(ctx context.Context, f Filter) ([]domain.Ticket, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
}

func New(tickets Tickets) *Handler { return &Handler{tickets: tickets} }

func (h *Handler) Handle(ctx context.Context, f Filter) ([]domain.Ticket, error) {
	return h.tickets.ListForRequester(ctx, f)
}
