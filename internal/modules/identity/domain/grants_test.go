package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestRoleGrantsAnswerCustomerForAnyoneNotListed(t *testing.T) {
	grants, err := NewRoleGrants(nil, nil)
	if err != nil {
		t.Fatalf("NewRoleGrants returned an unexpected error: %v", err)
	}

	if got := grants.RoleFor("user_2nobody"); got != RoleCustomer {
		t.Errorf("RoleFor(unlisted) = %q, want %q", got, RoleCustomer)
	}
}

// The default matters more than the grants do. An unconfigured deploy must have
// no agents at all rather than promoting whoever arrives first.
func TestEmptyGrantsPromoteNobody(t *testing.T) {
	grants, err := NewRoleGrants([]string{}, []string{})
	if err != nil {
		t.Fatalf("NewRoleGrants returned an unexpected error: %v", err)
	}

	for _, subject := range []string{"", "user_2abc", "admin"} {
		if got := grants.RoleFor(subject); got != RoleCustomer {
			t.Errorf("RoleFor(%q) = %q, want %q", subject, got, RoleCustomer)
		}
	}
}

func TestRoleGrantsResolveEachListToItsRole(t *testing.T) {
	grants, err := NewRoleGrants([]string{"user_2agent"}, []string{"user_2admin"})
	if err != nil {
		t.Fatalf("NewRoleGrants returned an unexpected error: %v", err)
	}

	cases := []struct {
		subject string
		want    Role
	}{
		{"user_2agent", RoleAgent},
		{"user_2admin", RoleAdmin},
		{"user_2other", RoleCustomer},
	}

	for _, c := range cases {
		if got := grants.RoleFor(c.subject); got != c.want {
			t.Errorf("RoleFor(%q) = %q, want %q", c.subject, got, c.want)
		}
	}
}

// A subject in both lists has no defensible answer. Resolving it by whichever
// map Go happens to range over first would make the deployed role depend on
// nothing a reader can see, so it stops the boot instead.
func TestASubjectInBothListsIsRefused(t *testing.T) {
	_, err := NewRoleGrants([]string{"user_2both"}, []string{"user_2both"})
	if !errors.Is(err, ErrConflictingGrant) {
		t.Fatalf("NewRoleGrants error = %v, want ErrConflictingGrant", err)
	}
}

// The error has to name the subject, because an operator reading a failed boot
// needs to know which entry to remove and the lists may hold many.
func TestTheConflictErrorNamesTheSubject(t *testing.T) {
	_, err := NewRoleGrants([]string{"user_2fine", "user_2both"}, []string{"user_2both"})
	if err == nil {
		t.Fatal("NewRoleGrants accepted a subject listed twice")
	}
	if !strings.Contains(err.Error(), "user_2both") {
		t.Errorf("error %q does not name the offending subject", err)
	}
}

// Repeating a subject within one list says the same thing twice. It is a typo,
// not a contradiction, and refusing to boot over it would be hostile.
func TestARepeatedSubjectInOneListIsHarmless(t *testing.T) {
	grants, err := NewRoleGrants([]string{"user_2agent", "user_2agent"}, nil)
	if err != nil {
		t.Fatalf("NewRoleGrants returned an unexpected error: %v", err)
	}

	if got := grants.RoleFor("user_2agent"); got != RoleAgent {
		t.Errorf("RoleFor = %q, want %q", got, RoleAgent)
	}
}

// Counting is what startup logs, so it must never be the length of the raw
// input: duplicates would inflate it and an operator would read a number that
// does not match how many people can sign in as agents.
func TestGrantedCountsDeduplicate(t *testing.T) {
	grants, err := NewRoleGrants([]string{"user_2agent", "user_2agent", "user_2other"}, []string{"user_2admin"})
	if err != nil {
		t.Fatalf("NewRoleGrants returned an unexpected error: %v", err)
	}

	if got := grants.CountOf(RoleAgent); got != 2 {
		t.Errorf("CountOf(agent) = %d, want 2", got)
	}
	if got := grants.CountOf(RoleAdmin); got != 1 {
		t.Errorf("CountOf(admin) = %d, want 1", got)
	}
}

// The zero value is what a caller gets by forgetting to build one. It must be
// the safe answer rather than a panic, because the alternative to "everyone is
// a customer" is an unrecoverable nil map dereference on the first request.
func TestTheZeroRoleGrantsPromoteNobody(t *testing.T) {
	var grants RoleGrants

	if got := grants.RoleFor("user_2agent"); got != RoleCustomer {
		t.Errorf("RoleFor on the zero value = %q, want %q", got, RoleCustomer)
	}
	if got := grants.CountOf(RoleAgent); got != 0 {
		t.Errorf("CountOf on the zero value = %d, want 0", got)
	}
}
