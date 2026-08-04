package domain

// ActorRole is the role someone held when they acted on a ticket, as defined by
// the RBAC matrix in docs/spec.md §4.3.
//
// It lives here rather than in the identity module because the transition rules
// take it as an argument and this package must import nothing. The identity
// module declares its own Role over the same three strings, and the duplication
// is deliberate rather than an oversight:
//
//   - identity.Role is what someone *is*. It is a column on users, and it
//     changes when they are promoted.
//   - ActorRole is what someone *was when they acted*. It is denormalised onto
//     ticket_status_history precisely so that promoting an agent does not
//     rewrite what the audit trail says they were.
//
// One shared type would make those the same value, and the second property is
// the one an audit trail exists for. internal/app maps between them and is the
// only package allowed to. See docs/adr/0005.
//
// The role is read from our users table and never from a Clerk token or any
// other client input. A client-supplied role claim is not a role.
type ActorRole string

const (
	RoleCustomer ActorRole = "customer"
	RoleAgent    ActorRole = "agent"
	RoleAdmin    ActorRole = "admin"
)
