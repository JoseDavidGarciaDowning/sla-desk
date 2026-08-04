package domain

import (
	"errors"
	"testing"
	"time"
)

// running and paused build a Phase, so the tables below read as a timeline
// rather than as a column of booleans.
//
// Note what is no longer expressible here: the difference between pending,
// resolved and closed. All three are `paused` to this package, which is the
// whole point of docs/adr/0005 — the ticket module decides which statuses burn
// budget, and that decision is asserted there, in TestOnlyOpenRunsTheClock.
func running(t time.Time) Phase { return Phase{At: t, Running: true} }
func paused(t time.Time) Phase  { return Phase{At: t, Running: false} }

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

func TestReconstruct_FreshTicketRunsFromCreation(t *testing.T) {
	created := at(1, 9, 0)
	p := normalPolicy(4 * time.Hour)

	got, err := p.Reconstruct([]Phase{running(created)})
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

// TestReconstruct_FoldsTimelineIntoClockState exercises the core rule from
// docs/spec.md §4.2: the clock runs only while the ticket is open, and pauses in
// pending, resolved and closed.
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
			// This case also stands in for the one that used to read "resolved
			// stops the clock like pending does". Both produced the same two
			// phases, and keeping two entries would have asserted the same
			// arithmetic twice while looking like extra coverage.
			name:      "a pause banks the time spent open",
			timeline:  []Phase{running(at(1, 9, 0)), paused(at(1, 10, 30))},
			wantUsed:  90 * time.Minute,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "waiting on the customer costs the team nothing, however long it lasts",
			timeline: []Phase{
				running(at(1, 9, 0)),
				paused(at(1, 10, 30)),
				running(at(2, 14, 0)),
			},
			// 1h30 spent before the pause; 2h30 of budget survives the 27h wait.
			wantUsed:  90 * time.Minute,
			wantSince: new(at(2, 14, 0)),
			wantDue:   new(at(2, 16, 30)),
		},
		{
			name: "repeated pauses accumulate only the running stretches",
			timeline: []Phase{
				running(at(1, 9, 0)),
				paused(at(1, 10, 30)), // +1h30
				running(at(2, 14, 0)),
				paused(at(2, 15, 0)), // +1h
			},
			wantUsed:  150 * time.Minute,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			// Two consecutive paused phases — a resolved ticket being closed.
			// The second must not re-close an interval that is already shut,
			// which would double-count the stretch before it.
			name: "a second pause while already paused adds nothing",
			timeline: []Phase{
				running(at(1, 9, 0)),
				paused(at(1, 11, 0)),
				paused(at(3, 9, 0)),
			},
			wantUsed:  2 * time.Hour,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "resuming picks up the remaining budget, not a fresh one",
			timeline: []Phase{
				running(at(1, 9, 0)),
				paused(at(1, 10, 0)),
				running(at(1, 11, 0)),
			},
			wantUsed:  1 * time.Hour,
			wantSince: new(at(1, 11, 0)),
			wantDue:   new(at(1, 14, 0)), // 3h left, not 4h
		},
		{
			name:      "budget exhausted to the minute is still just paused",
			timeline:  []Phase{running(at(1, 9, 0)), paused(at(1, 13, 0))},
			wantUsed:  budget,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "an overspent ticket that resumes is due in the past, so the breach query catches it",
			timeline: []Phase{
				running(at(1, 9, 0)),
				paused(at(1, 14, 0)), // 5h against a 4h budget
				running(at(2, 9, 0)),
			},
			wantUsed:  5 * time.Hour,
			wantSince: new(at(2, 9, 0)),
			wantDue:   new(at(2, 8, 0)), // one hour overdue the moment it resumes
		},
		{
			name:      "a ticket paused the instant it was created has spent nothing",
			timeline:  []Phase{running(at(1, 9, 0)), paused(at(1, 9, 0))},
			wantUsed:  0,
			wantSince: nil,
			wantDue:   nil,
		},
		{
			name: "a zero-length pause changes nothing",
			timeline: []Phase{
				running(at(1, 9, 0)),
				paused(at(1, 10, 0)),
				running(at(1, 10, 0)),
			},
			wantUsed:  1 * time.Hour,
			wantSince: new(at(1, 10, 0)),
			wantDue:   new(at(1, 13, 0)),
		},
	}

	p := normalPolicy(budget)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Reconstruct(tt.timeline)
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

// TestReconstruct_RejectsMalformedTimeline covers the preconditions this module
// owns. Transition legality is deliberately NOT checked here — that rule lives
// in the ticket module and is enforced at write time. See docs/adr/0002.
//
// A wrong ClockState is worse than a loud failure: a bad deadline looks entirely
// plausible. So each case also asserts the returned state is zero, leaving a
// caller who ignores the error with nothing usable.
func TestReconstruct_RejectsMalformedTimeline(t *testing.T) {
	tests := []struct {
		name     string
		timeline []Phase
		wantErr  error
	}{
		{
			name:     "empty timeline has no starting instant to compute from",
			timeline: nil,
			wantErr:  ErrEmptyTimeline,
		},
		{
			name: "a timeline that runs backwards would corrupt the interval arithmetic",
			timeline: []Phase{
				running(at(1, 11, 0)),
				paused(at(1, 9, 0)),
			},
			wantErr: ErrUnorderedTimeline,
		},
		{
			name:     "a timeline that does not begin running did not come from a ticket",
			timeline: []Phase{paused(at(1, 9, 0))},
			wantErr:  ErrTimelineMustStartRunning,
		},
	}

	p := normalPolicy(4 * time.Hour)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Reconstruct(tt.timeline)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want one wrapping %v", err, tt.wantErr)
			}
			if got != (ClockState{}) {
				t.Errorf("state = %+v, want the zero value — a rejected timeline must yield nothing usable", got)
			}
		})
	}
}

// Equal timestamps are legal: a status can change twice at the same instant, and
// the "zero-length pause" case above depends on it. Only a strict decrease is an
// error.
func TestReconstruct_AcceptsEqualTimestamps(t *testing.T) {
	p := normalPolicy(4 * time.Hour)

	_, err := p.Reconstruct([]Phase{
		running(at(1, 9, 0)),
		paused(at(1, 10, 0)),
		running(at(1, 10, 0)),
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
