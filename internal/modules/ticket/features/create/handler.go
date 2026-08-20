// Package create opens a ticket.
//
// The SLA clock is resolved here, before the repository opens its transaction,
// and that ordering is this package's whole reason to exist rather than the
// HTTP adapter calling the repository directly. Resolving a policy is I/O
// belonging to another module; doing it inside our transaction would hold a
// second pooled connection for that transaction's duration (docs/adr/0008).
//
// Making it structural rather than conventional is the point: the repository is
// handed a value that *cannot* do I/O, so the ordering cannot be got wrong by
// someone who did not read this comment.
package create

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// Command is everything a caller supplies to open a ticket.
//
// The status, the clock and the policy are not in it — they are consequences,
// not inputs. Neither is the requester's identity trusted from a client: it
// arrives from the authenticated caller (docs/spec.md §4.3).
type Command struct {
	RequesterID uuid.UUID
	ActorRole   domain.Role
	Title       string
	Description string
	Category    domain.Category
	Priority    domain.Priority
}

// Tickets is the persistence this use case needs, and nothing more.
//
// Declared here, by the consumer, rather than in the package that implements it
// (docs/spec.md §8). One method, because one is what creating a ticket uses —
// a handler holding a ten-method repository interface can reach nine things it
// has no business touching, and a test for it has nine methods to stub.
//
// The implementation is the module's shared postgres adapter, which satisfies
// this and its siblings at once. That adapter imports this package to name
// Command in its signature: Go's structural typing covers method sets, not
// parameter types, so an identical struct declared elsewhere would not do. See
// docs/adr/0012 — the dependency points inward, from the adapter to the port,
// which is the direction it should.
type Tickets interface {
	Create(ctx context.Context, in Command, clock ports.SLAClock) (domain.Ticket, error)
}

// Handler executes the use case.
type Handler struct {
	tickets Tickets
	sla     ports.SLAPolicies
}

func New(tickets Tickets, sla ports.SLAPolicies) *Handler {
	return &Handler{tickets: tickets, sla: sla}
}

// Handle resolves the SLA policy, then writes the ticket and its opening
// history row in one transaction the repository owns.
func (h *Handler) Handle(ctx context.Context, cmd Command) (domain.Ticket, error) {
	clock, err := h.sla.ForPriority(ctx, cmd.Priority)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("resolving the SLA policy: %w", err)
	}

	return h.tickets.Create(ctx, cmd, clock)
}
