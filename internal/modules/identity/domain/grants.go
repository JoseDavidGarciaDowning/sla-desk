package domain

import (
	"errors"
	"fmt"
)

// ErrConflictingGrant reports a Clerk subject listed as both an agent and an
// admin.
var ErrConflictingGrant = errors.New("identity: a subject is granted two roles")

// RoleGrants answers what role a Clerk subject was granted by the operator.
//
// This is how someone becomes an agent, and it resolves docs/spec.md §12.4. The
// lists come from the process environment and never from a request, so §4.5
// still holds in full: a role claim in a token, and a role field in a webhook
// payload, are both ignored. What is trusted here is what we put on the server
// ourselves, which is the same trust already placed in DATABASE_URL.
//
// Why not a migration, which is the obvious alternative: a migration is static
// SQL, so granting a role from one would mean writing a person's clerk_user_id
// into a file committed to git — a value that differs between Clerk's
// development and production instances, and that names a row which does not
// exist yet, because it is written when that person first signs up. The
// migration would run at deploy time, match nothing, and report success. See
// tasks/slice-2/plan.md decision A.
//
// The zero value is usable and grants nothing.
type RoleGrants struct {
	bySubject map[string]Role
}

// NewRoleGrants builds the grants from two lists of Clerk subjects.
//
// It returns an error rather than picking a winner when a subject appears in
// both. Map iteration order in Go is deliberately random, so resolving it
// silently would make a deployed role depend on something no reader can see and
// no test can pin. The caller is expected to fail the boot on it, the same way
// an unusable Clerk webhook secret already does.
func NewRoleGrants(agents, admins []string) (RoleGrants, error) {
	bySubject := make(map[string]Role, len(agents)+len(admins))

	for _, subject := range agents {
		bySubject[subject] = RoleAgent
	}

	for _, subject := range admins {
		// A repeat within one list says the same thing twice and is a typo. A
		// subject in both lists says two different things and is a decision
		// nobody made.
		if existing, ok := bySubject[subject]; ok && existing != RoleAdmin {
			return RoleGrants{}, fmt.Errorf("%w: %s is listed as both %s and %s",
				ErrConflictingGrant, subject, existing, RoleAdmin)
		}
		bySubject[subject] = RoleAdmin
	}

	return RoleGrants{bySubject: bySubject}, nil
}

// RoleFor answers what a subject was granted, and RoleCustomer for everyone
// else.
//
// The default is the whole point: an unconfigured deploy has no agents, rather
// than promoting whoever happens to arrive first.
func (g RoleGrants) RoleFor(subject string) Role {
	if role, ok := g.bySubject[subject]; ok {
		return role
	}
	return RoleCustomer
}

// CountOf is how many distinct subjects hold a role. It exists so startup can
// log the size of each list without logging the identifiers in it — a log line
// naming who the agents are is an inventory of privileged accounts.
func (g RoleGrants) CountOf(role Role) int {
	n := 0
	for _, granted := range g.bySubject {
		if granted == role {
			n++
		}
	}
	return n
}
