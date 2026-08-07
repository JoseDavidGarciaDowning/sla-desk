// Package ticket holds the ticket domain: its states and the rules that govern
// moving between them.
//
// It has no database, HTTP or SLA dependencies. In particular it must never
// import internal/sla — the dependency runs one way only, and an architecture
// test enforces it. See docs/adr/0002.
package domain

// Status is a ticket's position in the workflow described in docs/spec.md §4.1.
//
// StatusClosed is terminal: nothing transitions out of it. A closed ticket is
// never reopened; a new ticket is created and linked instead.
type Status string

const (
	StatusOpen     Status = "open"
	StatusPending  Status = "pending"
	StatusResolved Status = "resolved"
	StatusClosed   Status = "closed"
)

// Priority selects which SLA policy applies to a ticket. The budget attached to
// each priority lives in the sla_policies table, never in this package.
type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// Statuses is every status a ticket can hold, in workflow order.
//
// Ordered rather than alphabetical: open, pending, resolved, closed is the path
// a ticket walks, and a filter control that lists them in that order reads like
// the process it describes. The transport layer keeps its own copy for
// validation; this one exists so the domain can iterate its own vocabulary
// without asking a package above it.
func Statuses() []Status {
	return []Status{StatusOpen, StatusPending, StatusResolved, StatusClosed}
}
