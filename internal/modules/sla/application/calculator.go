// Package application resolves SLA policies for callers.
//
// It is deliberately thin. The arithmetic lives in the domain, on Policy, and
// the reason it is not exposed through a service method here is the property
// that makes this module safe to call from inside someone else's transaction:
// resolving costs I/O, computing does not. A caller resolves first, opens its
// transaction second, and computes with no connection of its own held. See
// docs/adr/0006.
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

// Calculator hands out resolved policies.
type Calculator struct {
	policies PolicyRepository
}

func NewCalculator(policies PolicyRepository) *Calculator {
	return &Calculator{policies: policies}
}

// ForPriority resolves the policy that serves a priority.
func (c *Calculator) ForPriority(ctx context.Context, p domain.Priority) (domain.Policy, error) {
	policy, err := c.policies.ActiveByPriority(ctx, p)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("resolving the policy for %s: %w", p, err)
	}
	return policy, nil
}

// ForPolicy reads the policy a ticket was snapshotted with.
func (c *Calculator) ForPolicy(ctx context.Context, id int64) (domain.Policy, error) {
	policy, err := c.policies.ByID(ctx, id)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("reading policy %d: %w", id, err)
	}
	return policy, nil
}
