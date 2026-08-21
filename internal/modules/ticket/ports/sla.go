// Package ports holds the contracts this module needs from outside itself, and
// the ones more than one of its features share.
//
// Every interface here is declared by the consumer — this module — and never by
// whoever implements it (docs/spec.md §8). That is what makes the module
// independently evolvable: it states what it needs in its own vocabulary, and
// the composition root is responsible for finding something that can provide
// it. Nothing here imports another business module.
//
// What belongs here is narrow on purpose. A contract used by exactly one
// feature belongs *in* that feature, declared beside the handler that needs it.
// This package is for what crosses features: the SLA clock, which create and
// transition both resolve, and the caller, which every endpoint reads.
package ports

import (
	"context"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// SLAClock is a resolved SLA policy: it computes clock state from a timeline
// with no further I/O.
//
// The split between resolving and computing is not incidental — it is what lets
// the repository do its read *before* opening its transaction. Calling out to
// another module mid-transaction would hold a second pooled connection for the
// transaction's duration, and pgxpool defaults to max(4, NumCPU), so enough
// concurrent writes would wait on each other (docs/adr/0008).
type SLAClock interface {
	// PolicyID is the policy this clock was resolved from, snapshotted onto the
	// ticket so that editing a policy does not move existing deadlines.
	PolicyID() int64

	// Compute is pure. It touches nothing and may be called inside a
	// transaction.
	Compute(timeline []domain.Phase) (ClockState, error)
}

// ClockState is what an SLA calculation yields.
//
// RunningSince and DueAt are both nil or both set: a paused ticket has no
// deadline and therefore cannot breach (docs/spec.md §4.2). The tickets table
// carries CHECK constraints saying the same thing, so an implementation that
// got this wrong would be refused by the database rather than stored.
type ClockState struct {
	BudgetUsed   time.Duration
	RunningSince *time.Time
	DueAt        *time.Time
}

// SLAPolicies resolves SLA clocks.
//
// Implemented in the composition root against the SLA module. This module does
// not know that module exists, and states its needs in its own Priority type.
type SLAPolicies interface {
	// ForPriority resolves the policy a new ticket should be created under.
	ForPriority(ctx context.Context, p domain.Priority) (SLAClock, error)

	// ForPolicy reads the policy a ticket was already snapshotted with.
	ForPolicy(ctx context.Context, id int64) (SLAClock, error)
}
