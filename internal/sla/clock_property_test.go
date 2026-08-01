package sla

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

const propertyIterations = 2000

// randomHistory produces a history that satisfies Reconstruct's preconditions:
// it starts in open and its timestamps never go backwards. Everything else —
// length, statuses, gaps — is random.
func randomHistory(rng *rand.Rand) []StatusChange {
	statuses := []ticket.Status{
		ticket.StatusOpen,
		ticket.StatusPending,
		ticket.StatusResolved,
		ticket.StatusClosed,
	}

	history := []StatusChange{{To: ticket.StatusOpen, At: at(1, 9, 0)}}
	cursor := history[0].At

	for range rng.IntN(10) {
		// Zero gaps are allowed on purpose: equal consecutive timestamps are legal.
		cursor = cursor.Add(time.Duration(rng.IntN(600)) * time.Minute)
		history = append(history, StatusChange{
			To: statuses[rng.IntN(len(statuses))],
			At: cursor,
		})
	}

	return history
}

func dump(history []StatusChange) string {
	var b strings.Builder
	for _, c := range history {
		b.WriteString("\n  ")
		b.WriteString(c.At.Format(time.RFC3339))
		b.WriteString(" -> ")
		b.WriteString(string(c.To))
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
		history := randomHistory(rng)

		got, err := Reconstruct(p, history)
		if err != nil {
			t.Fatalf("iteration %d: generated history was rejected: %v%s", i, err, dump(history))
		}

		if (got.RunningSince == nil) != (got.DueAt == nil) {
			t.Fatalf("iteration %d: RunningSince and DueAt disagree (%v / %v)%s",
				i, got.RunningSince, got.DueAt, dump(history))
		}

		// The clock runs exactly when the last entry left the ticket open.
		wantRunning := history[len(history)-1].To == ticket.StatusOpen
		if gotRunning := got.RunningSince != nil; gotRunning != wantRunning {
			t.Fatalf("iteration %d: running = %v, want %v (last status %q)%s",
				i, gotRunning, wantRunning, history[len(history)-1].To, dump(history))
		}
	}
}

// Budget used is never negative, and never exceeds the wall-clock span the
// history covers. A violation of the upper bound would mean time was counted
// twice; a violation of the lower bound would mean budget was handed back.
func TestProperty_BudgetUsedStaysWithinTheHistorySpan(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	p := normalPolicy(4 * time.Hour)

	for i := range propertyIterations {
		history := randomHistory(rng)

		got, err := Reconstruct(p, history)
		if err != nil {
			t.Fatalf("iteration %d: generated history was rejected: %v%s", i, err, dump(history))
		}

		span := history[len(history)-1].At.Sub(history[0].At)
		switch {
		case got.BudgetUsed < 0:
			t.Fatalf("iteration %d: BudgetUsed = %s, must never be negative%s",
				i, got.BudgetUsed, dump(history))
		case got.BudgetUsed > span:
			t.Fatalf("iteration %d: BudgetUsed = %s exceeds the %s the history spans%s",
				i, got.BudgetUsed, span, dump(history))
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
		history := randomHistory(rng)

		// Force the clock to be running, so there is a deadline to compare.
		last := history[len(history)-1]
		if last.To != ticket.StatusOpen {
			history = append(history, StatusChange{
				To: ticket.StatusOpen,
				At: last.At.Add(time.Duration(rng.IntN(120)) * time.Minute),
			})
		}

		before, err := Reconstruct(p, history)
		if err != nil {
			t.Fatalf("iteration %d: %v%s", i, err, dump(history))
		}

		// Insert a pause of a random length, then resume.
		pausedAt := history[len(history)-1].At.Add(time.Duration(rng.IntN(300)) * time.Minute)
		pauseFor := time.Duration(rng.IntN(5000)) * time.Minute
		paused := append(history,
			StatusChange{To: ticket.StatusPending, At: pausedAt},
			StatusChange{To: ticket.StatusOpen, At: pausedAt.Add(pauseFor)},
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
		history := randomHistory(rng)

		first, err1 := Reconstruct(p, history)
		second, err2 := Reconstruct(p, history)

		if err1 != nil || err2 != nil {
			t.Fatalf("iteration %d: %v / %v%s", i, err1, err2, dump(history))
		}
		if first.BudgetUsed != second.BudgetUsed {
			t.Fatalf("iteration %d: BudgetUsed differed between calls: %s vs %s%s",
				i, first.BudgetUsed, second.BudgetUsed, dump(history))
		}
		assertInstant(t, "RunningSince", second.RunningSince, first.RunningSince)
		assertInstant(t, "DueAt", second.DueAt, first.DueAt)
	}
}
