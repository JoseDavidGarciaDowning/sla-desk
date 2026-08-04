package api

import (
	"context"
	"errors"

	slaapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	sladomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// This file and identity.go are the only places in the codebase that name two
// business modules at once, which is what makes this package the composition
// point. Both move to internal/app in the next step; nothing about them changes
// when they do, which is the test of whether the translation is real work or
// ceremony.
//
// Nothing here contains a business rule. If a decision has to be made, it
// belongs in a module.

// SLAPolicies adapts the SLA module to the contract the ticket module declared.
//
// Exported because cmd/api builds it. The types on either side hold the same
// strings and mean different things, so the translation is real — see
// docs/adr/0005.
type SLAPolicies struct {
	Calculator *slaapp.Calculator
}

var _ ticketapp.SLAPolicies = SLAPolicies{}

func (s SLAPolicies) ForPriority(ctx context.Context, p ticketdomain.Priority) (ticketapp.SLAClock, error) {
	// ticketdomain.Priority and sladomain.Priority carry the same four strings
	// and are different types on purpose: one is how urgent a requester says a
	// ticket is, the other is the key a budget is filed under. Both are
	// constrained by a CHECK in their own module's table, so the conversion
	// cannot widen either vocabulary.
	policy, err := s.Calculator.ForPriority(ctx, sladomain.Priority(p))
	if err != nil {
		return nil, translateSLAError(err)
	}
	return slaClock{policy: policy}, nil
}

func (s SLAPolicies) ForPolicy(ctx context.Context, id int64) (ticketapp.SLAClock, error) {
	policy, err := s.Calculator.ForPolicy(ctx, id)
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
// connections at once.
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

	state, err := sladomain.Reconstruct(c.policy, phases)
	if err != nil {
		return ticketapp.ClockState{}, err
	}

	return ticketapp.ClockState{
		BudgetUsed:   state.BudgetUsed,
		RunningSince: state.RunningSince,
		DueAt:        state.DueAt,
	}, nil
}
