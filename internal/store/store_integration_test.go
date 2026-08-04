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

func ptr[T any](v T) *T { return &v }
