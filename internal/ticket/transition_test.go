package ticket_test

import (
	"errors"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// The complete legal edge set of docs/spec.md §4.1, written out rather than
// derived from the implementation. A table computed the way the code computes
// it would agree with any mistake the code made.
func TestEveryLegalTransition(t *testing.T) {
	cases := []struct {
		from  ticket.Status
		to    ticket.Status
		actor ticket.Role
	}{
		// An agent triages, answers and closes.
		{ticket.StatusOpen, ticket.StatusPending, ticket.RoleAgent},
		{ticket.StatusOpen, ticket.StatusResolved, ticket.RoleAgent},
		{ticket.StatusPending, ticket.StatusOpen, ticket.RoleAgent},
		{ticket.StatusPending, ticket.StatusResolved, ticket.RoleAgent},
		{ticket.StatusResolved, ticket.StatusOpen, ticket.RoleAgent},
		{ticket.StatusResolved, ticket.StatusClosed, ticket.RoleAgent},

		// An admin can do everything an agent can.
		{ticket.StatusOpen, ticket.StatusPending, ticket.RoleAdmin},
		{ticket.StatusResolved, ticket.StatusClosed, ticket.RoleAdmin},

		// A customer never sets a status directly, but replying moves a
		// pending ticket back to open and reopening moves a resolved one.
		// §4.3 calls this "only implicitly, by replying".
		{ticket.StatusPending, ticket.StatusOpen, ticket.RoleCustomer},
		{ticket.StatusResolved, ticket.StatusOpen, ticket.RoleCustomer},
	}

	for _, tc := range cases {
		t.Run(string(tc.actor)+": "+string(tc.from)+" to "+string(tc.to), func(t *testing.T) {
			got, err := ticket.Transition(tc.from, tc.to, tc.actor)
			if err != nil {
				t.Fatalf("Transition: %v", err)
			}
			if got != tc.to {
				t.Errorf("new status = %q, want %q", got, tc.to)
			}
		})
	}
}

// closed is terminal, and that is load-bearing rather than tidy: it is what
// makes the SLA clock and the audit trail unambiguous. A closed ticket is never
// reopened; a new one is created and linked.
func TestNothingLeavesClosed(t *testing.T) {
	for _, target := range []ticket.Status{
		ticket.StatusOpen, ticket.StatusPending, ticket.StatusResolved, ticket.StatusClosed,
	} {
		t.Run("to "+string(target), func(t *testing.T) {
			_, err := ticket.Transition(ticket.StatusClosed, target, ticket.RoleAdmin)
			if !errors.Is(err, ticket.ErrInvalidTransition) {
				t.Errorf("err = %v, want ErrInvalidTransition — even an admin cannot reopen a closed ticket", err)
			}
		})
	}
}

func TestIllegalTransitionsAreRejected(t *testing.T) {
	cases := []struct {
		name  string
		from  ticket.Status
		to    ticket.Status
		actor ticket.Role
	}{
		{"open straight to closed skips resolution", ticket.StatusOpen, ticket.StatusClosed, ticket.RoleAdmin},
		{"pending straight to closed", ticket.StatusPending, ticket.StatusClosed, ticket.RoleAdmin},
		{"resolved back to pending", ticket.StatusResolved, ticket.StatusPending, ticket.RoleAgent},
		{"open to open is not a transition", ticket.StatusOpen, ticket.StatusOpen, ticket.RoleAgent},
		{"pending to pending is not a transition", ticket.StatusPending, ticket.StatusPending, ticket.RoleAgent},
		{"an unknown current status", "archived", ticket.StatusOpen, ticket.RoleAdmin},
		{"an unknown target status", ticket.StatusOpen, "archived", ticket.RoleAdmin},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ticket.Transition(tc.from, tc.to, tc.actor); !errors.Is(err, ticket.ErrInvalidTransition) {
				t.Errorf("err = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

// A legal edge the actor may not take is forbidden, not invalid. The
// distinction matters at the HTTP boundary: one is 409 because the ticket is in
// the wrong state, the other is 403 because the caller is the wrong person.
func TestForbiddenIsDistinctFromInvalid(t *testing.T) {
	cases := []struct {
		name  string
		from  ticket.Status
		to    ticket.Status
		actor ticket.Role
	}{
		{"a customer cannot put their own ticket on hold", ticket.StatusOpen, ticket.StatusPending, ticket.RoleCustomer},
		{"a customer cannot resolve", ticket.StatusOpen, ticket.StatusResolved, ticket.RoleCustomer},
		{"a customer cannot close", ticket.StatusResolved, ticket.StatusClosed, ticket.RoleCustomer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ticket.Transition(tc.from, tc.to, tc.actor)

			if !errors.Is(err, ticket.ErrForbidden) {
				t.Errorf("err = %v, want ErrForbidden", err)
			}
			if errors.Is(err, ticket.ErrInvalidTransition) {
				t.Error("a permission failure was reported as an invalid transition; the HTTP layer would answer 409 instead of 403")
			}
		})
	}
}

// The clock runs in open and nowhere else (docs/spec.md §4.2). Expressing that
// here keeps internal/sla from having to know which statuses exist.
func TestOnlyOpenRunsTheClock(t *testing.T) {
	running := map[ticket.Status]bool{
		ticket.StatusOpen:     true,
		ticket.StatusPending:  false,
		ticket.StatusResolved: false,
		ticket.StatusClosed:   false,
	}

	for status, want := range running {
		if got := status.RunsClock(); got != want {
			t.Errorf("%q.RunsClock() = %v, want %v", status, got, want)
		}
	}
}
