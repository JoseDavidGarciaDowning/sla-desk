//go:build integration

// These tests need a migrated Postgres. Run them with `make test-int`, which
// applies the migrations first. They are behind a build tag so `make check`
// stays runnable without Docker.
package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

// begin opens a transaction that is always rolled back. Every test therefore
// sees the seeded database and leaves it exactly as it found it: no cleanup
// code to forget, and no ordering dependency between tests.
//
// It hands back the raw transaction as well as the generated queries. The
// constraint tests need to attempt writes that no generated query would ever
// produce — that is the point of them.
func begin(t *testing.T) (context.Context, pgx.Tx, *store.Queries) {
	t.Helper()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set — run `make up` then `make test-int`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	return ctx, tx, store.New(tx)
}

// rejectedBy returns the name of the constraint that refused the write.
// Asserting on the name rather than on "some error happened" is what keeps
// these tests specific: a statement that fails for an unrelated reason — a typo
// in the SQL, a missing column — does not quietly pass.
func rejectedBy(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected the database to reject the write, got no error")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a *pgconn.PgError, got %T: %v", err, err)
	}
	return pgErr.ConstraintName
}

// The four budgets in docs/spec.md §4.2, written out as literals on purpose.
// Deriving them from the migration would make this test agree with itself no
// matter what the migration said.
var wantBudgets = map[ticket.Priority]int32{
	ticket.PriorityUrgent: 60,
	ticket.PriorityHigh:   240,
	ticket.PriorityNormal: 1440,
	ticket.PriorityLow:    4320,
}

func TestSeedMatchesTheSpecBudgets(t *testing.T) {
	ctx, _, q := begin(t)

	policies, err := q.ListActiveSLAPolicies(ctx)
	if err != nil {
		t.Fatalf("ListActiveSLAPolicies: %v", err)
	}
	if len(policies) != len(wantBudgets) {
		t.Fatalf("active policies = %d, want %d", len(policies), len(wantBudgets))
	}

	got := make(map[ticket.Priority]int32, len(policies))
	for _, p := range policies {
		got[p.Priority] = p.BudgetMinutes

		if p.ScheduleMode != "24x7" {
			t.Errorf("%s: schedule_mode = %q, want 24x7 — internal/sla implements no other schedule", p.Priority, p.ScheduleMode)
		}
		if p.CreatedAt.IsZero() {
			t.Errorf("%s: created_at is the zero time", p.Priority)
		}
	}

	for priority, want := range wantBudgets {
		if got[priority] != want {
			t.Errorf("%s budget = %d minutes, want %d", priority, got[priority], want)
		}
	}
}

// The point of the sqlc override: a domain value goes in as a query parameter
// and the same domain type comes back out, with nothing converting at either
// end.
func TestPolicyLookupRoundTripsTheDomainType(t *testing.T) {
	ctx, _, q := begin(t)

	for priority, wantBudget := range wantBudgets {
		t.Run(string(priority), func(t *testing.T) {
			policy, err := q.GetActiveSLAPolicyByPriority(ctx, priority)
			if err != nil {
				t.Fatalf("GetActiveSLAPolicyByPriority(%q): %v", priority, err)
			}
			if policy.Priority != priority {
				t.Errorf("priority = %q, want %q", policy.Priority, priority)
			}
			if policy.BudgetMinutes != wantBudget {
				t.Errorf("budget = %d, want %d", policy.BudgetMinutes, wantBudget)
			}
			if policy.ID == 0 {
				t.Error("id = 0, want an identity value")
			}
		})
	}
}

// Every priority must resolve to exactly one policy. Without the partial unique
// index a second active row would make ticket creation pick a policy by row
// order — non-deterministic, and only visible in production.
func TestSecondActivePolicyForAPriorityIsRejected(t *testing.T) {
	ctx, tx, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes) VALUES ('Duplicate urgent', 'urgent', 30)`)

	if name := rejectedBy(t, err); name != "sla_policies_one_active_per_priority" {
		t.Errorf("constraint = %q, want sla_policies_one_active_per_priority", name)
	}
}

// A deactivated policy is history, not a conflict, so the same priority can be
// claimed again. This is what makes the unique index partial rather than total.
func TestInactivePolicyDoesNotBlockAnActiveOne(t *testing.T) {
	ctx, tx, _ := begin(t)

	if _, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Retired urgent', 'urgent', 30, FALSE)`); err != nil {
		t.Fatalf("inserting an inactive duplicate should be allowed: %v", err)
	}
}

func TestDatabaseRejectsAnUnknownPriority(t *testing.T) {
	ctx, tx, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Critical', 'critical', 15, FALSE)`)

	if name := rejectedBy(t, err); name != "sla_policies_priority_valid" {
		t.Errorf("constraint = %q, want sla_policies_priority_valid", name)
	}
}

func TestDatabaseRejectsANonPositiveBudget(t *testing.T) {
	ctx, tx, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Zero', 'urgent', 0, FALSE)`)

	if name := rejectedBy(t, err); name != "sla_policies_budget_positive" {
		t.Errorf("constraint = %q, want sla_policies_budget_positive", name)
	}
}

// internal/sla ships Always24x7 and nothing else. A row the code cannot
// interpret must not be creatable.
func TestDatabaseRejectsAnUnimplementedScheduleMode(t *testing.T) {
	ctx, tx, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, schedule_mode, active)
		 VALUES ('Business hours', 'urgent', 60, 'business_hours', FALSE)`)

	if name := rejectedBy(t, err); name != "sla_policies_schedule_mode_valid" {
		t.Errorf("constraint = %q, want sla_policies_schedule_mode_valid", name)
	}
}

func ptr[T any](v T) *T { return &v }
