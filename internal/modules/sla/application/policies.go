// Package application resolves SLA policies for callers.
//
// It is deliberately thin, and what it does *not* do is the interesting part:
// the arithmetic stays in the domain, on Policy, and is not wrapped in a
// service method here.
//
// That split is what makes this module safe to call from a caller that owns a
// transaction. Resolving a policy costs I/O; computing a clock from one does
// not. A caller resolves first and computes second, so no read of this module's
// data happens while somebody else's transaction is open.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
)

// ErrNoPolicyForPriority means no active SLA policy serves that priority.
//
// A ticket cannot be created without one: the budget is data, and there is no
// default hiding in the code to fall back on.
var ErrNoPolicyForPriority = errors.New("sla: no active policy for that priority")

// PolicyRepository reads stored SLA policies.
//
// Declared here rather than in the postgres package because this package is the
// consumer, and the consumer owns the interface (docs/spec.md §8). It returns
// domain values, so nothing above it ever sees a database row.
type PolicyRepository interface {
	// ActiveByPriority resolves the policy a new ticket should be created
	// under. It returns ErrNoPolicyForPriority when none is active.
	ActiveByPriority(ctx context.Context, p domain.Priority) (domain.Policy, error)

	// ByID reads the policy something was already created under.
	//
	// By id, not by priority: the policy is snapshotted at creation precisely
	// so that editing one does not silently move the deadlines of tickets that
	// already exist.
	ByID(ctx context.Context, id int64) (domain.Policy, error)
}

// Policies hands out stored SLA policies.
//
// Called Policies rather than Calculator, which is what it was named first: no
// method here computes anything. The arithmetic is on domain.Policy, and the
// package comment above says so — a type named for a calculation it does not
// perform sends whoever goes looking for the deadline maths to the wrong file.
type Policies struct {
	repo PolicyRepository
}

func NewPolicies(repo PolicyRepository) *Policies {
	return &Policies{repo: repo}
}

// ForPriority resolves the policy that serves a priority.
func (p *Policies) ForPriority(ctx context.Context, prio domain.Priority) (domain.Policy, error) {
	policy, err := p.repo.ActiveByPriority(ctx, prio)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("resolving the policy for %s: %w", prio, err)
	}
	return policy, nil
}

// ByID reads the policy something was snapshotted with.
//
// ByID rather than ForPolicy, which took a policy id and returned a policy and
// so named neither end of the trip. It matches PolicyRepository.ByID, which had
// the better name all along.
func (p *Policies) ByID(ctx context.Context, id int64) (domain.Policy, error) {
	policy, err := p.repo.ByID(ctx, id)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("reading policy %d: %w", id, err)
	}
	return policy, nil
}
