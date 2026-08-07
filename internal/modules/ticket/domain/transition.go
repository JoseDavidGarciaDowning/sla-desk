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

// Edges is the state machine as data: from which status, to which, and by whom.
//
// A copy rather than the map itself. `allowed` decides every transition in this
// system, and handing out a reference to it would let any caller — or any test
// that forgot to restore something — rewrite the rules for the whole process.
//
// It exists so the generated frontend contract can carry the table instead of
// transcribing it. A UI has to know which moves to offer, and the alternative
// to generating that is a copy in TypeScript that drifts the day an edge
// changes: the button stays on screen and the API starts refusing it, which is
// exactly the failure the contract was introduced to prevent (T13).
//
// Statuses with no outgoing edges appear with an empty map rather than being
// left out, so a caller can look one up without a special case. closed is the
// only one today, and it is terminal on purpose (docs/spec.md §4.1).
func Edges() map[Status]map[Status][]Role {
	out := make(map[Status]map[Status][]Role, len(Statuses()))

	for _, from := range Statuses() {
		targets := make(map[Status][]Role, len(allowed[from]))
		for to, roles := range allowed[from] {
			targets[to] = append([]Role(nil), roles...)
		}
		out[from] = targets
	}

	return out
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
