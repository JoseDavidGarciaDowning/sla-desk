//go:build integration

// These tests need a migrated Postgres. Run them with `make test-int`.
//
// They live in the composition root rather than in the ticket module, and that
// is the point: every one of them asserts something about a deadline, and a
// deadline is the ticket module's write path driven by the SLA module's
// arithmetic. Neither module may import the other, so the only place their
// combination exists is here, where the adapter connects them. Testing it
// anywhere else would mean standing in for one of the two, and a stand-in for
// the SLA calculation is a second copy of the thing under test.
package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/app"
	sladomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	slapg "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/ticketdb"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/config"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/pgtest"
)

// The repo owns its own transaction, so these tests cannot use the rolled-back
// one the rest of the file shares. They clean up after themselves instead.
type repoFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	repo      *ticketapp.Service
	requester uuid.UUID
}

func newRepoFixture(t *testing.T) repoFixture {
	t.Helper()

	ctx, pool := pgtest.Pool(t)

	clerkID := "user_repo_" + t.Name()
	var requester uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (clerk_user_id, email) VALUES ($1, $2) RETURNING id`,
		clerkID, clerkID+"@example.test",
	).Scan(&requester); err != nil {
		t.Fatalf("creating the requester: %v", err)
	}

	// Registered after pool.Close so it runs before it: t.Cleanup is LIFO.
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg,
			`DELETE FROM ticket_status_history WHERE ticket_id IN (SELECT id FROM tickets WHERE requester_id = $1)`,
			requester); err != nil {
			t.Errorf("cleanup history: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM tickets WHERE requester_id = $1`, requester); err != nil {
			t.Errorf("cleanup tickets: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM users WHERE id = $1`, requester); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	// The assembled application, not a hand-built repository. What these tests
	// exercise is the wiring as production runs it: the ticket service, the
	// adapter, and the real SLA calculator behind it.
	return repoFixture{
		ctx:       ctx,
		pool:      pool,
		repo:      app.New(config.Config{}, pool).Ticket.Service,
		requester: requester,
	}
}

func (f repoFixture) newTicket(priority domain.Priority) ticketapp.NewTicket {
	return ticketapp.NewTicket{
		RequesterID: f.requester,
		ActorRole:   domain.RoleCustomer,
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    domain.CategoryBilling,
		Priority:    priority,
	}
}

func TestCreateStartsTheClockRunning(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityNormal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if tk.Status != domain.StatusOpen {
		t.Errorf("status = %q, want open", tk.Status)
	}
	if tk.SLAClockStartedAt == nil {
		t.Fatal("sla_clock_started_at is nil — a new ticket is open, so the clock runs")
	}
	if tk.SLADueAt == nil {
		t.Fatal("sla_due_at is nil — a ticket that cannot breach is not on any clock")
	}
	if tk.SLAConsumed != 0 {
		t.Errorf("consumed = %s, want 0 on a ticket that has never paused", tk.SLAConsumed)
	}
	if tk.SLABreachedAt != nil {
		t.Error("sla_breached_at is set on a brand new ticket")
	}
	if tk.SLAPolicyID == 0 {
		t.Error("sla_policy_id was not snapshotted")
	}
}

// Every priority resolves to the budget seeded in migration 002, and the
// deadline is that budget away from the moment the clock started. Written as
// literals rather than read back from sla_policies, so the test disagrees with
// the seed if the seed is wrong.
func TestDeadlineIsTheSeededBudgetForEveryPriority(t *testing.T) {
	f := newRepoFixture(t)

	budgets := map[domain.Priority]time.Duration{
		domain.PriorityUrgent: 60 * time.Minute,
		domain.PriorityHigh:   240 * time.Minute,
		domain.PriorityNormal: 1440 * time.Minute,
		domain.PriorityLow:    4320 * time.Minute,
	}

	for priority, budget := range budgets {
		t.Run(string(priority), func(t *testing.T) {
			tk, err := f.repo.Create(f.ctx, f.newTicket(priority))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			// Exact, not approximate. Both instants come from the same
			// transaction timestamp, so there is nothing to be within a
			// tolerance of.
			want := tk.SLAClockStartedAt.Add(budget)
			if !tk.SLADueAt.Equal(want) {
				t.Errorf("due at %v, want %v (started %v + %v)",
					tk.SLADueAt, want, tk.SLAClockStartedAt, budget)
			}
		})
	}
}

// The property docs/spec.md §9 makes mandatory, at the moment of creation:
// rebuilding the clock from ticket_status_history alone must reproduce the
// cached columns exactly.
//
// This is what the explicit created_at on the history row buys. Left to the
// column default, the row would carry the instant Postgres stamped it and the
// cache the instant the deadline was computed from, and the two would differ by
// however long the insert took.
func TestCachedClockMatchesTheReconstructionFromHistory(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityUrgent))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	q := ticketdb.New(f.pool)

	rows, err := q.ListTicketStatusHistory(f.ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListTicketStatusHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("history rows = %d, want 1", len(rows))
	}
	if rows[0].FromStatus != nil {
		t.Errorf("from_status = %q, want nil on the creation row", *rows[0].FromStatus)
	}
	if rows[0].ToStatus != domain.StatusOpen {
		t.Errorf("to_status = %q, want open", rows[0].ToStatus)
	}

	state, err := reconstruct(t, f, sladomain.Priority(tk.Priority), rows)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if got := state.BudgetUsed.Microseconds(); got != tk.SLAConsumed.Microseconds() {
		t.Errorf("reconstructed consumed = %d micros, cached = %d", got, tk.SLAConsumed.Microseconds())
	}
	if state.RunningSince == nil || !state.RunningSince.Equal(*tk.SLAClockStartedAt) {
		t.Errorf("reconstructed clock start = %v, cached = %v", state.RunningSince, tk.SLAClockStartedAt)
	}
	if state.DueAt == nil || !state.DueAt.Equal(*tk.SLADueAt) {
		t.Errorf("reconstructed due = %v, cached = %v", state.DueAt, tk.SLADueAt)
	}
}

