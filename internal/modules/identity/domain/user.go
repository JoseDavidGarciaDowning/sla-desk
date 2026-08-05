// Package domain owns who a person is and what they are allowed to be.
//
// The split it exists to hold is the one in docs/spec.md §4.3: Clerk answers
// who you are, and this module answers what you may do by reading the role from
// our own users table. A role claim arriving from a client is not a role.
//
// Nothing here touches a database, an HTTP request or Clerk's SDK.
package domain

import "github.com/google/uuid"

// Role is what a user may do, as defined by the RBAC matrix in docs/spec.md §4.3.
//
// This is the identity module's own vocabulary. internal/ticket declares a Role
// holding the same three strings, and that duplication is deliberate rather
// than an oversight: the role on a users row is what someone *is*, and the role
// on a status history row is what someone *was when they acted*. Promoting an
// agent must not rewrite the audit trail, so the two cannot be the same value
// even though they read alike.
//
// The conversion between them lives in the caller — today internal/api, and
// internal/app once the modules are wired there. See docs/adr/0005.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleAgent    Role = "agent"
	RoleAdmin    Role = "admin"
)

// User is one of our users: a Clerk identity we hold a row for.
//
// The ID is a uuid.UUID rather than the driver's own type, and that is what
// keeps this package testable with nothing running. internal/auth carried a
// pgtype.UUID here and noted the decision belonged to whoever first needed
// something better — a domain that must not import a database driver is that
// moment.
//
// The role is never read from a token. It comes from the users table and
// nowhere else.
type User struct {
	ID          uuid.UUID
	ClerkUserID string
	Email       string
	Name        string
	Role        Role
}

// Identity is what Clerk knows about a person that their session token does not
// carry. The token holds the subject and nothing else we need.
//
// Deliberately without a role: see docs/spec.md §4.5. The provisioning query
// writes the role as a literal and leaves it out of the conflict clause, so a
// webhook payload can neither create an agent nor demote one — and a struct
// with nowhere to put a role cannot be talked into carrying one.
type Identity struct {
	Email string
	Name  string
}
