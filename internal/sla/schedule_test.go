package sla

import (
	"testing"
	"time"
)

// at is a helper for readable, unambiguous instants in tests.
func at(day, hour, min int) time.Time {
	return time.Date(2026, time.August, day, hour, min, 0, 0, time.UTC)
}

func TestAlways24x7_Elapsed(t *testing.T) {
	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want time.Duration
	}{
		{
			name: "counts every minute between the two instants",
			from: at(1, 9, 0),
			to:   at(1, 11, 30),
			want: 2*time.Hour + 30*time.Minute,
		},
		{
			name: "counts overnight, because the clock never stops",
			from: at(1, 23, 0),
			to:   at(2, 1, 0),
			want: 2 * time.Hour,
		},
		{
			name: "counts weekends, because the clock never stops",
			// 2026-08-01 is a Saturday; 2026-08-03 is a Monday.
			from: at(1, 12, 0),
			to:   at(3, 12, 0),
			want: 48 * time.Hour,
		},
		{
			name: "identical instants consume nothing",
			from: at(1, 9, 0),
			to:   at(1, 9, 0),
			want: 0,
		},
		{
			name: "reversed range consumes nothing, never a negative",
			from: at(1, 11, 0),
			to:   at(1, 9, 0),
			want: 0,
		},
	}

	var s Always24x7

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Elapsed(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("Elapsed(%s, %s) = %s, want %s",
					tt.from.Format(time.RFC3339), tt.to.Format(time.RFC3339), got, tt.want)
			}
		})
	}
}

func TestAlways24x7_DueAt(t *testing.T) {
	tests := []struct {
		name      string
		from      time.Time
		remaining time.Duration
		want      time.Time
	}{
		{
			name:      "four hours of budget lands four hours later",
			from:      at(1, 9, 0),
			remaining: 4 * time.Hour,
			want:      at(1, 13, 0),
		},
		{
			name:      "runs through the night without pausing",
			from:      at(1, 23, 0),
			remaining: 3 * time.Hour,
			want:      at(2, 2, 0),
		},
		{
			name:      "the 72h low-priority budget spans three full days",
			from:      at(1, 9, 0),
			remaining: 72 * time.Hour,
			want:      at(4, 9, 0),
		},
		{
			name:      "no budget left is due immediately",
			from:      at(1, 9, 0),
			remaining: 0,
			want:      at(1, 9, 0),
		},
		{
			name:      "budget already overspent is due in the past, so the breach query catches it at once",
			from:      at(1, 9, 0),
			remaining: -30 * time.Minute,
			want:      at(1, 8, 30),
		},
	}

	var s Always24x7

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.DueAt(tt.from, tt.remaining)
			if !got.Equal(tt.want) {
				t.Errorf("DueAt(%s, %s) = %s, want %s",
					tt.from.Format(time.RFC3339), tt.remaining,
					got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

// Elapsed and DueAt must agree: consuming a span and then asking when the
// remaining budget runs out has to land on the same instant as spending the whole
// budget from the start. This is the invariant BusinessHours will also have to
// satisfy, which is why it is asserted against the interface contract rather than
// the arithmetic.
func TestAlways24x7_ElapsedAndDueAtAgree(t *testing.T) {
	var s Always24x7

	const budget = 4 * time.Hour
	start := at(1, 9, 0)
	pausedAt := at(1, 10, 30)
	resumedAt := at(2, 14, 0)

	used := s.Elapsed(start, pausedAt)
	got := s.DueAt(resumedAt, budget-used)

	// 1h30 of a 4h budget was spent before the pause, leaving 2h30 from 14:00.
	want := at(2, 16, 30)
	if !got.Equal(want) {
		t.Errorf("after spending %s of %s and resuming at %s, due = %s, want %s",
			used, budget, resumedAt.Format(time.RFC3339),
			got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
