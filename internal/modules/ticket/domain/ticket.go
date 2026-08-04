package domain

import (
	"time"

	"github.com/google/uuid"
)

// Ticket is a ticket as this module understands it.
//
// Deliberately not the generated row type. That would put the shape of the
// tickets table into every layer above, and make adding a column a change to
// the application and the transport at once. The mapping happens once, in the
// repository.
//
// The sla_* fields are a cache of what the status history says, never the
// source of truth. docs/spec.md §4.2: the history is the fact, and these are
// rebuilt from it on every write. A consistency test proves they agree.
type Ticket struct {
	ID          uuid.UUID
	RequesterID uuid.UUID
	AssigneeID  *uuid.UUID

	Title       string
	Description string
	Category    Category
	Priority    Priority
	Status      Status

	// SLAPolicyID is snapshotted at creation. Editing a policy must not
	// silently move the deadlines of tickets created under the old budget.
	SLAPolicyID int64

	SLAConsumed       time.Duration
	SLAClockStartedAt *time.Time
	SLADueAt          *time.Time
	SLABreachedAt     *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Breached reports whether the SLA has been marked breached.
//
// A method rather than a bare nil check at each call site: "breached" is a fact
// about a ticket, and the fact that it is stored as a nullable timestamp is
// this package's business.
func (t Ticket) Breached() bool { return t.SLABreachedAt != nil }

// HistoryEntry is one status change, as recorded in ticket_status_history.
//
// This table is the fact the SLA clock is rebuilt from. Nothing here is
// derived, and nothing is ever deleted (docs/spec.md §10).
type HistoryEntry struct {
	// FromStatus is nil on the entry that records the ticket's creation, which
	// is the only entry that moved from nowhere.
	FromStatus *Status
	ToStatus   Status

	ActorID   uuid.UUID
	ActorRole ActorRole
	Reason    *string

	CreatedAt time.Time
}

// Timeline reduces a history to the running/paused phases the SLA calculation
// works in.
//
// This is the whole of what the SLA module is entitled to know about a ticket.
// Which statuses burn budget is decided here, by Status.RunsClock, and the
// boolean is all that crosses the boundary. Adding a fifth status is a change
// in this package and no change at all in that one. See docs/adr/0005.
func Timeline(history []HistoryEntry) []Phase {
	phases := make([]Phase, len(history))
	for i, entry := range history {
		phases[i] = Phase{At: entry.CreatedAt, Running: entry.ToStatus.RunsClock()}
	}
	return phases
}

// Phase is one segment of a ticket's life: the instant something changed, and
// whether the SLA clock ran from that instant onward.
type Phase struct {
	At      time.Time
	Running bool
}
