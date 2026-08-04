package sla

import (
	"errors"
	"fmt"
	"time"
)

// Errors returned when a timeline cannot be reconstructed.
var (
	ErrEmptyTimeline            = errors.New("sla: timeline is empty")
	ErrUnorderedTimeline        = errors.New("sla: timeline is not in chronological order")
	ErrTimelineMustStartRunning = errors.New("sla: timeline must start with the clock running")
)

// Schedule defines how SLA time is consumed and deadlines are calculated.
type Schedule interface {
	// Elapsed returns SLA time consumed between two moments.
	Elapsed(from, to time.Time) time.Duration
	// DueAt returns when the remaining SLA budget expires.
	DueAt(from time.Time, remaining time.Duration) time.Time
}

var _ Schedule = Always24x7{}

// Priority is the key a budget is filed under.
//
// It deliberately duplicates the four strings the ticket package also declares,
// because the two are not the same concept even though they read alike: a
// ticket's priority is how urgent the requester says it is, and this one is
// which row of the policy table applies. Sharing one type would mean one
// package owning a vocabulary the other's schema constrains, and the boundary
// would exist in the folder layout and nowhere else.
//
// Both columns carry a CHECK constraint, so the conversion between them cannot
// widen either vocabulary.
type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// Policy defines the SLA budget and schedule applied under a priority.
type Policy struct {
	ID       int64
	Priority Priority
	Budget   time.Duration
	Schedule Schedule
}

// Phase is one segment of a timeline: the instant something changed, and
// whether the clock was consuming budget from that instant on.
//
// This used to be a ticket status and a time, which meant this package had to
// know that tickets exist, that they have statuses, and which of those statuses
// the clock runs in. It never needed any of that — it needed a boolean.
//
// The division of labour is now exact, and it is why the import is gone rather
// than merely redirected: the ticket package decides which statuses burn
// budget; this package decides how much time that is. A fifth status is a
// change over there and no change at all here.
type Phase struct {
	At      time.Time
	Running bool
}

// ClockState is the derived SLA state stored for quick access.
//
// It can always be rebuilt from the policy and status history.
type ClockState struct {
	BudgetUsed   time.Duration
	RunningSince *time.Time
	DueAt        *time.Time
}

// Reconstruct calculates the current SLA state from a timeline.
//
// It is the single source of truth for SLA deadline calculation.
func Reconstruct(p Policy, timeline []Phase) (ClockState, error) {
	if err := validate(timeline); err != nil {
		return ClockState{}, err
	}

	var (
		used         time.Duration
		runningSince *time.Time
	)

	// Accumulate closed running intervals. Keep the current interval open.
	for _, phase := range timeline {
		switch {
		case phase.Running && runningSince == nil:
			started := phase.At
			runningSince = &started
		case !phase.Running && runningSince != nil:
			used += p.Schedule.Elapsed(*runningSince, phase.At)
			runningSince = nil
		}
	}

	state := ClockState{
		BudgetUsed:   used,
		RunningSince: runningSince,
	}
	if runningSince != nil {
		due := p.Schedule.DueAt(*runningSince, p.Budget-used)
		state.DueAt = &due
	}

	return state, nil
}

// validate checks the minimum guarantees required for reconstruction.
//
// Transition rules are enforced by the ticket state machine, not here.
func validate(timeline []Phase) error {
	if len(timeline) == 0 {
		return ErrEmptyTimeline
	}

	// This used to read `history[0].To != ticket.StatusOpen`, which is a rule
	// about tickets wearing this package's name. The two are not the same
	// statement, and separating them is the point:
	//
	//   "a ticket starts in open"                 — a ticket rule
	//   "a timeline starts with the clock running" — an SLA rule
	//
	// Both are true, they agree because StatusOpen.RunsClock() is true, and
	// neither has to know the other exists. What was one rule written in the
	// wrong package turns out to be two rules, each already at home.
	if !timeline[0].Running {
		return ErrTimelineMustStartRunning
	}

	for i := 1; i < len(timeline); i++ {
		if timeline[i].At.Before(timeline[i-1].At) {
			return fmt.Errorf("%w: entry %d (%s) precedes entry %d (%s)",
				ErrUnorderedTimeline,
				i, timeline[i].At.Format(time.RFC3339),
				i-1, timeline[i-1].At.Format(time.RFC3339))
		}
	}

	return nil
}
