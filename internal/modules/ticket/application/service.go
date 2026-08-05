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
}

func NewService(repo Repository, sla SLAPolicies) *Service {
	return &Service{repo: repo, sla: sla}
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

// Get returns one of the caller's tickets, or ErrTicketNotFound.
func (s *Service) Get(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	return s.repo.OneForRequester(ctx, id, requesterID)
}

// History returns a ticket's timeline as its requester may see it.
func (s *Service) History(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	return s.repo.HistoryForRequester(ctx, ticketID, requesterID)
}
