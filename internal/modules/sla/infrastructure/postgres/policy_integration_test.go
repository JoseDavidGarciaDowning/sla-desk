//go:build integration

// These tests need a migrated Postgres. Run them with `make test-int`, which
// applies the migrations first. They are behind a build tag so `make check`
// stays runnable without Docker.
//
// They moved here from internal/store when the SLA module took ownership of
// sla_policies. The table is this module's, so the tests that pin its seed
// data, its constraints and its type mapping are this module's too — that is
// what module ownership means when it is more than a folder name.
package postgres_test

import (
	"errors"
	"testing"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres/sladb"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/pgtest"
)

// The four budgets in docs/spec.md §4.2, written out as literals on purpose.
// Deriving them from the migration would make this test agree with itself no
// matter what the migration said.
var wantBudgets = map[domain.Priority]int32{
	domain.PriorityUrgent: 60,
	domain.PriorityHigh:   240,
	domain.PriorityNormal: 1440,
	domain.PriorityLow:    4320,
}

func TestSeedMatchesTheSpecBudgets(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	policies, err := sladb.New(tx).ListActiveSLAPolicies(ctx)
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
			t.Errorf("%s: schedule_mode = %q, want 24x7 — this module implements no other schedule", p.Priority, p.ScheduleMode)
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
//
// Asserted through the repository rather than the generated query, because the
// repository is what everything above it actually calls — and it is where the
// schedule_mode string becomes a domain.Schedule.
func TestPolicyLookupRoundTripsTheDomainType(t *testing.T) {
	ctx, tx := pgtest.Begin(t)
	repo := postgres.NewPolicyRepository(tx)

	for priority, wantBudget := range wantBudgets {
		t.Run(string(priority), func(t *testing.T) {
			policy, err := repo.ActiveByPriority(ctx, priority)
			if err != nil {
				t.Fatalf("ActiveByPriority(%q): %v", priority, err)
			}
			if policy.Priority != priority {
				t.Errorf("priority = %q, want %q", policy.Priority, priority)
			}
			if got := int32(policy.Budget.Minutes()); got != wantBudget {
				t.Errorf("budget = %d minutes, want %d", got, wantBudget)
			}
			if policy.ID == 0 {
				t.Error("id = 0, want an identity value")
			}
			// A policy with no Schedule computes nothing. The column says
			// "24x7"; turning that name into behaviour is this layer's job, and
			// a nil here would mean it silently did not.
			if policy.Schedule == nil {
				t.Error("schedule is nil, want the one schedule_mode names")
			}
		})
	}
}

// A priority with no active policy is refused with a sentinel the caller can
// match, not with pgx.ErrNoRows leaking upward. Ticket creation depends on
// telling this apart from a database that is simply broken.
func TestActiveByPriorityReportsAMissingPolicy(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	if _, err := tx.Exec(ctx, `UPDATE sla_policies SET active = FALSE WHERE priority = 'urgent'`); err != nil {
		t.Fatalf("deactivating the urgent policy: %v", err)
	}

	_, err := postgres.NewPolicyRepository(tx).ActiveByPriority(ctx, domain.PriorityUrgent)
	if err == nil {
		t.Fatal("expected an error once no active policy serves urgent")
	}
	if !errors.Is(err, application.ErrNoPolicyForPriority) {
		t.Errorf("error = %v, want one wrapping ErrNoPolicyForPriority", err)
	}
}

// Every priority must resolve to exactly one policy. Without the partial unique
// index a second active row would make ticket creation pick a policy by row
// order — non-deterministic, and only visible in production.
func TestSecondActivePolicyForAPriorityIsRejected(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes) VALUES ('Duplicate urgent', 'urgent', 30)`)

	if name := pgtest.RejectedBy(t, err); name != "sla_policies_one_active_per_priority" {
		t.Errorf("constraint = %q, want sla_policies_one_active_per_priority", name)
	}
}

// A deactivated policy is history, not a conflict, so the same priority can be
// claimed again. This is what makes the unique index partial rather than total.
func TestInactivePolicyDoesNotBlockAnActiveOne(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	if _, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Retired urgent', 'urgent', 30, FALSE)`); err != nil {
		t.Fatalf("inserting an inactive duplicate should be allowed: %v", err)
	}
}

func TestDatabaseRejectsAnUnknownPriority(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Critical', 'critical', 15, FALSE)`)

	if name := pgtest.RejectedBy(t, err); name != "sla_policies_priority_valid" {
		t.Errorf("constraint = %q, want sla_policies_priority_valid", name)
	}
}

func TestDatabaseRejectsANonPositiveBudget(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, active) VALUES ('Zero', 'urgent', 0, FALSE)`)

	if name := pgtest.RejectedBy(t, err); name != "sla_policies_budget_positive" {
		t.Errorf("constraint = %q, want sla_policies_budget_positive", name)
	}
}

// This module ships Always24x7 and nothing else. A row the code cannot
// interpret must not be creatable.
func TestDatabaseRejectsAnUnimplementedScheduleMode(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO sla_policies (name, priority, budget_minutes, schedule_mode, active)
		 VALUES ('Business hours', 'urgent', 60, 'business_hours', FALSE)`)

	if name := pgtest.RejectedBy(t, err); name != "sla_policies_schedule_mode_valid" {
		t.Errorf("constraint = %q, want sla_policies_schedule_mode_valid", name)
	}
}
