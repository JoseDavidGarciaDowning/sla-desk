package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Service is the ticket module's use cases.
type Service struct {
	repo Repository
	sla  SLAPolicies
	dir  AssigneeDirectory
}

func NewService(repo Repository, sla SLAPolicies, dir AssigneeDirectory) *Service {
	return &Service{repo: repo, sla: sla, dir: dir}
}

// Create writes a ticket and its opening history row.
//
// The SLA clock is resolved here, before the repository opens its transaction,
// and that ordering is this method's whole reason to exist rather than the
// handler calling the repository directly.
//
// internal/store used to do the read inside the transaction, on the
// transaction's own handle, which kept it to one connection but made the
// arrangement fragile: it only worked because the SLA module happened to accept
// a handle, and nothing said so. Resolving first makes it structural — the
// repository is handed a value that cannot do I/O.
func (s *Service) Create(ctx context.Context, in NewTicket) (domain.Ticket, error) {
	clock, err := s.sla.ForPriority(ctx, in.Priority)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("resolving the SLA policy: %w", err)
	}

	return s.repo.Create(ctx, in, clock)
}

// Transition moves a ticket and rebuilds its SLA clock.
//
// The policy id is read first, unlocked, so the clock can be resolved before
// the transaction opens. That read is safe because sla_policy_id is written
// once by Create and no query updates it — and the repository does not take it
// on trust: it asserts the locked row still agrees before writing anything.
func (s *Service) Transition(ctx context.Context, in StatusChange) (domain.Ticket, error) {
	policyID, err := s.repo.PolicyIDOf(ctx, in.TicketID)
	if err != nil {
		return domain.Ticket{}, err
	}

	clock, err := s.sla.ForPolicy(ctx, policyID)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("resolving the SLA policy: %w", err)
	}

	return s.repo.Transition(ctx, in, clock)
}

// List returns one page of the caller's tickets.
func (s *Service) List(ctx context.Context, f ListFilter) ([]domain.Ticket, error) {
	return s.repo.ListForRequester(ctx, f)
}

// Queue returns one page of every ticket, in deadline order.
//
// It takes no caller. The identity of whoever is reading changes nothing about
// what comes back — which is exactly what makes this the unscoped read, and why
// the route it hangs off has to be the one carrying the role check.
//
// Passing a caller in would be worse than useless: it would look like the
// answer depends on them, and the next person to read this would assume a
// predicate exists somewhere.
func (s *Service) Queue(ctx context.Context, f QueueFilter) ([]QueueEntry, error) {
	// A zero scope is a caller who forgot to set one. Defaulting it to "any"
	// would turn forgetting into "return every ticket", on the one query in
	// this module that has no predicate to fall back on.
	if f.Assignee == "" {
		return nil, ErrUnsetAssigneeScope
	}
	return s.repo.ListForQueue(ctx, f)
}

// Assign puts an agent on a ticket, or takes whoever is there off it.
//
// The directory is consulted only when there is somebody to ask about.
// Unassigning has no candidate, and asking anyway would make "take this off my
// plate" fail whenever the directory is unreachable.
//
// A directory that cannot answer is not a refusal. Reporting an outage as "that
// user may not hold tickets" would be a lie the caller acts on, so the cause
// travels up instead and the handler answers 500.
func (s *Service) Assign(ctx context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error) {
	if assignee != nil {
		ok, err := s.dir.CanHoldTickets(ctx, *assignee)
		if err != nil {
			return domain.Ticket{}, fmt.Errorf("checking whether the assignee may hold tickets: %w", err)
		}
		if !ok {
			return domain.Ticket{}, ErrNotAssignable
		}
	}

	return s.repo.Assign(ctx, ticketID, assignee)
}

// Detail returns any ticket, or ErrTicketNotFound.
//
// Like Queue it takes no caller, for the same reason: who is asking changes
// nothing about the answer. That is what makes it the unscoped read, and why
// the route it hangs off is the one carrying the role check.
func (s *Service) Detail(ctx context.Context, id uuid.UUID) (domain.Ticket, error) {
	return s.repo.OneByID(ctx, id)
}

// Timeline returns any ticket's full history.
func (s *Service) Timeline(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error) {
	return s.repo.Timeline(ctx, ticketID)
}

// Get returns one of the caller's tickets, or ErrTicketNotFound.
func (s *Service) Get(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	return s.repo.OneForRequester(ctx, id, requesterID)
}

// History returns a ticket's timeline as its requester may see it.
func (s *Service) History(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	return s.repo.HistoryForRequester(ctx, ticketID, requesterID)
}
