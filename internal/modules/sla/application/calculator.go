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

// ForPolicy reads the policy something was snapshotted with.
func (c *Calculator) ForPolicy(ctx context.Context, id int64) (domain.Policy, error) {
	policy, err := c.policies.ByID(ctx, id)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("reading policy %d: %w", id, err)
	}
	return policy, nil
}
