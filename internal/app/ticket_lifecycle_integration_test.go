//go:build integration

package app_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/app"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	slaapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	sladomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	slapostgres "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	ticketpostgres "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/ticketdb"
)

// The repo owns its own transaction, so these tests cannot use the rolled-back
// one the rest of the file shares. They clean up after themselves instead.
type repoFixture struct {
	ctx  context.Context
	pool *pgxpool.Pool
	repo *ticketpostgres.Repository

	// svc is the repository behind the use cases that resolve an SLA clock for
	// it. The write paths are exercised through this rather than through repo,
	// because resolving-then-writing is the sequence production runs and the
	// repository alone cannot produce a clock.
	svc *ticketapp.Service

	requester uuid.UUID
}

func newRepoFixture(t *testing.T) repoFixture {
	t.Helper()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set — run `make up` then `make test-int`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

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

	repo := ticketpostgres.NewRepository(pool)
	// The production adapter, not a copy of it. In the ticket module this test
	// had to rebuild the translation by hand — importing the SLA module from
	// inside ticket, which depguard now refuses. Here it is one import, and the
	// test exercises the very code cmd/api wires.
	sla := app.SLAPolicies{Calculator: slaapp.NewCalculator(slapostgres.NewPolicyRepository(pool))}

	return repoFixture{
		ctx:       ctx,
		pool:      pool,
		repo:      repo,
		svc:       ticketapp.NewService(repo, sla),
		requester: requester,
	}
}

func (f repoFixture) newTicket(priority ticketdomain.Priority) ticketapp.NewTicket {
	return ticketapp.NewTicket{
		RequesterID: f.requester,
		ActorRole:   ticketdomain.RoleCustomer,
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    ticketdomain.CategoryBilling,
		Priority:    priority,
	}
}

