//go:build integration

// These tests need a migrated Postgres. Run them with `make test-int`, which
// applies the migrations first. They are behind a build tag so `make check`
// stays runnable without Docker.
//
// They moved here from internal/store when the identity module took ownership
// of the users table. They also moved *down* a level: they used to call the
// generated queries directly and now go through the repository, because the
// repository is what everything above it actually calls — and it is where the
// nil-versus-empty name decision now lives.
package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/pgtest"
)

// The role column carries DEFAULT 'customer' as a second line of defence: every
// write path sets the role explicitly, so the default only ever fires if one
// forgets. That makes it exactly the kind of safety net that rots untested —
// mutation testing found it uncovered, because the upsert writes the literal
// 'customer' and never exercises the default at all.
func TestOmittedRoleDefaultsToCustomer(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	var role domain.Role
	err := tx.QueryRow(ctx,
		`INSERT INTO users (clerk_user_id, email) VALUES ('user_no_role', 'norole@example.test') RETURNING role`,
	).Scan(&role)
	if err != nil {
		t.Fatalf("insert without a role: %v", err)
	}
	if role != domain.RoleCustomer {
		t.Errorf("role = %q, want customer — a forgotten role must land on the least privileged one", role)
	}
}

func TestDatabaseRejectsAnUnknownRole(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	_, err := tx.Exec(ctx,
		`INSERT INTO users (clerk_user_id, email, role) VALUES ('user_bad_role', 'a@example.test', 'superadmin')`)

	if name := pgtest.RejectedBy(t, err); name != "users_role_valid" {
		t.Errorf("constraint = %q, want users_role_valid", name)
	}
}

// Two Clerk identities can legitimately carry the same address. A unique index
// on email would break the upsert on a path we do not control, so this must be
// allowed.
func TestDuplicateEmailIsAllowed(t *testing.T) {
	ctx, tx := pgtest.Begin(t)
	repo := postgres.NewUserRepository(tx)

	for _, id := range []string{"user_dup_a", "user_dup_b"} {
		if _, err := repo.Upsert(ctx, id, domain.Identity{Email: "shared@example.test"}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
}

// The resolution of the signup race in docs/spec.md §4.5: whichever of the
// webhook and the lazy fallback arrives first creates the row, and the second
// updates instead of failing.
func TestUpsertIsIdempotent(t *testing.T) {
	ctx, tx := pgtest.Begin(t)
	repo := postgres.NewUserRepository(tx)

	first, err := repo.Upsert(ctx, "user_race_test", domain.Identity{
		Email: "before@example.test",
		Name:  "Before",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Role != domain.RoleCustomer {
		t.Errorf("role = %q, want customer — a new user is never seeded with any other role", first.Role)
	}
	if first.Name != "Before" {
		t.Errorf("name = %q, want Before", first.Name)
	}

	second, err := repo.Upsert(ctx, "user_race_test", domain.Identity{
		Email: "after@example.test",
		Name:  "After",
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
	if second.Name != "After" {
		t.Errorf("name = %q, want After — the conflict branch is not refreshing it", second.Name)
	}

	// updated_at is deliberately not asserted here. now() is the transaction
	// timestamp, so both upserts inside this rolled-back transaction share it
	// and any comparison would pass no matter what the query did.
}

// Clerk does not guarantee a name, so the column is nullable. An identity with
// no name must land as NULL rather than as an empty string, and come back out
// as the empty string rather than as a blank one somebody has to check for.
//
// This is the assertion the transport-level test can no longer make: turning ""
// into a nil parameter is this layer's job now.
func TestUpsertStoresAMissingNameAsNull(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	user, err := postgres.NewUserRepository(tx).Upsert(ctx, "user_no_name",
		domain.Identity{Email: "noname@example.test"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if user.Name != "" {
		t.Errorf("name = %q, want empty", user.Name)
	}

	// Read the column itself: the round trip above would look identical if ''
	// had been stored, and '' is a name nobody has.
	var name *string
	if err := tx.QueryRow(ctx,
		`SELECT name FROM users WHERE clerk_user_id = 'user_no_name'`).Scan(&name); err != nil {
		t.Fatalf("reading the column: %v", err)
	}
	if name != nil {
		t.Errorf("stored name = %q, want NULL", *name)
	}
}

// An existing agent must survive a replayed webhook. Svix retries any non-2xx
// response, so this path runs more often than it looks.
func TestUpsertDoesNotDemoteAnExistingAgent(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (clerk_user_id, email, role) VALUES ('user_agent', 'agent@example.test', 'agent')`); err != nil {
		t.Fatalf("seeding an agent: %v", err)
	}

	got, err := postgres.NewUserRepository(tx).Upsert(ctx, "user_agent",
		domain.Identity{Email: "agent@example.test", Name: "Agent"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.Role != domain.RoleAgent {
		t.Errorf("role = %q, want agent — the upsert demoted an existing agent", got.Role)
	}
}

// An unknown subject is reported with the module's own sentinel, not with
// pgx.ErrNoRows. RequireAuth distinguishes this case to trigger the lazy
// upsert, and it must not have to know pgx exists to do so.
func TestByClerkIDReportsAnUnknownSubject(t *testing.T) {
	ctx, tx := pgtest.Begin(t)

	_, err := postgres.NewUserRepository(tx).ByClerkID(ctx, "user_does_not_exist")
	if !errors.Is(err, application.ErrUserNotFound) {
		t.Errorf("err = %v, want one wrapping ErrUserNotFound", err)
	}
}

// The two provisioning paths of docs/spec.md §4.5 are independent HTTP requests
// that can land at the same instant: Clerk's webhook and the browser's first
// authenticated call. Both run this upsert with the same clerk_user_id.
//
// This one cannot use the rolled-back transaction the other tests share — the
// whole point is several connections racing — so it cleans up after itself.
func TestConcurrentUpsertsCreateExactlyOneUser(t *testing.T) {
	ctx, pool := pgtest.Pool(t)

	const clerkID = "user_concurrent_race"
	cleanup := func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM users WHERE clerk_user_id = $1`, clerkID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	const racers = 16
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, racers)
	errs := make([]error, racers)

	repo := postgres.NewUserRepository(pool)

	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them together, so they actually collide

			user, err := repo.Upsert(ctx, clerkID, domain.Identity{
				Email: "race@example.test",
				Name:  "Racer",
			})
			ids[i], errs[i] = user.ID, err
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d failed: %v — the upsert is not safe under concurrency", i, err)
		}
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE clerk_user_id = $1`, clerkID).Scan(&rows); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if rows != 1 {
		t.Fatalf("users rows = %d, want 1", rows)
	}

	// Every caller must have been handed the same row, not just have avoided an
	// error. A racer that received a different id would be holding a user that
	// no longer exists.
	for i, id := range ids {
		if id != ids[0] {
			t.Errorf("racer %d got id %v, racer 0 got %v", i, id, ids[0])
		}
	}
}