// docs/spec.md §4.1: no history row, no transition. The ticket and its first
// history row are one write or they are neither.
func TestAFailedHistoryWriteLeavesNoTicket(t *testing.T) {
	f := newRepoFixture(t)

	in := f.newTicket(domain.PriorityNormal)
	in.ActorRole = "superadmin" // rejected by ticket_status_history_actor_role_valid

	if _, err := f.repo.Create(f.ctx, in); err == nil {
		t.Fatal("Create succeeded with an invalid actor role")
	}

	var tickets, history int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT (SELECT count(*) FROM tickets WHERE requester_id = $1),
		        (SELECT count(*) FROM ticket_status_history h JOIN tickets t ON t.id = h.ticket_id
		         WHERE t.requester_id = $1)`,
		f.requester).Scan(&tickets, &history); err != nil {
		t.Fatalf("counting: %v", err)
	}

	if tickets != 0 || history != 0 {
		t.Errorf("left %d tickets and %d history rows behind; the transaction did not roll back",
			tickets, history)
	}
}

func TestCreateReportsAnUnservedPriority(t *testing.T) {
	f := newRepoFixture(t)

	in := f.newTicket("critical") // no seeded policy, and not a legal value

	_, err := f.repo.Create(f.ctx, in)
	if !errors.Is(err, ticketapp.ErrNoSLAPolicy) {
		t.Errorf("err = %v, want ErrNoPolicyForPriority", err)
	}
}

// The stability keyset pagination exists for. Reading page one, then inserting
// a ticket, then reading page two with the cursor must not repeat or skip a row
// — which is exactly what OFFSET would do, because the new ticket sorts first
// and pushes everything down by one.
func TestPaginationIsStableAcrossAnInsert(t *testing.T) {
	f := newRepoFixture(t)

	const total = 5
	for range total {
		if _, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityNormal)); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	page := func(after *domain.Ticket, size int32) []domain.Ticket {
		t.Helper()
		params := ticketapp.ListFilter{RequesterID: f.requester, PageSize: size}
		if after != nil {
			params.AfterCreatedAt = &after.CreatedAt
			params.AfterID = &after.ID
		}
		rows, err := f.repo.List(f.ctx, params)
		if err != nil {
			t.Fatalf("ListTicketsByRequester: %v", err)
		}
		return rows
	}

	first := page(nil, 2)
	if len(first) != 2 {
		t.Fatalf("first page returned %d rows, want 2", len(first))
	}

	// A ticket arrives between the two reads. With OFFSET 2 the second page
	// would start one row too late and the caller would never see one of the
	// original tickets.
	if _, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityUrgent)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	second := page(&first[len(first)-1], 10)

	seen := map[string]bool{}
	for _, row := range append(append([]domain.Ticket{}, first...), second...) {
		id := row.ID.String()
		if seen[id] {
			t.Errorf("ticket %v appeared on both pages", row.ID)
		}
		seen[id] = true
	}

	// The five that existed when paging started must all be accounted for. The
	// one inserted in between sorts ahead of the cursor and is legitimately not
	// in either page.
	if len(seen) < total {
		t.Errorf("saw %d distinct tickets across both pages, want at least the %d that existed", len(seen), total)
	}
}

// Two tickets created in the same transaction share created_at exactly, which
// is the tie the cursor's id component exists to break. Without it a page
// boundary landing on the tie would drop a row or repeat one.
func TestCursorSeparatesTicketsSharingATimestamp(t *testing.T) {
	f := newRepoFixture(t)

	// Each Create is its own transaction, so force the tie instead.
	for range 3 {
		if _, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityNormal)); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE tickets SET created_at = now() WHERE requester_id = $1`, f.requester); err != nil {
		t.Fatalf("flattening the timestamps: %v", err)
	}

	var collected []domain.Ticket
	var after *domain.Ticket
	for range 5 { // bounded, so a cursor that fails to advance cannot spin
		params := ticketapp.ListFilter{RequesterID: f.requester, PageSize: 1}
		if after != nil {
			params.AfterCreatedAt = &after.CreatedAt
			params.AfterID = &after.ID
		}
		rows, err := f.repo.List(f.ctx, params)
		if err != nil {
			t.Fatalf("ListTicketsByRequester: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		collected = append(collected, rows[0])
		after = &rows[0]
	}

	if len(collected) != 3 {
		t.Fatalf("walked %d tickets one page at a time, want 3 — the cursor cannot separate equal timestamps", len(collected))
	}
	seen := map[string]bool{}
	for _, row := range collected {
		id := row.ID.String()
		if seen[id] {
			t.Errorf("ticket %v came back twice", row.ID)
		}
		seen[id] = true
	}
}

// The fact and the cache are one write or neither. docs/spec.md §4.1: no
// history row, no transition.
//
// The cache update is forced to fail by pointing the ticket at a policy whose
// schedule internal/sla cannot interpret, which fails after the history row has
// already been inserted. If the two were in separate transactions the history
// row would survive and the ticket would carry a status its history never
// records.
func TestAFailedCacheUpdateLeavesNoHistoryRow(t *testing.T) {
	f := newRepoFixture(t)
	q := ticketdb.New(f.pool)

	tk, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityNormal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := q.ListTicketStatusHistory(f.ctx, tk.ID)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}

	// A policy the code cannot interpret. The CHECK constraint normally makes
	// this impossible, which is why it has to be introduced deliberately.
	if _, err := f.pool.Exec(f.ctx,
		`ALTER TABLE sla_policies DROP CONSTRAINT sla_policies_schedule_mode_valid`); err != nil {
		t.Fatalf("relaxing the constraint: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(),
			`UPDATE sla_policies SET schedule_mode = '24x7' WHERE schedule_mode <> '24x7';
			 ALTER TABLE sla_policies ADD CONSTRAINT sla_policies_schedule_mode_valid
			   CHECK (schedule_mode IN ('24x7'))`); err != nil {
			t.Errorf("restoring the constraint: %v", err)
		}
	})
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE sla_policies SET schedule_mode = 'business_hours' WHERE id = $1`, tk.SLAPolicyID); err != nil {
		t.Fatalf("breaking the policy: %v", err)
	}

	if _, err := f.repo.Transition(f.ctx, ticketapp.StatusChange{
		TicketID:  tk.ID,
		Target:    domain.StatusPending,
		ActorID:   f.requester,
		ActorRole: domain.RoleAdmin,
	}); !errors.Is(err, slapg.ErrUnsupportedSchedule) {
		t.Fatalf("err = %v, want ErrUnsupportedSchedule", err)
	}

	after, err := q.ListTicketStatusHistory(f.ctx, tk.ID)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("history grew from %d to %d rows on a transition that failed", len(before), len(after))
	}

	stored, err := f.repo.Get(f.ctx, tk.ID, f.requester)
	if err != nil {
		t.Fatalf("reading the ticket: %v", err)
	}
	if stored.Status != domain.StatusOpen {
		t.Errorf("status = %q, want it unchanged at open", stored.Status)
	}
}

// A closed ticket is terminal, and the database is not the thing enforcing it —
// the domain is. This is the end-to-end proof that the pure state machine is
// actually consulted by the write path.
func TestTransitionRefusesToLeaveClosed(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityNormal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, target := range []domain.Status{domain.StatusResolved, domain.StatusClosed} {
		tk, err = f.repo.Transition(f.ctx, ticketapp.StatusChange{
			TicketID: tk.ID, Target: target, ActorID: f.requester, ActorRole: domain.RoleAdmin,
		})
		if err != nil {
			t.Fatalf("moving to %s: %v", target, err)
		}
	}

	_, err = f.repo.Transition(f.ctx, ticketapp.StatusChange{
		TicketID: tk.ID, Target: domain.StatusOpen, ActorID: f.requester, ActorRole: domain.RoleAdmin,
	})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("err = %v, want ErrInvalidTransition — closed is terminal", err)
	}
}

// Pausing stops the clock and clears the deadline; resuming starts it again
// from the budget already spent. This is the behaviour the whole SLA model
// exists for, and until now nothing exercised it.
func TestPausingStopsTheClockAndResumingKeepsWhatWasSpent(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.repo.Create(f.ctx, f.newTicket(domain.PriorityUrgent))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	paused, err := f.repo.Transition(f.ctx, ticketapp.StatusChange{
		TicketID: tk.ID, Target: domain.StatusPending, ActorID: f.requester, ActorRole: domain.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("pausing: %v", err)
	}

	if paused.SLADueAt != nil {
		t.Error("a pending ticket carries a deadline and can therefore breach")
	}
	if paused.SLAClockStartedAt != nil {
		t.Error("a pending ticket still has a running clock")
	}
	spent := paused.SLAConsumed
	if spent <= 0 {
		t.Fatalf("consumed = %s after time passed in open", spent)
	}

	// Time spent waiting on the customer must not consume budget.
	time.Sleep(20 * time.Millisecond)

	resumed, err := f.repo.Transition(f.ctx, ticketapp.StatusChange{
		TicketID: tk.ID, Target: domain.StatusOpen, ActorID: f.requester, ActorRole: domain.RoleCustomer,
	})
	if err != nil {
		t.Fatalf("resuming: %v", err)
	}

	if resumed.SLAConsumed != spent {
		t.Errorf("consumed = %s after the pause, was %s before — the pause was billed",
			resumed.SLAConsumed, spent)
	}
	if resumed.SLADueAt == nil {
		t.Fatal("no deadline after resuming")
	}

	// The deadline is what is left of the budget, counted from now — not the
	// original deadline shifted, and not the full budget again.
	remaining := 60*time.Minute - spent
	want := resumed.SLAClockStartedAt.Add(remaining)
	if !resumed.SLADueAt.Equal(want) {
		t.Errorf("deadline %v, want %v (%v of budget left)", resumed.SLADueAt, want, remaining)
	}
}

// reconstruct rebuilds an SLA clock from history, through the SLA module's own
// repository and arithmetic.
//
// Deliberately not a second implementation. The whole point of the consistency
// property is that the cached columns agree with what the real calculation
// produces from the real history; a hand-rolled reconstruction here would only
// prove that two copies of the arithmetic agree.
//
// It is also the one thing these tests do that no module could do for itself:
// reading a policy from the SLA module and a history from the ticket module in
// the same function is exactly the cross-module knowledge that lives only here.
func reconstruct(t *testing.T, f repoFixture, priority sladomain.Priority,
	rows []ticketdb.TicketStatusHistory,
) (sladomain.ClockState, error) {
	t.Helper()

	policy, err := slapg.NewPolicyRepository(f.pool).ActiveByPriority(f.ctx, priority)
	if err != nil {
		t.Fatalf("resolving the policy: %v", err)
	}

	timeline := make([]sladomain.Phase, len(rows))
	for i, row := range rows {
		// Through Status.RunsClock, not a restatement of which statuses burn
		// budget. Restating it would make this agree with a second copy of the
		// rule rather than with the one the application uses.
		timeline[i] = sladomain.Phase{At: row.CreatedAt, Running: row.ToStatus.RunsClock()}
	}

	return policy.Reconstruct(timeline)
}

// reconstructByID is reconstruct for a ticket that already exists, resolving the
// policy it was snapshotted with rather than the one its priority resolves to
// now.
//
// By id, not by priority: editing a policy must not retroactively move the
// deadlines of tickets created under the old budget, and a consistency check
// that looked the policy up by priority would report a false mismatch the
// moment anyone did.
func reconstructByID(t *testing.T, f repoFixture, policyID int64,
	rows []ticketdb.TicketStatusHistory,
) (sladomain.ClockState, error) {
	t.Helper()

	policy, err := slapg.NewPolicyRepository(f.pool).ByID(f.ctx, policyID)
	if err != nil {
		t.Fatalf("reading policy %d: %v", policyID, err)
	}

	timeline := make([]sladomain.Phase, len(rows))
	for i, row := range rows {
		timeline[i] = sladomain.Phase{At: row.CreatedAt, Running: row.ToStatus.RunsClock()}
	}

	return policy.Reconstruct(timeline)
}
