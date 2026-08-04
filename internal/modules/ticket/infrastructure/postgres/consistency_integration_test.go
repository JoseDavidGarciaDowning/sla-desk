//go:build integration

package postgres_test

import (
	"math/rand/v2"
	"testing"
	"time"

	sladomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	slapostgres "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/ticketdb"
)

// The obligation docs/spec.md §4.2 takes on by keeping a derived cache:
//
//	the values stored in tickets.sla_* equal Reconstruct over the rows stored
//	in ticket_status_history for that ticket.
//
// Reframed per docs/adr/0001, this is not a test of the arithmetic. The unit
// tests in the SLA module's domain cover that, and asserting Reconstruct(history)
// == cache when the write path *produces* the cache by calling Reconstruct
// would be circular.
//
// What it targets is the write path. Every defect it catches is one where the
// arithmetic is right and something around it is wrong: the cache never
// updated, the fact and the cache committed separately, the history read before
// the new row was inserted so the clock is one event behind, or a partial write
// left behind. None of those are reachable from a unit test, because none of
// them are in the SLA module.
//
// Property-based over generated sequences rather than a handful of examples,
// because the interesting failures live in particular shapes — a pause
// immediately after a resume, a resolve straight from open — and enumerating
// them by hand is how the one that matters gets left out.
func TestCacheAlwaysMatchesTheHistoryItWasBuiltFrom(t *testing.T) {
	f := newRepoFixture(t)
	q := ticketdb.New(f.pool)

	const sequences = 15

	// Seeded so a failure can be replayed exactly. Printed rather than hidden,
	// so the seed is in the output of the run that failed.
	seed := uint64(time.Now().UnixNano())
	t.Logf("sequence seed: %d", seed)
	rng := rand.New(rand.NewPCG(seed, 0x5eed))

	for run := range sequences {
		tk, err := f.svc.Create(f.ctx, f.newTicket(randomPriority(rng)))
		if err != nil {
			t.Fatalf("run %d: Create: %v", run, err)
		}

		assertCacheMatchesHistory(t, f, q, tk, "after creation")

		for step := range 6 {
			target, ok := randomLegalTarget(rng, tk.Status)
			if !ok {
				break // closed is terminal
			}

			// A real interval, so the clock actually accrues between events and
			// a cache that silently drops accumulated time has something to
			// drop.
			time.Sleep(time.Millisecond)

			tk, err = f.svc.Transition(f.ctx, application.StatusChange{
				TicketID:  tk.ID,
				Target:    target,
				ActorID:   f.requester,
				ActorRole: domain.RoleAdmin,
			})
			if err != nil {
				t.Fatalf("run %d step %d: Transition to %s: %v", run, step, target, err)
			}

			assertCacheMatchesHistory(t, f, q, tk, "after transition to "+string(target))
		}
	}
}

// assertCacheMatchesHistory rebuilds the clock from what is stored and compares
// it against what is cached. It reads both back from the database rather than
// using the value Create or Transition returned, so a cache that was never
// written cannot pass by handing back the value it meant to write.
func assertCacheMatchesHistory(t *testing.T, f repoFixture, q *ticketdb.Queries, tk domain.Ticket, when string) {
	t.Helper()

	stored, err := f.repo.GetForRequester(f.ctx, tk.ID, f.requester)
	if err != nil {
		t.Fatalf("%s: reading the ticket back: %v", when, err)
	}

	rows, err := q.ListTicketStatusHistory(f.ctx, tk.ID)
	if err != nil {
		t.Fatalf("%s: reading the history: %v", when, err)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: the ticket has no history at all", when)
	}

	policy, err := slapostgres.NewPolicyRepository(f.pool).ByID(f.ctx, stored.SLAPolicyID)
	if err != nil {
		t.Fatalf("%s: reading the policy: %v", when, err)
	}

	timeline := make([]sladomain.Phase, len(rows))
	for i, row := range rows {
		timeline[i] = sladomain.Phase{At: row.CreatedAt, Running: row.ToStatus.RunsClock()}
	}

	state, err := sladomain.Reconstruct(policy, timeline)
	if err != nil {
		t.Fatalf("%s: Reconstruct over %d rows: %v", when, len(rows), err)
	}

	// The status the history ends on has to be the status the ticket is in. A
	// cache updated from a stale read would disagree here first.
	if last := rows[len(rows)-1].ToStatus; last != stored.Status {
		t.Errorf("%s: history ends in %q but the ticket is %q — the cache is not built from this history",
			when, last, stored.Status)
	}

	// Compared as Durations, which is what both sides now are. The column is
	// still microseconds — see docs/spec.md §4.2 for why minutes were not
	// enough — and the repository is what turns it into one.
	if state.BudgetUsed != stored.SLAConsumed {
		t.Errorf("%s: reconstructed %s consumed, cached %s (difference %s)",
			when, state.BudgetUsed, stored.SLAConsumed, state.BudgetUsed-stored.SLAConsumed)
	}
	if !sameInstant(state.RunningSince, stored.SLAClockStartedAt) {
		t.Errorf("%s: reconstructed clock start %v, cached %v", when, state.RunningSince, stored.SLAClockStartedAt)
	}
	if !sameInstant(state.DueAt, stored.SLADueAt) {
		t.Errorf("%s: reconstructed deadline %v, cached %v", when, state.DueAt, stored.SLADueAt)
	}

	// Falls out of the model rather than being a rule anyone enforces: a paused
	// ticket has no deadline, so the breach worker's predicate cannot match it.
	if !stored.Status.RunsClock() && stored.SLADueAt != nil {
		t.Errorf("%s: a %s ticket carries a deadline and can therefore breach", when, stored.Status)
	}
}

func sameInstant(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

func randomPriority(rng *rand.Rand) domain.Priority {
	all := []domain.Priority{
		domain.PriorityUrgent, domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow,
	}
	return all[rng.IntN(len(all))]
}

// randomLegalTarget picks a status the ticket can actually move to, using
// domain.Transition as the oracle for what is legal. Generating illegal moves
// would only exercise the rejection path, which the unit tests already cover.
func randomLegalTarget(rng *rand.Rand, from domain.Status) (domain.Status, bool) {
	candidates := []domain.Status{
		domain.StatusOpen, domain.StatusPending, domain.StatusResolved, domain.StatusClosed,
	}

	var legal []domain.Status
	for _, target := range candidates {
		if _, err := domain.Transition(from, target, domain.RoleAdmin); err == nil {
			legal = append(legal, target)
		}
	}
	if len(legal) == 0 {
		return "", false
	}
	return legal[rng.IntN(len(legal))], true
}
