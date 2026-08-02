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

// The role column carries DEFAULT 'customer' as a second line of defence: every
// write path sets the role explicitly, so the default only ever fires if one
// forgets. That makes it exactly the kind of safety net that rots untested —
// mutation testing found it uncovered, because the upsert writes the literal
// 'customer' and never exercises the default at all.
func TestOmittedRoleDefaultsToCustomer(t *testing.T) {
	ctx, tx, _ := begin(t)

	var role ticket.Role
	err := tx.QueryRow(ctx,
		`INSERT INTO users (clerk_user_id, email) VALUES ('user_no_role', 'norole@example.test') RETURNING role`,
	).Scan(&role)
	if err != nil {
		t.Fatalf("insert without a role: %v", err)
	}
	if role != ticket.RoleCustomer {
		t.Errorf("role = %q, want customer — a forgotten role must land on the least privileged one", role)
	}
}

func TestDatabaseRejectsAnUnknownRole(t *testing.T) {
	ctx, tx, _ := begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO users (clerk_user_id, email, role) VALUES ('user_bad_role', 'a@example.test', 'superadmin')`)

	if name := rejectedBy(t, err); name != "users_role_valid" {
		t.Errorf("constraint = %q, want users_role_valid", name)
	}
}

// Two Clerk identities can legitimately carry the same address. A unique index
// on email would break the upsert on a path we do not control, so this must be
// allowed.
func TestDuplicateEmailIsAllowed(t *testing.T) {
	ctx, _, q := begin(t)

	for _, id := range []string{"user_dup_a", "user_dup_b"} {
		if _, err := q.UpsertUserFromClerk(ctx, store.UpsertUserFromClerkParams{
			ClerkUserID: id,
			Email:       "shared@example.test",
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
}

// The resolution of the signup race in docs/spec.md §4.5: whichever of the
// webhook and the lazy fallback arrives first creates the row, and the second
// updates instead of failing.
func TestUpsertUserFromClerkIsIdempotent(t *testing.T) {
	ctx, _, q := begin(t)

	first, err := q.UpsertUserFromClerk(ctx, store.UpsertUserFromClerkParams{
		ClerkUserID: "user_race_test",
		Email:       "before@example.test",
		Name:        ptr("Before"),
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Role != ticket.RoleCustomer {
		t.Errorf("role = %q, want customer — a new user is never seeded with any other role", first.Role)
	}
	if first.Name == nil || *first.Name != "Before" {
		t.Errorf("name = %v, want Before", first.Name)
	}

	second, err := q.UpsertUserFromClerk(ctx, store.UpsertUserFromClerkParams{
		ClerkUserID: "user_race_test",
		Email:       "after@example.test",
		Name:        ptr("After"),
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("id changed between upserts: %v then %v — the second call created a second row", first.ID, second.ID)
	}
	if second.Email != "after@example.test" {
		t.Errorf("email = %q, want the second call's value", second.Email)
	}
	if second.Name == nil || *second.Name != "After" {
		t.Errorf("name = %v, want After — the conflict branch is not refreshing it", second.Name)
	}

	// updated_at is deliberately not asserted here. now() is the transaction
	// timestamp, so both upserts inside this rolled-back transaction share it
	// and any comparison would pass no matter what the query did.
}

// Clerk does not guarantee a name, so the column is nullable and the parameter
// is a pointer. nil must round-trip as NULL rather than as an empty string.
func TestUpsertAcceptsAMissingName(t *testing.T) {
	ctx, _, q := begin(t)

	user, err := q.UpsertUserFromClerk(ctx, store.UpsertUserFromClerkParams{
		ClerkUserID: "user_no_name",
		Email:       "noname@example.test",
		Name:        nil,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if user.Name != nil {
		t.Errorf("name = %q, want nil", *user.Name)
	}
}

// An existing agent must survive a replayed webhook. Svix retries any non-2xx
// response, so this path runs more often than it looks.
func TestUpsertDoesNotDemoteAnExistingAgent(t *testing.T) {
	ctx, tx, q := begin(t)

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (clerk_user_id, email, role) VALUES ('user_agent', 'agent@example.test', 'agent')`); err != nil {
		t.Fatalf("seeding an agent: %v", err)
	}

	got, err := q.UpsertUserFromClerk(ctx, store.UpsertUserFromClerkParams{
		ClerkUserID: "user_agent",
		Email:       "agent@example.test",
		Name:        ptr("Agent"),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.Role != ticket.RoleAgent {
		t.Errorf("role = %q, want agent — the upsert demoted an existing agent", got.Role)
	}
}

func TestGetUserByClerkIDReturnsNoRowsForAnUnknownSubject(t *testing.T) {
	ctx, _, q := begin(t)

	_, err := q.GetUserByClerkID(ctx, "user_does_not_exist")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err = %v, want pgx.ErrNoRows — RequireAuth distinguishes this case to trigger the lazy upsert", err)
	}
}

func ptr[T any](v T) *T { return &v }
