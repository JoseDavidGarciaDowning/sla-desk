package domain

import (
	"errors"
	"fmt"
	"slices"
)

var (
	// ErrInvalidTransition means the edge does not exist: the ticket is in the
	// wrong state for this move. The HTTP layer answers 409.
	ErrInvalidTransition = errors.New("ticket: transition is not allowed from this status")

	// ErrForbidden means the edge exists but this actor may not take it. The
	// HTTP layer answers 403.
	//
	// Distinct from ErrInvalidTransition on purpose. Collapsing them would tell
	// a customer that closing is impossible when it is merely not theirs to do,
	// and would answer 409 where 403 is the truth.
	ErrForbidden = errors.New("ticket: this role may not make that transition")
)

// allowed is the edge set drawn in docs/spec.md §4.1, with the roles permitted
// to take each edge.
//
// Note what is absent: nothing leaves closed. That is not an omission — a
// terminal state is what makes the SLA clock and the audit trail unambiguous,
// and a closed ticket is superseded by a new one rather than reopened.
//
// The spec's diagram labels one arrow "reopen (customer or agent)" and draws it
// touching closed, which contradicts the terminality stated twice in the same
// section. The prose wins: the edge is resolved → open.
var allowed = map[Status]map[Status][]Role{
	StatusOpen: {
		StatusPending:  {RoleAgent, RoleAdmin},
		StatusResolved: {RoleAgent, RoleAdmin},
	},
	StatusPending: {
		// A customer reaches this edge by replying rather than by asking for
		// it, but the edge is theirs to take.
		StatusOpen:     {RoleCustomer, RoleAgent, RoleAdmin},
		StatusResolved: {RoleAgent, RoleAdmin},
	},
	StatusResolved: {
		StatusOpen:   {RoleCustomer, RoleAgent, RoleAdmin},
		StatusClosed: {RoleAgent, RoleAdmin},
	},
}

// Transition validates a status change and returns the new status.
//
// Pure: no database, no clock, no context. Everything it needs is an argument,
// so every edge and every refusal is testable without anything running.
func Transition(current, target Status, actor Role) (Status, error) {
	edges, ok := allowed[current]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrInvalidTransition, current)
	}

	roles, ok := edges[target]
	if !ok {
		return "", fmt.Errorf("%w: %s to %s", ErrInvalidTransition, current, target)
	}

	if slices.Contains(roles, actor) {
		return target, nil
	}

	return "", fmt.Errorf("%w: %s may not move %s to %s", ErrForbidden, actor, current, target)
}

// RunsClock reports whether the SLA clock consumes budget in this status.
//
// It lives here rather than in internal/sla so that package never has to learn
// which statuses exist — it asks the status itself. docs/spec.md §4.2: the
// clock runs only in open, and is paused in pending, resolved and closed.
func (s Status) RunsClock() bool {
	return s == StatusOpen
}
