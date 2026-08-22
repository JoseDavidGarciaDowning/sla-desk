// Package resolve reads the SLA policy that governs a ticket.
//
// It is deliberately thin, and what it does *not* do is the interesting part:
// the arithmetic stays in the domain, on Policy, and is not wrapped in a method
// here.
//
// That split is what makes this module safe to call from a caller that owns a
// transaction. Resolving a policy costs I/O; computing a clock from one does
// not. A caller resolves first and computes second, so no read of this module's
// data happens while somebody else's transaction is open (docs/adr/0007,
// docs/adr/0008).
//
// One feature, not two. ForPriority and ByID answer the same question — which
// policy applies — under two keys, and the second exists only because the
// policy is snapshotted at creation so that editing one does not move the
// deadlines of tickets that already exist. Splitting them would put two halves
// of one rule in two directories.
package resolve

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

// Policies reads stored SLA policies.
//
// Declared here rather than in the postgres package because this package is the
// consumer, and the consumer owns the interface (docs/spec.md §8). It returns
// domain values, so nothing above it ever sees a database row.
type Policies interface {
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

// Handler hands out stored SLA policies.
//
// Called Handler like every other use case in the codebase, and holding no
// arithmetic — the same reason the type it replaced was called Policies rather
// than Calculator. A type named for a calculation it does not perform sends
// whoever goes looking for the deadline maths to the wrong file.
type Handler struct {
	policies Policies
}

func New(policies Policies) *Handler { return &Handler{policies: policies} }

// ForPriority resolves the policy that serves a priority.
func (h *Handler) ForPriority(ctx context.Context, prio domain.Priority) (domain.Policy, error) {
	policy, err := h.policies.ActiveByPriority(ctx, prio)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("resolving the policy for %s: %w", prio, err)
	}
	return policy, nil
}

// ByID reads the policy something was snapshotted with.
//
// ByID rather than ForPolicy, which took a policy id and returned a policy and
// so named neither end of the trip. It matches Policies.ByID, which had the
// better name all along.
func (h *Handler) ByID(ctx context.Context, id int64) (domain.Policy, error) {
	policy, err := h.policies.ByID(ctx, id)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("reading policy %d: %w", id, err)
	}
	return policy, nil
}
