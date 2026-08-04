//go:build integration

// These tests need a migrated Postgres. Run them with `make test-int`.
//
// They moved here from internal/store with the sla_policies table. Two of them
// changed shape rather than address: what used to call the generated queries
// now goes through the module's repository, because that is what production
// calls and it is where a row becomes a domain Policy — including the schedule
// interpretation, which no generated query performs.
package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres/sladb"
)

// begin opens a transaction that is always rolled back, so every test sees the
// seeded database and leaves it exactly as it found it.
//
// It hands back the raw transaction, the generated queries and the repository.
// The constraint tests need writes no query in this module would ever produce —
// that is the point of them — and the repository is what the rest exercise.
func begin(t *testing.T) (context.Context, pgx.Tx, *sladb.Queries, *postgres.PolicyRepository) {
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

	return ctx, tx, sladb.New(tx), postgres.NewPolicyRepository(tx)
}

// rejectedBy returns the name of the constraint that refused the write.
// Asserting on the name rather than on "some error happened" keeps these tests
// specific: a statement that fails for an unrelated reason does not pass.
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

var wantBudgets = map[domain.Priority]int32{
	domain.PriorityUrgent: 60,
	domain.PriorityHigh:   240,
	domain.PriorityNormal: 1440,
	domain.PriorityLow:    4320,
}

func TestSeedMatchesTheSpecBudgets(t *testing.T) {
	ctx, _, q, _ := begin(t)

	policies, err := q.ListActiveSLAPolicies(ctx)
	if err != nil {
		t.Fatalf("ListActiveSLAPolicies: %v", err)
	}
	if len(policies) != len(wantBudgets) {
		t.Fatalf("active policies = %d, want %d", len(policies), len(wantBudgets))
	}

	got := make(map[domain.Priority]int32, len(policies))
	for _, p := range policies {
		got[p.Priority] = p.BudgetMinutes

		if p.ScheduleMode != "24x7" {
			t.Errorf("%s: schedule_mode = %q, want 24x7 — the SLA domain implements no other schedule", p.Priority, p.ScheduleMode)
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

// The point of the sqlc override, plus what the repository adds on top: a
// domain value goes in as a query parameter, the same domain type comes back,
// and the budget arrives as a Duration rather than as a column of minutes.
func TestPolicyLookupRoundTripsTheDomainType(t *testing.T) {
	ctx, _, _, repo := begin(t)

	for priority, wantBudget := range wantBudgets {
		t.Run(string(priority), func(t *testing.T) {
			policy, err := repo.ActiveByPriority(ctx, priority)
			if err != nil {
				t.Fatalf("ActiveByPriority(%q): %v", priority, err)
			}
			if policy.Priority != priority {
				t.Errorf("priority = %q, want %q", policy.Priority, priority)
			}
			if want := time.Duration(wantBudget) * time.Minute; policy.Budget != want {
				t.Errorf("budget = %s, want %s", policy.Budget, want)
			}
			if policy.ID == 0 {
				t.Error("id = 0, want an identity value")
			}
			if policy.Schedule == nil {
				t.Error("schedule is nil — the repository did not interpret schedule_mode")
			}
		})
	}
}

// No active policy is not a driver error to the caller. Ticket creation
// branches on this sentinel to answer "our seed data is wrong" differently from
// "the database is down", and it cannot import a driver to tell them apart.
func TestAnUnservedPriorityIsReportedAsNoPolicy(t *testing.T) {
	ctx, tx, _, repo := begin(t)

	if _, err := tx.Exec(ctx,
		`UPDATE sla_policies SET active = FALSE WHERE priority = 'low'`); err != nil {
		t.Fatalf("deactivating: %v", err)
	}

	_, err := repo.ActiveByPriority(ctx, domain.PriorityLow)
	if !errors.Is(err, application.ErrNoPolicyForPriority) {
		t.Errorf("err = %v, want application.ErrNoPolicyForPriority", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		t.Error("the driver's error reached the caller; the translation exists so it does not")
	}
}

// Every priority must resolve to exactly one policy. Without the partial unique
// index a second active row would make ticket creation pick a policy by row
// order — non-deterministic, and only visible in production.
func TestSecondActivePolicyForAPriorityIsRejected(t *testing.T) {
	ctx, tx, _, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes) VALUES ('Duplicate urgent', 'urgent', 30)`)

	if name := rejectedBy(t, err); name != "sla_policies_one_active_per_priority" {
		t.Errorf("constraint = %q, want sla_policies_one_active_per_priority", name)
	}
}

// A deactivated policy is history, not a conflict, so the same priority can be
// claimed again. This is what makes the unique index partial rather than total.
func TestInactivePolicyDoesNotBlockAnActiveOne(t *testing.T) {
	ctx, tx, _, _ := begin(t)

	if _, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Retired urgent', 'urgent', 30, FALSE)`); err != nil {
		t.Fatalf("inserting an inactive duplicate should be allowed: %v", err)
	}
}

func TestDatabaseRejectsAnUnknownPriority(t *testing.T) {
	ctx, tx, _, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Critical', 'critical', 15, FALSE)`)

	if name := rejectedBy(t, err); name != "sla_policies_priority_valid" {
		t.Errorf("constraint = %q, want sla_policies_priority_valid", name)
	}
}

func TestDatabaseRejectsANonPositiveBudget(t *testing.T) {
	ctx, tx, _, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Zero', 'urgent', 0, FALSE)`)

	if name := rejectedBy(t, err); name != "sla_policies_budget_positive" {
		t.Errorf("constraint = %q, want sla_policies_budget_positive", name)
	}
}

// The SLA domain ships Always24x7 and nothing else. A row the code cannot
// interpret must not be creatable.
func TestDatabaseRejectsAnUnimplementedScheduleMode(t *testing.T) {
	ctx, tx, _, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, schedule_mode, active)
		 VALUES ('Business hours', 'urgent', 60, 'business_hours', FALSE)`)

	if name := rejectedBy(t, err); name != "sla_policies_schedule_mode_valid" {
		t.Errorf("constraint = %q, want sla_policies_schedule_mode_valid", name)
	}
}
