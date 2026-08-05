package domain

import (
	"errors"
	"testing"
	"time"
)

// normalPolicy builds a policy with an explicit budget. Budgets are data
// (sla_policies), never constants in the code, so tests state theirs outright.
func normalPolicy(budget time.Duration) Policy {
	return Policy{
		ID:       1,
		Priority: PriorityNormal,
		Budget:   budget,
		Schedule: Always24x7{},
	}
}

func TestReconstruct_ASingleRunningPhaseSpendsNothingYet(t *testing.T) {
	created := at(1, 9, 0)
	p := normalPolicy(4 * time.Hour)

	got, err := Reconstruct(p, []Phase{
		{At: created, Running: true},
	})
	if err != nil {
		t.Fatalf("Reconstruct returned an unexpected error: %v", err)
	}

	if got.BudgetUsed != 0 {
		t.Errorf("BudgetUsed = %s, want 0 — nothing has been spent yet", got.BudgetUsed)
	}
	if got.RunningSince == nil {
		t.Fatal("RunningSince is nil, want the starting instant — a running clock has one")
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

// TestReconstruct_FoldsTimelineIntoClockState exercises the core rule from
// docs/spec.md §4.2: budget is consumed only in running phases.
//
// Which ticket statuses those are is not this package's question, and
// TestOnlyOpenRunsTheClock in internal/ticket is where it is answered. The two
// cases below that used to differ only by which paused status produced them now
// differ only by their arithmetic, which is all this package was ever testing.
//
// Expected values are worked examples computed by hand from the timeline, not
// recomputed the way the implementation does.
func TestReconstruct_FoldsTimelineIntoClockState(t *testing.T) {
	const budget = 4 * time.Hour

	tests := []struct {
		name     string
		timeline []Phase

		wantUsed  time.Duration
		wantSince *time.Time // nil means the clock is paused
		wantDue   *time.Time // nil means the clock is paused
	}{
		{
			name: "pausing banks the time spent running",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 10, 30), Running: false},
			},
			wantUsed:  90 * time.Minute,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "waiting on the customer costs the team nothing, however long it lasts",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 10, 30), Running: false},
				{At: at(2, 14, 0), Running: true},
			},
			// 1h30 spent before the pause; 2h30 of budget survives the 27h wait.
			wantUsed:  90 * time.Minute,
			wantSince: new(at(2, 14, 0)),
			wantDue:   new(at(2, 16, 30)),
		},
		{
			name: "repeated pauses accumulate only the running stretches",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 10, 30), Running: false}, // +1h30
				{At: at(2, 14, 0), Running: true},
				{At: at(2, 15, 0), Running: false}, // +1h
			},
			wantUsed:  150 * time.Minute,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "a pause two hours in banks exactly those two hours",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 11, 0), Running: false},
			},
			wantUsed:  2 * time.Hour,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "a second pause while already paused adds nothing",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 11, 0), Running: false},
				{At: at(3, 9, 0), Running: false},
			},
			wantUsed:  2 * time.Hour,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "resuming continues the remaining budget, not a fresh one",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 10, 0), Running: false},
				{At: at(1, 11, 0), Running: true},
			},
			wantUsed:  1 * time.Hour,
			wantSince: new(at(1, 11, 0)),
			wantDue:   new(at(1, 14, 0)), // 3h left, not 4h
		},
		{
			name: "budget exhausted to the minute is still just paused",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 13, 0), Running: false},
			},
			wantUsed:  budget,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "an overspent clock that resumes is due in the past, so the breach query catches it",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 14, 0), Running: false}, // 5h against a 4h budget
				{At: at(2, 9, 0), Running: true},
			},
			wantUsed:  5 * time.Hour,
			wantSince: new(at(2, 9, 0)),
			wantDue:   new(at(2, 8, 0)), // one hour overdue the moment it resumes
		},
		{
			name: "a clock paused the instant it started has spent nothing",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 9, 0), Running: false},
			},
			wantUsed:  0,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "a zero-length pause changes nothing",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: true},
				{At: at(1, 10, 0), Running: false},
				{At: at(1, 10, 0), Running: true},
			},
			wantUsed:  1 * time.Hour,
			wantSince: new(at(1, 10, 0)),
			wantDue:   new(at(1, 13, 0)),
		},
	}

	p := normalPolicy(budget)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reconstruct(p, tt.timeline)
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

// TestReconstruct_RejectsMalformedTimelines covers the preconditions internal/sla
// owns. Transition legality is deliberately NOT checked here — that rule lives in
// internal/ticket and is enforced at write time. See docs/adr/0002, and
// docs/adr/0005 for why the third case below is now stated about timelines
// rather than about tickets.
//
// A wrong ClockState is worse than a loud failure: a bad deadline looks entirely
// plausible. So each case also asserts the returned state is zero, leaving a
// caller who ignores the error with nothing usable.
func TestReconstruct_RejectsMalformedTimelines(t *testing.T) {
	tests := []struct {
		name     string
		timeline []Phase
		wantErr  error
	}{
		{
			name:     "an empty timeline has no starting instant to compute from",
			timeline: nil,
			wantErr:  ErrEmptyTimeline,
		},
		{
			name: "a timeline that runs backwards would corrupt the interval arithmetic",
			timeline: []Phase{
				{At: at(1, 11, 0), Running: true},
				{At: at(1, 9, 0), Running: false},
			},
			wantErr: ErrUnorderedTimeline,
		},
		{
			// The rule this replaced read "must start in open", which is a statement
			// about tickets. "Starts with the clock running" is the same
			// guarantee stated in this package's own terms — and they agree
			// because StatusOpen.RunsClock() is true.
			name: "a timeline that starts paused has no interval to measure from",
			timeline: []Phase{
				{At: at(1, 9, 0), Running: false},
			},
			wantErr: ErrTimelineMustStartRunning,
		},
	}

	p := normalPolicy(4 * time.Hour)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reconstruct(p, tt.timeline)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want one wrapping %v", err, tt.wantErr)
			}
			if got != (ClockState{}) {
				t.Errorf("state = %+v, want the zero value — a rejected timeline must yield nothing usable", got)
			}
		})
	}
}

// Equal timestamps are legal: a phase can change twice at the same instant, and
// the "zero-length pause" case above depends on it. Only a strict decrease is an
// error.
func TestReconstruct_AcceptsEqualTimestamps(t *testing.T) {
	p := normalPolicy(4 * time.Hour)

	_, err := Reconstruct(p, []Phase{
		{At: at(1, 9, 0), Running: true},
		{At: at(1, 10, 0), Running: false},
		{At: at(1, 10, 0), Running: true},
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
