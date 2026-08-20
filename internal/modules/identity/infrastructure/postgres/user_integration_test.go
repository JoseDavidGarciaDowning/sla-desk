//go:build integration

// These tests need a migrated Postgres. Run them with `make test-int`, which
// applies the migrations first. They are behind a build tag so `make check`
// stays runnable without Docker.
//
// They moved here from internal/store with the users table. What changed is not
// only their address: they now exercise the module's repository rather than the
// generated queries directly, so what is under test is the code production
// actually calls — including the error translation, which is the part a caller
// depends on and which no generated query performs.
package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/infrastructure/postgres"
)

// begin opens a transaction that is always rolled back. Every test therefore
// sees the seeded database and leaves it exactly as it found it: no cleanup
// code to forget, and no ordering dependency between tests.
//
// It hands back the raw transaction as well as the repository. The constraint
// tests need to attempt writes that no query in this module would ever produce
// — that is the point of them.
func begin(t *testing.T) (context.Context, pgx.Tx, *postgres.UserRepository) {
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

	return ctx, tx, postgres.NewUserRepository(tx)
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

// The role column carries DEFAULT 'customer' as a second line of defence: every
// write path sets the role explicitly, so the default only ever fires if one
// forgets. That makes it exactly the kind of safety net that rots untested —
// mutation testing found it uncovered, because the upsert writes the literal
// 'customer' and never exercises the default at all.
func TestOmittedRoleDefaultsToCustomer(t *testing.T) {
	ctx, tx, _ := begin(t)

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
	ctx, _, repo := begin(t)

	for _, id := range []string{"user_dup_a", "user_dup_b"} {
		if _, err := repo.Upsert(ctx, id, domain.Identity{Email: "shared@example.test"}, domain.RoleCustomer); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
}

// The resolution of the signup race in docs/spec.md §4.5: whichever of the
// webhook and the lazy fallback arrives first creates the row, and the second
// updates instead of failing.
func TestUpsertIsIdempotent(t *testing.T) {
	ctx, _, repo := begin(t)

	first, err := repo.Upsert(ctx, "user_race_test", domain.Identity{
		Email: "before@example.test",
		Name:  "Before",
	}, domain.RoleCustomer)
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
	}, domain.RoleCustomer)
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

// Clerk does not guarantee a name, and the column has to distinguish "not
// provided" from "blank".
//
// The domain carries a plain string, so the repository is what turns an empty
// one into NULL — and the assertion reads the column rather than the value
// handed back, because a returned "" is exactly what a blank string would give
// too. That is the distinction under test.
func TestUpsertStoresAMissingNameAsNull(t *testing.T) {
	ctx, tx, repo := begin(t)

	user, err := repo.Upsert(ctx, "user_no_name", domain.Identity{Email: "noname@example.test"}, domain.RoleCustomer)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if user.Name != "" {
		t.Errorf("name = %q, want empty", user.Name)
	}

	var isNull bool
	if err := tx.QueryRow(ctx,
		`SELECT name IS NULL FROM users WHERE clerk_user_id = 'user_no_name'`).Scan(&isNull); err != nil {
		t.Fatalf("reading the column back: %v", err)
	}
	if !isNull {
		t.Error("name is stored as a blank string, want NULL — the column can no longer tell them apart")
	}
}

// An existing agent must survive a replayed webhook. Svix retries any non-2xx
// response, so this path runs more often than it looks.
func TestUpsertDoesNotDemoteAnExistingAgent(t *testing.T) {
	ctx, tx, repo := begin(t)

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (clerk_user_id, email, role) VALUES ('user_agent', 'agent@example.test', 'agent')`); err != nil {
		t.Fatalf("seeding an agent: %v", err)
	}

	// The role argument says 'customer', which is the demotion under test:
	// since slice 2 it is a parameter rather than a literal, so the guarantee
	// now has to survive a caller passing the wrong thing.
	got, err := repo.Upsert(ctx, "user_agent",
		domain.Identity{Email: "refreshed@example.test", Name: "Agent"}, domain.RoleCustomer)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.Role != domain.RoleAgent {
		t.Errorf("role = %q, want agent — the upsert demoted an existing agent", got.Role)
	}
	// The rest of the update must still apply. A conflict clause that protected
	// the role by doing nothing at all would pass the assertion above.
	if got.Email != "refreshed@example.test" {
		t.Errorf("email = %q, want the update to still have applied", got.Email)
	}
}

// The repository translates the driver's "no rows" into the module's own
// sentinel. EnsureUser branches on it to decide whether to provision, so a
// translation that stopped happening would turn every first request into a 500
// instead of a signup.
func TestUnknownSubjectIsReportedAsNoSuchUser(t *testing.T) {
	ctx, _, repo := begin(t)

	_, err := repo.ByClerkID(ctx, "user_does_not_exist")
	if !errors.Is(err, domain.ErrNoSuchUser) {
		t.Errorf("err = %v, want domain.ErrNoSuchUser", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		t.Error("the driver's error reached the caller; the point of the translation is that it does not")
	}
}

// The two provisioning paths of docs/spec.md §4.5 are independent HTTP requests
// that can land at the same instant: Clerk's webhook and the browser's first
// authenticated call. Both run this upsert with the same clerk_user_id.
//
// This one cannot use the rolled-back transaction the other tests share — the
// whole point is several connections racing — so it cleans up after itself.
func TestConcurrentUpsertsCreateExactlyOneUser(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set — run `make up` then `make test-int`")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	// Registered before the row cleanup so it runs after it: t.Cleanup is LIFO.
	// A deferred pool.Close() would run first and leave the cleanup with a
	// closed pool.
	t.Cleanup(pool.Close)

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
			}, domain.RoleCustomer)
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

// --- Role grants (slice 2, T17) ------------------------------------------
//
// These prove the guarantees live in the SQL rather than in the caller. Every
// one of them writes through the repository the application layer actually
// holds, so a rule that only exists in an if statement upstream would not be
// enough to make them pass.

// The grant has to land on the very first write. Provisioning a listed agent as
// a customer and promoting them later would leave a window in which they sign
// in and are refused the routes they were granted.
func TestUpsertWritesTheGrantedRoleOnInsert(t *testing.T) {
	ctx, _, repo := begin(t)

	user, err := repo.Upsert(ctx, "user_granted_agent",
		domain.Identity{Email: "agent@example.test", Name: "An Agent"}, domain.RoleAgent)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if user.Role != domain.RoleAgent {
		t.Errorf("Role = %q, want %q", user.Role, domain.RoleAgent)
	}
}

// The ordinary case: you cannot know a Clerk subject until that person has
// signed up, so a grant almost always arrives after the row exists.
func TestGrantRolePromotesAnExistingCustomer(t *testing.T) {
	ctx, _, repo := begin(t)

	before, err := repo.Upsert(ctx, "user_to_promote",
		domain.Identity{Email: "promote@example.test"}, domain.RoleCustomer)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	after, err := repo.GrantRole(ctx, "user_to_promote", domain.RoleAgent)
	if err != nil {
		t.Fatalf("GrantRole: %v", err)
	}

	if after.Role != domain.RoleAgent {
		t.Errorf("Role = %q, want %q", after.Role, domain.RoleAgent)
	}
	if after.ID != before.ID {
		t.Errorf("ID changed from %s to %s — the row was replaced, not promoted", before.ID, after.ID)
	}
}

// The load-bearing test of T17. The predicate in GrantUserRole is what makes a
// demotion unrepresentable: there is no argument to this method that takes a
// role away, so an id removed from the grant list — or mistyped in it — cannot
// strip an agent mid-shift. If the predicate is ever deleted, this fails.
func TestGrantRoleStructurallyCannotDemote(t *testing.T) {
	ctx, _, repo := begin(t)

	if _, err := repo.Upsert(ctx, "user_agent_kept",
		domain.Identity{Email: "kept@example.test"}, domain.RoleAgent); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	_, err := repo.GrantRole(ctx, "user_agent_kept", domain.RoleCustomer)
	if !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("GrantRole error = %v, want ErrNoSuchUser — nothing may be written", err)
	}

	still, err := repo.ByClerkID(ctx, "user_agent_kept")
	if err != nil {
		t.Fatalf("ByClerkID: %v", err)
	}
	if still.Role != domain.RoleAgent {
		t.Errorf("Role = %q, want the agent role to have survived the attempt", still.Role)
	}
}

// An admin moved into the agent list is a real reconfiguration, not a
// demotion, and the predicate must not block it — it only refuses 'customer'.
func TestGrantRoleMovesAUserBetweenPrivilegedRoles(t *testing.T) {
	ctx, _, repo := begin(t)

	if _, err := repo.Upsert(ctx, "user_moving",
		domain.Identity{Email: "moving@example.test"}, domain.RoleAgent); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	after, err := repo.GrantRole(ctx, "user_moving", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("GrantRole: %v", err)
	}
	if after.Role != domain.RoleAdmin {
		t.Errorf("Role = %q, want %q", after.Role, domain.RoleAdmin)
	}
}

// A grant for someone who never signed up writes nothing and says so. The
// caller must not receive a row implying a user exists.
func TestGrantRoleReportsAnUnknownSubject(t *testing.T) {
	ctx, _, repo := begin(t)

	_, err := repo.GrantRole(ctx, "user_never_seen", domain.RoleAgent)
	if !errors.Is(err, domain.ErrNoSuchUser) {
		t.Errorf("GrantRole error = %v, want ErrNoSuchUser", err)
	}
}

// --- The staff roster (T24) -----------------------------------------------

// The predicate is in the query rather than a filter above it, so a customer
// cannot reach this list even from a caller that forgot to check.
func TestTheRosterHoldsStaffAndNobodyElse(t *testing.T) {
	ctx, tx, repo := begin(t)

	seed := func(clerkID string, role domain.Role) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (clerk_user_id, email, role) VALUES ($1, $2, $3)`,
			clerkID, clerkID+"@example.test", role); err != nil {
			t.Fatalf("seeding %s: %v", clerkID, err)
		}
	}

	seed("user_roster_agent", domain.RoleAgent)
	seed("user_roster_admin", domain.RoleAdmin)
	seed("user_roster_customer", domain.RoleCustomer)

	found, err := repo.Assignable(ctx)
	if err != nil {
		t.Fatalf("Assignable: %v", err)
	}

	byClerkID := map[string]domain.Role{}
	for _, u := range found {
		byClerkID[u.ClerkUserID] = u.Role
	}

	if _, ok := byClerkID["user_roster_agent"]; !ok {
		t.Error("the agent is missing from the roster")
	}
	if _, ok := byClerkID["user_roster_admin"]; !ok {
		t.Error("the admin is missing from the roster")
	}
	if _, ok := byClerkID["user_roster_customer"]; ok {
		t.Error("a customer is on the roster — the predicate is not doing its job")
	}
}

