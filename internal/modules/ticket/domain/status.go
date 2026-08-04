// Package domain holds the ticket domain: its states, its vocabulary, and the
// rules that govern moving between them.
//
// It has no database, HTTP or SLA dependencies. In particular it must never
// import the SLA module — this package decides which statuses burn budget, and
// that module decides how much time that is. An architecture test enforces it.
// See docs/adr/0002 and docs/adr/0005.
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
