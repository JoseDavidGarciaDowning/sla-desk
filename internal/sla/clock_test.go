package sla

import (
	"errors"
	"testing"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// normalPolicy builds a policy with an explicit budget. Budgets are data
// (sla_policies), never constants in the code, so tests state theirs outright.
func normalPolicy(budget time.Duration) Policy {
	return Policy{
		ID:       1,
		Priority: ticket.PriorityNormal,
		Budget:   budget,
		Schedule: Always24x7{},
	}
}

func TestReconstruct_FreshTicketRunsFromCreation(t *testing.T) {
	created := at(1, 9, 0)
	p := normalPolicy(4 * time.Hour)

	got, err := Reconstruct(p, []StatusChange{
		{To: ticket.StatusOpen, At: created},
	})
	if err != nil {
		t.Fatalf("Reconstruct returned an unexpected error: %v", err)
	}

	if got.BudgetUsed != 0 {
		t.Errorf("BudgetUsed = %s, want 0 — nothing has been spent yet", got.BudgetUsed)
	}
	if got.RunningSince == nil {
		t.Fatal("RunningSince is nil, want the creation instant — a new ticket's clock runs")
	}
	if !got.RunningSince.Equal(created) {
		t.Errorf("RunningSince = %s, want %s",
			got.RunningSince.Format(time.RFC3339), created.Format(time.RFC3339))
	}
	if got.DueAt == nil {
		t.Fatal("DueAt is nil, want a deadline — a running clock always has one")
	}
	if want := at(1, 13, 0); !got.DueAt.Equal(want) {
		t.Errorf("DueAt = %s, want %s — the full 4h budget from creation",
			got.DueAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestReconstruct_FoldsHistoryIntoClockState exercises the core rule from
// docs/spec.md §4.2: the clock runs only while the ticket is open, and pauses in
// pending, resolved and closed.
//
// Expected values are worked examples computed by hand from the history, not
// recomputed the way the implementation does.
func TestReconstruct_FoldsHistoryIntoClockState(t *testing.T) {
	const budget = 4 * time.Hour

	tests := []struct {
		name    string
		history []StatusChange

		wantUsed  time.Duration
		wantSince *time.Time // nil means the clock is paused
		wantDue   *time.Time // nil means the clock is paused
	}{
		{
			name: "pending pauses the clock and banks the time spent open",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 10, 30)},
			},
			wantUsed:  90 * time.Minute,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "waiting on the customer costs the team nothing, however long it lasts",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 10, 30)},
				{To: ticket.StatusOpen, At: at(2, 14, 0)},
			},
			// 1h30 spent before the pause; 2h30 of budget survives the 27h wait.
			wantUsed:  90 * time.Minute,
			wantSince: new(at(2, 14, 0)),
			wantDue:   new(at(2, 16, 30)),
		},
		{
			name: "repeated pauses accumulate only the open stretches",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 10, 30)}, // +1h30
				{To: ticket.StatusOpen, At: at(2, 14, 0)},
				{To: ticket.StatusPending, At: at(2, 15, 0)}, // +1h
			},
			wantUsed:  150 * time.Minute,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "resolved stops the clock like pending does",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusResolved, At: at(1, 11, 0)},
			},
			wantUsed:  2 * time.Hour,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "closing a resolved ticket adds nothing — the clock was already stopped",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusResolved, At: at(1, 11, 0)},
				{To: ticket.StatusClosed, At: at(3, 9, 0)},
			},
			wantUsed:  2 * time.Hour,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "reopening a resolved ticket resumes the remaining budget, not a fresh one",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusResolved, At: at(1, 10, 0)},
				{To: ticket.StatusOpen, At: at(1, 11, 0)},
			},
			wantUsed:  1 * time.Hour,
			wantSince: new(at(1, 11, 0)),
			wantDue:   new(at(1, 14, 0)), // 3h left, not 4h
		},
		{
			name: "budget exhausted to the minute is still just paused",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 13, 0)},
			},
			wantUsed:  budget,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "an overspent ticket that reopens is due in the past, so the breach query catches it",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 14, 0)}, // 5h against a 4h budget
				{To: ticket.StatusOpen, At: at(2, 9, 0)},
			},
			wantUsed:  5 * time.Hour,
			wantSince: new(at(2, 9, 0)),
			wantDue:   new(at(2, 8, 0)), // one hour overdue the moment it resumes
		},
		{
			name: "a ticket paused the instant it was created has spent nothing",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 9, 0)},
			},
			wantUsed:  0,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "a zero-length pause changes nothing",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 9, 0)},
				{To: ticket.StatusPending, At: at(1, 10, 0)},
				{To: ticket.StatusOpen, At: at(1, 10, 0)},
			},
			wantUsed:  1 * time.Hour,
			wantSince: new(at(1, 10, 0)),
			wantDue:   new(at(1, 13, 0)),
		},
	}

	p := normalPolicy(budget)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reconstruct(p, tt.history)
			if err != nil {
				t.Fatalf("Reconstruct returned an unexpected error: %v", err)
			}

			if got.BudgetUsed != tt.wantUsed {
				t.Errorf("BudgetUsed = %s, want %s", got.BudgetUsed, tt.wantUsed)
			}
			assertInstant(t, "RunningSince", got.RunningSince, tt.wantSince)
			assertInstant(t, "DueAt", got.DueAt, tt.wantDue)
		})
	}
}

