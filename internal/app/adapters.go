package app

import (
	"context"
	"errors"

	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	slaapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	sladomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// This file is the only place in the codebase that imports two business modules
// at once, and that is the whole point of it.
//
// Each module declares what it needs in its own vocabulary and implements
// nobody else's interface. The types on either side of these adapters hold the
// same strings and mean different things, so the translation is real work
// rather than ceremony — see docs/adr/0005. Nothing here contains a business
// rule: if a decision has to be made, it belongs in a module, not in the wiring.

// slaPolicies adapts the SLA module to the contract the ticket module declared.
type slaPolicies struct {
	calculator *slaapp.Calculator
}

var _ ticketapp.SLAPolicies = slaPolicies{}

func (s slaPolicies) ForPriority(ctx context.Context, p ticketdomain.Priority) (ticketapp.SLAClock, error) {
	// ticketdomain.Priority and sladomain.Priority carry the same four strings
	// and are different types on purpose: one is how urgent a requester says a
	// ticket is, the other is the key a budget is filed under. Both are
	// constrained by a CHECK in their own module's table, so the conversion
	// cannot widen either vocabulary.
	policy, err := s.calculator.ForPriority(ctx, sladomain.Priority(p))
	if err != nil {
		return nil, translateSLAError(err)
	}
	return slaClock{policy: policy}, nil
}

func (s slaPolicies) ForPolicy(ctx context.Context, id int64) (ticketapp.SLAClock, error) {
	policy, err := s.calculator.ForPolicy(ctx, id)
	if err != nil {
		return nil, translateSLAError(err)
	}
	return slaClock{policy: policy}, nil
}

// translateSLAError maps the SLA module's sentinels onto the ticket module's.
//
// Without this the ticket module would have to import the SLA module to match
// on its errors, which is the coupling the contract exists to avoid. The
// original is kept wrapped so the cause still reaches the logs.
func translateSLAError(err error) error {
	if errors.Is(err, slaapp.ErrNoPolicyForPriority) {
		return errors.Join(ticketapp.ErrNoSLAPolicy, err)
	}
	return err
}

// slaClock is a resolved SLA policy, presented as the ticket module's contract.
//
// Compute touches nothing: the policy was read before the caller opened its
// transaction, which is what stops a ticket write holding two pooled
// connections at once. See docs/adr/0006.
type slaClock struct {
	policy sladomain.Policy
}

var _ ticketapp.SLAClock = slaClock{}

func (c slaClock) PolicyID() int64 { return c.policy.ID }

func (c slaClock) Compute(timeline []ticketdomain.Phase) (ticketapp.ClockState, error) {
	phases := make([]sladomain.Phase, len(timeline))
	for i, p := range timeline {
		phases[i] = sladomain.Phase{At: p.At, Running: p.Running}
	}

	state, err := c.policy.Reconstruct(phases)
	if err != nil {
		return ticketapp.ClockState{}, err
	}

	return ticketapp.ClockState{
		BudgetUsed:   state.BudgetUsed,
		RunningSince: state.RunningSince,
		DueAt:        state.DueAt,
	}, nil
}

// callerFromContext adapts the identity module's authenticated user to the
// contract the ticket module declared.
//
// The role conversion is the interesting half. identity.Role is what someone
// *is*; ticket.ActorRole is what they *were when they acted*, denormalised onto
// the audit trail so that promoting an agent does not rewrite history. Reading
// one into the other at the moment of acting is exactly right, and is why they
// are not the same type.
func callerFromContext(ctx context.Context) (tickethttp.Caller, bool) {
	user, ok := identityhttp.UserFromContext(ctx)
	if !ok {
		return tickethttp.Caller{}, false
	}

	return tickethttp.Caller{
		ID:   user.ID,
		Role: actorRole(user.Role),
	}, true
}

// actorRole maps an identity role onto a ticket actor role.
//
// Exhaustive rather than a bare conversion. A role added to the identity module
// and not accounted for here would otherwise flow into ticket_status_history as
// a string the CHECK constraint rejects — a 500 at write time, on a path only
// exercised by whoever holds the new role. Failing to the least privileged role
// keeps that a permission error instead.
func actorRole(r identitydomain.Role) ticketdomain.ActorRole {
	switch r {
	case identitydomain.RoleAdmin:
		return ticketdomain.RoleAdmin
	case identitydomain.RoleAgent:
		return ticketdomain.RoleAgent
	case identitydomain.RoleCustomer:
		return ticketdomain.RoleCustomer
	default:
		return ticketdomain.RoleCustomer
	}
}
