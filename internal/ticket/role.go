package ticket

// Role is what a user may do, as defined by the RBAC matrix in docs/spec.md §4.3.
//
// It lives in this package rather than in internal/auth because the transition
// rules take the actor's role as an argument, and internal/ticket must not
// import anything. The dependency runs the other way: internal/auth imports this
// package to resolve a verified identity into a role.
//
// The role is read from our users table and never from a Clerk token or any
// other client input. A client-supplied role claim is not a role.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleAgent    Role = "agent"
	RoleAdmin    Role = "admin"
)
