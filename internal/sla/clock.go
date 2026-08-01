package sla

import (
	"errors"
	"fmt"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// Errors returned when status history cannot be reconstructed.
var (
	ErrEmptyHistory         = errors.New("sla: status history is empty")
	ErrUnorderedHistory     = errors.New("sla: status history is not in chronological order")
	ErrHistoryMustStartOpen = errors.New("sla: status history must start in open")
)

// Schedule defines how SLA time is consumed and deadlines are calculated.
type Schedule interface {
	// Elapsed returns SLA time consumed between two moments.
	Elapsed(from, to time.Time) time.Duration
	// DueAt returns when the remaining SLA budget expires.
	DueAt(from time.Time, remaining time.Duration) time.Time
}

var _ Schedule = Always24x7{}

// Policy defines the SLA budget and schedule applied to a ticket.
type Policy struct {
	ID       int64
	Priority ticket.Priority
	Budget   time.Duration
	Schedule Schedule
}

// StatusChange represents a ticket status transition at a specific time.
type StatusChange struct {
	To ticket.Status
	At time.Time
}

// ClockState is the derived SLA state stored for quick access.
//
// It can always be rebuilt from the policy and status history.
type ClockState struct {
	BudgetUsed   time.Duration
	RunningSince *time.Time
	DueAt        *time.Time
}

// Reconstruct calculates the current SLA state from status history.
//
// It is the single source of truth for SLA deadline calculation.
func Reconstruct(p Policy, history []StatusChange) (ClockState, error) {
	if err := validate(history); err != nil {
		return ClockState{}, err
	}

	var (
		used         time.Duration
		runningSince *time.Time
	)

	// Accumulate closed open-state intervals. Keep the current interval open.
	for _, change := range history {
		running := change.To == ticket.StatusOpen

		switch {
		case running && runningSince == nil:
			started := change.At
			runningSince = &started
		case !running && runningSince != nil:
			used += p.Schedule.Elapsed(*runningSince, change.At)
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
// Status transition rules are enforced by the ticket state machine.
func validate(history []StatusChange) error {
	if len(history) == 0 {
		return ErrEmptyHistory
	}

	if first := history[0].To; first != ticket.StatusOpen {
		return fmt.Errorf("%w: starts in %q", ErrHistoryMustStartOpen, first)
	}

	for i := 1; i < len(history); i++ {
		if history[i].At.Before(history[i-1].At) {
			return fmt.Errorf("%w: entry %d (%s) precedes entry %d (%s)",
				ErrUnorderedHistory,
				i, history[i].At.Format(time.RFC3339),
				i-1, history[i-1].At.Format(time.RFC3339))
		}
	}

	return nil
}
