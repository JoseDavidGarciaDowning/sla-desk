package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// ErrTicketNotFound means no ticket has that id, or it is not the caller's.
//
// The two are deliberately the same error. A 403 would confirm that an id names
// a real ticket, which is exactly what a caller must not be able to learn
// (docs/spec.md §11), and the queries return nothing for both cases so nothing
// here can tell them apart either.
var ErrTicketNotFound = errors.New("ticket: no ticket with that id")

// Service is the ticket module's use cases.
type Service struct {
	repo Repository
	sla  SLAPolicies
}

func NewService(repo Repository, sla SLAPolicies) *Service {
	return &Service{repo: repo, sla: sla}
}

// Create writes a ticket and its opening history row.
//
// The SLA clock is resolved here, before the repository opens its transaction,
// and that ordering is this method's whole reason to exist rather than the
// handler calling the repository directly. See docs/adr/0006.
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
// once by Create and no query updates it; the repository asserts the locked row
// still agrees rather than trusting it.
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
	return s.repo.ListByRequester(ctx, f)
}

// Get returns one of the caller's tickets, or ErrTicketNotFound.
func (s *Service) Get(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	return s.repo.GetForRequester(ctx, id, requesterID)
}

// History returns a ticket's timeline as its requester may see it.
//
// An empty result is ErrTicketNotFound rather than an empty timeline. Every
// ticket has at least the entry recording its creation, so nothing comes back
// only when the ticket does not exist or is not the caller's.
func (s *Service) History(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	entries, err := s.repo.HistoryForRequester(ctx, ticketID, requesterID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrTicketNotFound
	}
	return entries, nil
}
