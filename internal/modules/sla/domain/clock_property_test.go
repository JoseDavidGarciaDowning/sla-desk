package domain

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

const propertyIterations = 2000

// randomTimeline produces a timeline that satisfies Reconstruct's
// preconditions: it starts with the clock running and its timestamps never go
// backwards. Everything else — length, phases, gaps — is random.
//
// This used to generate ticket statuses and let StatusOpen stand in for
// "running". Generating the boolean directly is not merely a translation of
// that: it explores every running/paused sequence, rather than only the ones
// four particular statuses happen to produce. Which sequences a real ticket can
// reach is the state machine's property, and internal/ticket already asserts it.
func randomTimeline(rng *rand.Rand) []Phase {
	timeline := []Phase{{At: at(1, 9, 0), Running: true}}
	cursor := timeline[0].At

	for range rng.IntN(10) {
		// Zero gaps are allowed on purpose: equal consecutive timestamps are legal.
		cursor = cursor.Add(time.Duration(rng.IntN(600)) * time.Minute)
		timeline = append(timeline, Phase{
			At:      cursor,
			Running: rng.IntN(2) == 0,
		})
	}

	return timeline
}

func dump(timeline []Phase) string {
	var b strings.Builder
	for _, p := range timeline {
		b.WriteString("\n  ")
		b.WriteString(p.At.Format(time.RFC3339))
		if p.Running {
			b.WriteString(" -> running")
		} else {
			b.WriteString(" -> paused")
		}
	}
	return b.String()
}

// A running clock has both RunningSince and DueAt; a paused clock has neither.
// docs/spec.md §4.2 leans on this: a paused ticket has no deadline and therefore
// cannot breach. If the two ever disagreed, the breach query would either miss
// tickets or fire on paused ones.
func TestProperty_RunningAndPausedStatesAreConsistent(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	p := normalPolicy(4 * time.Hour)

	for i := range propertyIterations {
		timeline := randomTimeline(rng)

		got, err := Reconstruct(p, timeline)
		if err != nil {
			t.Fatalf("iteration %d: generated timeline was rejected: %v%s", i, err, dump(timeline))
		}

		if (got.RunningSince == nil) != (got.DueAt == nil) {
			t.Fatalf("iteration %d: RunningSince and DueAt disagree (%v / %v)%s",
				i, got.RunningSince, got.DueAt, dump(timeline))
		}

		// The clock runs exactly when the last phase left it running.
		wantRunning := timeline[len(timeline)-1].Running
		if gotRunning := got.RunningSince != nil; gotRunning != wantRunning {
			t.Fatalf("iteration %d: running = %v, want %v%s",
				i, gotRunning, wantRunning, dump(timeline))
		}
	}
}

// Budget used is never negative, and never exceeds the wall-clock span the
// timeline covers. A violation of the upper bound would mean time was counted
// twice; a violation of the lower bound would mean budget was handed back.
func TestProperty_BudgetUsedStaysWithinTheTimelineSpan(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	p := normalPolicy(4 * time.Hour)

	for i := range propertyIterations {
		timeline := randomTimeline(rng)

		got, err := Reconstruct(p, timeline)
		if err != nil {
			t.Fatalf("iteration %d: generated timeline was rejected: %v%s", i, err, dump(timeline))
		}

		span := timeline[len(timeline)-1].At.Sub(timeline[0].At)
		switch {
		case got.BudgetUsed < 0:
			t.Fatalf("iteration %d: BudgetUsed = %s, must never be negative%s",
				i, got.BudgetUsed, dump(timeline))
		case got.BudgetUsed > span:
			t.Fatalf("iteration %d: BudgetUsed = %s exceeds the %s the timeline spans%s",
				i, got.BudgetUsed, span, dump(timeline))
		}
	}
}

// Pausing must never bring a deadline forward.
//
// This is the property the whole model rests on: time spent waiting on the
// customer does not consume the team's budget. Any Schedule implementation —
// including the BusinessHours one added later — has to satisfy it, which is why
// it is asserted about behaviour rather than about the arithmetic.
func TestProperty_PausingNeverMovesTheDeadlineEarlier(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	p := normalPolicy(4 * time.Hour)

	for i := range propertyIterations {
		timeline := randomTimeline(rng)

		// Force the clock to be running, so there is a deadline to compare.
		last := timeline[len(timeline)-1]
		if !last.Running {
			timeline = append(timeline, Phase{
				At:      last.At.Add(time.Duration(rng.IntN(120)) * time.Minute),
				Running: true,
			})
		}

		before, err := Reconstruct(p, timeline)
		if err != nil {
			t.Fatalf("iteration %d: %v%s", i, err, dump(timeline))
		}

		// Insert a pause of a random length, then resume.
		pausedAt := timeline[len(timeline)-1].At.Add(time.Duration(rng.IntN(300)) * time.Minute)
		pauseFor := time.Duration(rng.IntN(5000)) * time.Minute
		paused := append(timeline,
			Phase{At: pausedAt, Running: false},
			Phase{At: pausedAt.Add(pauseFor), Running: true},
		)

		after, err := Reconstruct(p, paused)
		if err != nil {
			t.Fatalf("iteration %d: %v%s", i, err, dump(paused))
		}

		if after.DueAt.Before(*before.DueAt) {
			t.Fatalf("iteration %d: pausing for %s moved the deadline from %s back to %s%s",
				i, pauseFor,
				before.DueAt.Format(time.RFC3339), after.DueAt.Format(time.RFC3339),
				dump(paused))
		}
	}
}

// Reconstruct is a pure function of its inputs. Nothing about it may depend on
// wall-clock time, map ordering, or anything else that varies between calls —
// the derived cache would drift from the fact the moment it did.
func TestProperty_ReconstructIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	p := normalPolicy(4 * time.Hour)

	for i := range propertyIterations {
		timeline := randomTimeline(rng)

		first, err1 := Reconstruct(p, timeline)
		second, err2 := Reconstruct(p, timeline)

		if err1 != nil || err2 != nil {
			t.Fatalf("iteration %d: %v / %v%s", i, err1, err2, dump(timeline))
		}
		if first.BudgetUsed != second.BudgetUsed {
			t.Fatalf("iteration %d: BudgetUsed differed between calls: %s vs %s%s",
				i, first.BudgetUsed, second.BudgetUsed, dump(timeline))
		}
		assertInstant(t, "RunningSince", second.RunningSince, first.RunningSince)
		assertInstant(t, "DueAt", second.DueAt, first.DueAt)
	}
}
