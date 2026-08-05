package domain_test

import (
	"errors"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// The complete legal edge set of docs/spec.md §4.1, written out rather than
// derived from the implementation. A table computed the way the code computes
// it would agree with any mistake the code made.
func TestEveryLegalTransition(t *testing.T) {
	cases := []struct {
		from  domain.Status
		to    domain.Status
		actor domain.Role
	}{
		// An agent triages, answers and closes.
		{domain.StatusOpen, domain.StatusPending, domain.RoleAgent},
		{domain.StatusOpen, domain.StatusResolved, domain.RoleAgent},
		{domain.StatusPending, domain.StatusOpen, domain.RoleAgent},
		{domain.StatusPending, domain.StatusResolved, domain.RoleAgent},
		{domain.StatusResolved, domain.StatusOpen, domain.RoleAgent},
		{domain.StatusResolved, domain.StatusClosed, domain.RoleAgent},

		// An admin can do everything an agent can.
		{domain.StatusOpen, domain.StatusPending, domain.RoleAdmin},
		{domain.StatusResolved, domain.StatusClosed, domain.RoleAdmin},

		// A customer never sets a status directly, but replying moves a
		// pending ticket back to open and reopening moves a resolved one.
		// §4.3 calls this "only implicitly, by replying".
		{domain.StatusPending, domain.StatusOpen, domain.RoleCustomer},
		{domain.StatusResolved, domain.StatusOpen, domain.RoleCustomer},
	}

	for _, tc := range cases {
		t.Run(string(tc.actor)+": "+string(tc.from)+" to "+string(tc.to), func(t *testing.T) {
			got, err := domain.Transition(tc.from, tc.to, tc.actor)
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
	for _, target := range []domain.Status{
		domain.StatusOpen, domain.StatusPending, domain.StatusResolved, domain.StatusClosed,
	} {
		t.Run("to "+string(target), func(t *testing.T) {
			_, err := domain.Transition(domain.StatusClosed, target, domain.RoleAdmin)
			if !errors.Is(err, domain.ErrInvalidTransition) {
				t.Errorf("err = %v, want ErrInvalidTransition — even an admin cannot reopen a closed ticket", err)
			}
		})
	}
}

func TestIllegalTransitionsAreRejected(t *testing.T) {
	cases := []struct {
		name  string
		from  domain.Status
		to    domain.Status
		actor domain.Role
	}{
		{"open straight to closed skips resolution", domain.StatusOpen, domain.StatusClosed, domain.RoleAdmin},
		{"pending straight to closed", domain.StatusPending, domain.StatusClosed, domain.RoleAdmin},
		{"resolved back to pending", domain.StatusResolved, domain.StatusPending, domain.RoleAgent},
		{"open to open is not a transition", domain.StatusOpen, domain.StatusOpen, domain.RoleAgent},
		{"pending to pending is not a transition", domain.StatusPending, domain.StatusPending, domain.RoleAgent},
		{"an unknown current status", "archived", domain.StatusOpen, domain.RoleAdmin},
		{"an unknown target status", domain.StatusOpen, "archived", domain.RoleAdmin},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := domain.Transition(tc.from, tc.to, tc.actor); !errors.Is(err, domain.ErrInvalidTransition) {
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
		from  domain.Status
		to    domain.Status
		actor domain.Role
	}{
		{"a customer cannot put their own ticket on hold", domain.StatusOpen, domain.StatusPending, domain.RoleCustomer},
		{"a customer cannot resolve", domain.StatusOpen, domain.StatusResolved, domain.RoleCustomer},
		{"a customer cannot close", domain.StatusResolved, domain.StatusClosed, domain.RoleCustomer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.Transition(tc.from, tc.to, tc.actor)

			if !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("err = %v, want ErrForbidden", err)
			}
			if errors.Is(err, domain.ErrInvalidTransition) {
				t.Error("a permission failure was reported as an invalid transition; the HTTP layer would answer 409 instead of 403")
			}
		})
	}
}

// The clock runs in open and nowhere else (docs/spec.md §4.2). Expressing that
// here keeps internal/sla from having to know which statuses exist.
func TestOnlyOpenRunsTheClock(t *testing.T) {
	running := map[domain.Status]bool{
		domain.StatusOpen:     true,
		domain.StatusPending:  false,
		domain.StatusResolved: false,
		domain.StatusClosed:   false,
	}

	for status, want := range running {
		if got := status.RunsClock(); got != want {
			t.Errorf("%q.RunsClock() = %v, want %v", status, got, want)
		}
	}
}