func TestCreateStartsTheClockRunning(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityNormal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if tk.Status != ticketdomain.StatusOpen {
		t.Errorf("status = %q, want open", tk.Status)
	}
	if tk.SLAClockStartedAt == nil {
		t.Fatal("sla_clock_started_at is nil — a new ticket is open, so the clock runs")
	}
	if tk.SLADueAt == nil {
		t.Fatal("sla_due_at is nil — a ticket that cannot breach is not on any clock")
	}
	if tk.SLAConsumed != 0 {
		t.Errorf("consumed = %d, want 0 on a ticket that has never paused", tk.SLAConsumed)
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

	budgets := map[ticketdomain.Priority]time.Duration{
		ticketdomain.PriorityUrgent: 60 * time.Minute,
		ticketdomain.PriorityHigh:   240 * time.Minute,
		ticketdomain.PriorityNormal: 1440 * time.Minute,
		ticketdomain.PriorityLow:    4320 * time.Minute,
	}

	for priority, budget := range budgets {
		t.Run(string(priority), func(t *testing.T) {
			tk, err := f.svc.Create(f.ctx, f.newTicket(priority))
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

	tk, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityUrgent))
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
	if rows[0].ToStatus != ticketdomain.StatusOpen {
		t.Errorf("to_status = %q, want open", rows[0].ToStatus)
	}

	policy, err := slapostgres.NewPolicyRepository(f.pool).ActiveByPriority(f.ctx, sladomain.Priority(tk.Priority))
	if err != nil {
		t.Fatalf("resolving the policy: %v", err)
	}

	timeline := make([]sladomain.Phase, len(rows))
	for i, row := range rows {
		timeline[i] = sladomain.Phase{At: row.CreatedAt, Running: row.ToStatus.RunsClock()}
	}

	state, err := sladomain.Reconstruct(policy, timeline)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if state.BudgetUsed != tk.SLAConsumed {
		t.Errorf("reconstructed consumed = %s, cached = %s", state.BudgetUsed, tk.SLAConsumed)
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

	in := f.newTicket(ticketdomain.PriorityNormal)
	in.ActorRole = "superadmin" // rejected by ticket_status_history_actor_role_valid

	if _, err := f.svc.Create(f.ctx, in); err == nil {
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

	_, err := f.svc.Create(f.ctx, in)
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
	q := ticketdb.New(f.pool)

	const total = 5
	for range total {
		if _, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityNormal)); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	page := func(after *ticketdb.Ticket, size int32) []ticketdb.Ticket {
		t.Helper()
		params := ticketdb.ListTicketsByRequesterParams{RequesterID: f.requester, PageSize: size}
		if after != nil {
			params.AfterCreatedAt = &after.CreatedAt
			params.AfterID = &after.ID
		}
		rows, err := q.ListTicketsByRequester(f.ctx, params)
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
	if _, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityUrgent)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	second := page(&first[len(first)-1], 10)

	seen := map[string]bool{}
	for _, row := range append(append([]ticketdb.Ticket{}, first...), second...) {
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
	q := ticketdb.New(f.pool)

	// Each Create is its own transaction, so force the tie instead.
	for range 3 {
		if _, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityNormal)); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE tickets SET created_at = now() WHERE requester_id = $1`, f.requester); err != nil {
		t.Fatalf("flattening the timestamps: %v", err)
	}

	var collected []ticketdb.Ticket
	var after *ticketdb.Ticket
	for range 5 { // bounded, so a cursor that fails to advance cannot spin
		params := ticketdb.ListTicketsByRequesterParams{RequesterID: f.requester, PageSize: 1}
		if after != nil {
			params.AfterCreatedAt = &after.CreatedAt
			params.AfterID = &after.ID
		}
		rows, err := q.ListTicketsByRequester(f.ctx, params)
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
// schedule the SLA module cannot interpret, which fails after the history row has
// already been inserted. If the two were in separate transactions the history
// row would survive and the ticket would carry a status its history never
// records.
func TestAFailedCacheUpdateLeavesNoHistoryRow(t *testing.T) {
	f := newRepoFixture(t)
	q := ticketdb.New(f.pool)

	tk, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityNormal))
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

	if _, err := f.svc.Transition(f.ctx, ticketapp.StatusChange{
		TicketID:  tk.ID,
		Target:    ticketdomain.StatusPending,
		ActorID:   f.requester,
		ActorRole: ticketdomain.RoleAdmin,
	}); !errors.Is(err, slapostgres.ErrUnsupportedScheduleMode) {
		t.Fatalf("err = %v, want ErrUnsupportedScheduleMode — the SLA module refuses a schedule it cannot compute with", err)
	}

	after, err := q.ListTicketStatusHistory(f.ctx, tk.ID)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("history grew from %d to %d rows on a transition that failed", len(before), len(after))
	}

	stored, err := q.GetTicketForRequester(f.ctx, ticketdb.GetTicketForRequesterParams{
		ID: tk.ID, RequesterID: f.requester,
	})
	if err != nil {
		t.Fatalf("reading the ticket: %v", err)
	}
	if stored.Status != ticketdomain.StatusOpen {
		t.Errorf("status = %q, want it unchanged at open", stored.Status)
	}
}

// A closed ticket is terminal, and the database is not the thing enforcing it —
// the domain is. This is the end-to-end proof that the pure state machine is
// actually consulted by the write path.
func TestTransitionRefusesToLeaveClosed(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityNormal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, target := range []ticketdomain.Status{ticketdomain.StatusResolved, ticketdomain.StatusClosed} {
		tk, err = f.svc.Transition(f.ctx, ticketapp.StatusChange{
			TicketID: tk.ID, Target: target, ActorID: f.requester, ActorRole: ticketdomain.RoleAdmin,
		})
		if err != nil {
			t.Fatalf("moving to %s: %v", target, err)
		}
	}

	_, err = f.svc.Transition(f.ctx, ticketapp.StatusChange{
		TicketID: tk.ID, Target: ticketdomain.StatusOpen, ActorID: f.requester, ActorRole: ticketdomain.RoleAdmin,
	})
	if !errors.Is(err, ticketdomain.ErrInvalidTransition) {
		t.Errorf("err = %v, want ErrInvalidTransition — closed is terminal", err)
	}
}

// Pausing stops the clock and clears the deadline; resuming starts it again
// from the budget already spent. This is the behaviour the whole SLA model
// exists for, and until now nothing exercised it.
func TestPausingStopsTheClockAndResumingKeepsWhatWasSpent(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.svc.Create(f.ctx, f.newTicket(ticketdomain.PriorityUrgent))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	paused, err := f.svc.Transition(f.ctx, ticketapp.StatusChange{
		TicketID: tk.ID, Target: ticketdomain.StatusPending, ActorID: f.requester, ActorRole: ticketdomain.RoleAdmin,
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

	resumed, err := f.svc.Transition(f.ctx, ticketapp.StatusChange{
		TicketID: tk.ID, Target: ticketdomain.StatusOpen, ActorID: f.requester, ActorRole: ticketdomain.RoleCustomer,
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