// Ordered by name so a select renders the same way twice, and NULLS LAST
// because Clerk holds no name for someone who signed up with an email and a
// password — an unnamed colleague belongs at the end of a list rather than the
// top of it.
func TestTheRosterIsOrderedByNameWithUnnamedLast(t *testing.T) {
	ctx, tx, repo := begin(t)

	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE role IN ('agent','admin')`); err != nil {
		t.Fatalf("clearing the roster: %v", err)
	}

	seed := func(clerkID string, name *string) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (clerk_user_id, email, name, role) VALUES ($1, $2, $3, 'agent')`,
			clerkID, clerkID+"@example.test", name); err != nil {
			t.Fatalf("seeding %s: %v", clerkID, err)
		}
	}

	zoe, ada := "Zoe", "Ada"
	seed("user_order_unnamed", nil)
	seed("user_order_zoe", &zoe)
	seed("user_order_ada", &ada)

	found, err := repo.Assignable(ctx)
	if err != nil {
		t.Fatalf("Assignable: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("roster has %d entries, want 3", len(found))
	}

	if found[0].Name != "Ada" || found[1].Name != "Zoe" {
		t.Errorf("order = %q, %q — want Ada then Zoe", found[0].Name, found[1].Name)
	}
	if found[2].Name != "" {
		t.Errorf("the unnamed agent is at position 3 with name %q, want it last and empty", found[2].Name)
	}
}

// An empty roster is an empty slice, not an error. A deploy with no agents
// configured is the default state (T17).
func TestAnEmptyRosterIsNotAnError(t *testing.T) {
	ctx, tx, repo := begin(t)

	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE role IN ('agent','admin')`); err != nil {
		t.Fatalf("clearing the roster: %v", err)
	}

	found, err := repo.Assignable(ctx)
	if err != nil {
		t.Fatalf("Assignable: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("roster has %d entries, want none", len(found))
	}
}
