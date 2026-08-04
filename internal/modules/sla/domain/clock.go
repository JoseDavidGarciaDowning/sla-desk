package domain

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

// Phase is one segment of a ticket's life: the instant something changed, and
// whether the SLA clock ran from that instant onward.
//
// This used to be a status — `{To: ticket.StatusOpen, At: t}` — and reading it
// meant calling ticket.Status.RunsClock(), which is why this package imported
// the ticket module at all. The boolean is the only part of a status this
// package was ever entitled to know.
//
// The division of labour is now exact, and it is the reason the import is gone
// rather than merely redirected: **the ticket module decides which statuses
// burn budget; this module decides how much time that is.** Adding a fifth
// status is a change over there and no change at all here.
type Phase struct {
	At      time.Time
	Running bool
}

// ClockState is the derived SLA state stored for quick access.
//
// It can always be rebuilt from the policy and the timeline.
type ClockState struct {
	BudgetUsed   time.Duration
	RunningSince *time.Time
	DueAt        *time.Time
}

// Reconstruct calculates the current SLA state from a timeline.
//
// It is the single source of truth for SLA deadline calculation, and it is a
// method on Policy because a resolved policy is precisely the thing that can
// answer this without touching anything.
func (p Policy) Reconstruct(timeline []Phase) (ClockState, error) {
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
// Which transitions are legal is not checked here — that is the ticket module's
// state machine, and this package has no opinion on it.
func validate(timeline []Phase) error {
	if len(timeline) == 0 {
		return ErrEmptyTimeline
	}

	// A ticket is created open, so its first phase always runs the clock. A
	// timeline that starts paused did not come from a ticket, and computing
	// from it would silently produce a plausible-looking wrong deadline.
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
