//go:build integration

// Package pgtest connects integration tests to a migrated Postgres.
//
// It is platform code by the same rule as everything else here: what it knows
// is how to reach a database and how to read a constraint violation, and it
// knows nothing about tickets, users or SLAs. Every module's integration tests
// need exactly this, and none of them need each other's.
//
// Behind the `integration` build tag, so it does not exist in an ordinary build
// and cannot be reached from production code by accident.
package pgtest

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const timeout = 10 * time.Second

// Begin opens a transaction that is always rolled back. Every test therefore
// sees the seeded database and leaves it exactly as it found it: no cleanup
// code to forget, and no ordering dependency between tests.
//
// It hands back the raw transaction because the constraint tests need to
// attempt writes no generated query would ever produce — that is the point of
// them.
func Begin(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()

	ctx := Context(t)

	conn, err := pgx.Connect(ctx, url(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	return ctx, tx
}

// Pool opens a real connection pool, for the tests that need writes to commit —
// anything exercising a repository that manages its own transaction, or
// concurrency across connections.
//
// Tests using it are responsible for their own cleanup: there is no enclosing
// transaction to roll back.
func Pool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx := Context(t)

	pool, err := pgxpool.New(ctx, url(t))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return ctx, pool
}

// Context returns a context bounded by the suite timeout.
func Context(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)

	return ctx
}

// url reads DATABASE_URL, skipping the test when it is unset.
//
// The skip is why `make test-int` refuses to run without the variable: every
// integration test would call this, skip, and `go test` would still print ok
// for a run that asserted nothing.
func url(t *testing.T) string {
	t.Helper()

	u := os.Getenv("DATABASE_URL")
	if u == "" {
		t.Skip("DATABASE_URL is not set — run `make up` then `make test-int`")
	}

	return u
}

// RejectedBy returns the name of the constraint that refused the write.
//
// Asserting on the name rather than on "some error happened" is what keeps
// these tests specific: a statement that fails for an unrelated reason — a typo
// in the SQL, a missing column — does not quietly pass.
func RejectedBy(t *testing.T, err error) string {
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
