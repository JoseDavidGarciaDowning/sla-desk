//go:build integration

package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/sla"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// The repo owns its own transaction, so these tests cannot use the rolled-back
// one the rest of the file shares. They clean up after themselves instead.
type repoFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	repo      *store.TicketRepo
	requester pgtype.UUID
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
	var requester pgtype.UUID
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

	return repoFixture{ctx: ctx, pool: pool, repo: store.NewTicketRepo(pool), requester: requester}
}

func (f repoFixture) newTicket(priority ticket.Priority) store.NewTicket {
	return store.NewTicket{
		RequesterID: f.requester,
		ActorRole:   ticket.RoleCustomer,
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    ticket.CategoryBilling,
		Priority:    priority,
	}
}

func TestCreateStartsTheClockRunning(t *testing.T) {
	f := newRepoFixture(t)

	tk, err := f.repo.Create(f.ctx, f.newTicket(ticket.PriorityNormal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if tk.Status != ticket.StatusOpen {
		t.Errorf("status = %q, want open", tk.Status)
	}
	if tk.SlaClockStartedAt == nil {
		t.Fatal("sla_clock_started_at is nil — a new ticket is open, so the clock runs")
	}
	if tk.SlaDueAt == nil {
		t.Fatal("sla_due_at is nil — a ticket that cannot breach is not on any clock")
	}
	if tk.SlaConsumedMicros != 0 {
		t.Errorf("consumed = %d, want 0 on a ticket that has never paused", tk.SlaConsumedMicros)
	}
	if tk.SlaBreachedAt != nil {
		t.Error("sla_breached_at is set on a brand new ticket")
	}
	if tk.SlaPolicyID == 0 {
		t.Error("sla_policy_id was not snapshotted")
	}
}

// Every priority resolves to the budget seeded in migration 002, and the
// deadline is that budget away from the moment the clock started. Written as
// literals rather than read back from sla_policies, so the test disagrees with
// the seed if the seed is wrong.
func TestDeadlineIsTheSeededBudgetForEveryPriority(t *testing.T) {
	f := newRepoFixture(t)

	budgets := map[ticket.Priority]time.Duration{
		ticket.PriorityUrgent: 60 * time.Minute,
		ticket.PriorityHigh:   240 * time.Minute,
		ticket.PriorityNormal: 1440 * time.Minute,
		ticket.PriorityLow:    4320 * time.Minute,
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
			want := tk.SlaClockStartedAt.Add(budget)
			if !tk.SlaDueAt.Equal(want) {
				t.Errorf("due at %v, want %v (started %v + %v)",
					tk.SlaDueAt, want, tk.SlaClockStartedAt, budget)
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

	tk, err := f.repo.Create(f.ctx, f.newTicket(ticket.PriorityUrgent))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	q := store.New(f.pool)

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
	if rows[0].ToStatus != ticket.StatusOpen {
		t.Errorf("to_status = %q, want open", rows[0].ToStatus)
	}

	policyRow, err := q.GetActiveSLAPolicyByPriority(f.ctx, tk.Priority)
	if err != nil {
		t.Fatalf("resolving the policy: %v", err)
	}

	history := make([]sla.StatusChange, len(rows))
	for i, row := range rows {
		history[i] = sla.StatusChange{To: row.ToStatus, At: row.CreatedAt}
	}

	state, err := sla.Reconstruct(sla.Policy{
		ID:       policyRow.ID,
		Priority: policyRow.Priority,
		Budget:   time.Duration(policyRow.BudgetMinutes) * time.Minute,
		Schedule: sla.Always24x7{},
	}, history)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if got := state.BudgetUsed.Microseconds(); got != tk.SlaConsumedMicros {
		t.Errorf("reconstructed consumed = %d micros, cached = %d", got, tk.SlaConsumedMicros)
	}
	if state.RunningSince == nil || !state.RunningSince.Equal(*tk.SlaClockStartedAt) {
		t.Errorf("reconstructed clock start = %v, cached = %v", state.RunningSince, tk.SlaClockStartedAt)
	}
	if state.DueAt == nil || !state.DueAt.Equal(*tk.SlaDueAt) {
		t.Errorf("reconstructed due = %v, cached = %v", state.DueAt, tk.SlaDueAt)
	}
}

// docs/spec.md §4.1: no history row, no transition. The ticket and its first
// history row are one write or they are neither.
func TestAFailedHistoryWriteLeavesNoTicket(t *testing.T) {
	f := newRepoFixture(t)

	in := f.newTicket(ticket.PriorityNormal)
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
	if !errors.Is(err, store.ErrNoPolicyForPriority) {
		t.Errorf("err = %v, want ErrNoPolicyForPriority", err)
	}
}