// TestReconstruct_RejectsMalformedHistory covers the preconditions internal/sla
// owns. Transition legality is deliberately NOT checked here — that rule lives in
// internal/ticket and is enforced at write time. See docs/adr/0002.
//
// A wrong ClockState is worse than a loud failure: a bad deadline looks entirely
// plausible. So each case also asserts the returned state is zero, leaving a
// caller who ignores the error with nothing usable.
func TestReconstruct_RejectsMalformedHistory(t *testing.T) {
	tests := []struct {
		name    string
		history []StatusChange
		wantErr error
	}{
		{
			name:    "empty history has no starting instant to compute from",
			history: nil,
			wantErr: ErrEmptyHistory,
		},
		{
			name: "history that runs backwards would corrupt the interval arithmetic",
			history: []StatusChange{
				{To: ticket.StatusOpen, At: at(1, 11, 0)},
				{To: ticket.StatusPending, At: at(1, 9, 0)},
			},
			wantErr: ErrUnorderedHistory,
		},
		{
			name: "a history that does not begin in open did not come from a ticket",
			history: []StatusChange{
				{To: ticket.StatusPending, At: at(1, 9, 0)},
			},
			wantErr: ErrHistoryMustStartOpen,
		},
	}

	p := normalPolicy(4 * time.Hour)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reconstruct(p, tt.history)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want one wrapping %v", err, tt.wantErr)
			}
			if got != (ClockState{}) {
				t.Errorf("state = %+v, want the zero value — a rejected history must yield nothing usable", got)
			}
		})
	}
}

// Equal timestamps are legal: a status can change twice at the same instant, and
// the "zero-length pause" case above depends on it. Only a strict decrease is an
// error.
func TestReconstruct_AcceptsEqualTimestamps(t *testing.T) {
	p := normalPolicy(4 * time.Hour)

	_, err := Reconstruct(p, []StatusChange{
		{To: ticket.StatusOpen, At: at(1, 9, 0)},
		{To: ticket.StatusPending, At: at(1, 10, 0)},
		{To: ticket.StatusOpen, At: at(1, 10, 0)},
	})
	if err != nil {
		t.Fatalf("equal consecutive timestamps must be accepted, got %v", err)
	}
}

func assertInstant(t *testing.T, field string, got, want *time.Time) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %s, want nil — the clock is paused", field, got.Format(time.RFC3339))
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %s — the clock is running", field, want.Format(time.RFC3339))
	case want != nil && got != nil && !got.Equal(*want):
		t.Errorf("%s = %s, want %s", field, got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
