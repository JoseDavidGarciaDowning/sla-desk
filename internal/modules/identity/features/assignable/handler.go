// Package assignable answers who may hold a ticket.
//
// Two questions, one concept: list the staff a ticket may be handed to, and
// say whether one particular id is among them. They are one feature because
// they are the same rule read two ways — the roster is the predicate applied
// to everyone, and the check is the predicate applied to one — and a change to
// what "may hold tickets" means has to land on both or neither.
//
// The predicate lives in the query rather than in a filter here, for the reason
// every other read in this project gives: a forgotten check in Go must not be
// enough to widen an answer. A customer cannot appear in the roster even if
// List is called from somewhere it should not be.
package assignable

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
)

// Users is the persistence this use case needs.
type Users interface {
	// ByID reads by our own primary key, returning domain.ErrNoSuchUser when
	// there is no row.
	ByID(ctx context.Context, id uuid.UUID) (domain.User, error)

	// Assignable lists the users a ticket may be handed to. The predicate lives
	// in the query, so a customer cannot appear in the result whatever the
	// caller does.
	Assignable(ctx context.Context) ([]domain.User, error)
}

// Handler executes the use case.
type Handler struct {
	users Users
}

func New(users Users) *Handler { return &Handler{users: users} }

// List returns the staff a ticket may be handed to — agents and admins.
func (h *Handler) List(ctx context.Context) ([]domain.User, error) {
	return h.users.Assignable(ctx)
}

// CanHoldTickets answers whether a user is one of ours who can be put on a
// ticket.
//
// It answers false for an id that names nobody, rather than reporting a missing
// user. The caller is deciding whether to accept an assignee, and "this person
// does not exist" and "this person is a customer" have to be the same answer
// there: two different ones would let a caller enumerate which uuids name real
// users.
//
// The roles are named here rather than by the caller, because what counts as
// privileged is this module's business — the RBAC matrix in docs/spec.md §4.3
// is about our users, and the ticket module never learns the word "agent".
//
// Named for the question the ticket module asks rather than for our answer to
// it. It is what satisfies that module's AssigneeDirectory, which names only
// a uuid and a bool, so neither module imports the other and the composition
// root connects the two ends (docs/adr/0005).
func (h *Handler) CanHoldTickets(ctx context.Context, id uuid.UUID) (bool, error) {
	user, err := h.users.ByID(ctx, id)
	if errors.Is(err, domain.ErrNoSuchUser) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return user.Role == domain.RoleAgent || user.Role == domain.RoleAdmin, nil
}
