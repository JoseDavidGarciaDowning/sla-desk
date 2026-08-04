// Package domain owns every deadline calculation in the system.
//
// Nothing here touches a database, an HTTP request, or the wall clock: `now` is
// always a parameter. Callers consume the values this package returns and never
// recompute them.
//
// See docs/spec.md §4.2 for the clock model, docs/adr/0001 for why a single
// calculation path exists, and docs/adr/0005 for why this package no longer
// knows what a ticket is.
package domain

import "time"

// Priority selects which SLA policy applies.
//
// This is the SLA module's own vocabulary, and it deliberately duplicates the
// four strings the ticket module also declares. The two are not the same
// concept even though they read alike: a ticket's priority is how urgent the
// requester says it is, and this one is the key a budget is filed under.
// Sharing one type would mean one module owning a type the other's schema
// constrains, and the boundary would exist in the folder layout and nowhere
// else.
//
// The cost is a mapping in internal/app, which is the only package allowed to
// know both. See docs/adr/0005.
type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// Policy is a resolved SLA budget: everything needed to compute a deadline,
// with no further I/O.
//
// That property is the point rather than an accident. Resolving a policy costs
// a database read; computing a clock from one does not. Keeping them apart is
// what lets a caller do the read *before* opening its own transaction, instead
// of holding two pooled connections at once — see docs/adr/0006.
type Policy struct {
	ID       int64
	Priority Priority
	Budget   time.Duration
	Schedule Schedule
}
