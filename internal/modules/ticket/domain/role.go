package domain

// Role is what an actor was at the moment they acted, as defined by the RBAC
// matrix in docs/spec.md §4.3.
//
// The identity module declares a Role holding the same three strings, and that
// duplication is deliberate. Theirs is what someone *is*; this one is
// denormalised onto ticket_status_history, so promoting an agent must not
// rewrite the audit trail. Two values that read alike and cannot be the same
// value.
//
// The conversion between them lives in the composition root, which is the only
// place allowed to know both modules exist. See docs/adr/0005.
//
// It is read from our users table and never from a Clerk token or any other
// client input. A client-supplied role claim is not a role.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleAgent    Role = "agent"
	RoleAdmin    Role = "admin"
)

// ActorRoles is every role the state machine keys its edges on, least
// privileged first.
//
// Ordered rather than alphabetical, so a generated file and a rendered list
// both read as an escalation. The transport layer does not accept a role from
// anyone — it comes from the authenticated caller — so this is a vocabulary for
// describing the rules, not for validating input.
func ActorRoles() []Role {
	return []Role{RoleCustomer, RoleAgent, RoleAdmin}
}
