// Package postgres is the ticket module's database adapter.
//
// It owns where transactions begin and end, and it is the only place the
// generated row types are named. Everything above works in domain values.
//
// One type, split across files by the use case each method serves, and that
// split is deliberate: a file boundary is not a type boundary. Every feature
// here shares a connection pool, a transaction discipline and a set of
// mappers, so one adapter satisfying several narrow ports is the honest shape.
// Nine adapters would be nine copies of the transaction handling docs/adr/0008
// depends on.
//
// Each file names the feature whose port it implements — which is what makes
// the dependency look backwards until you read docs/adr/0012. It is not: an
// adapter depending on the port it implements is the arrow pointing inward.
package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	ticketdb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/infrastructure/postgres/generated"
)

// ErrPolicyChangedUnderUs means a ticket's sla_policy_id was not what it was a
// moment ago.
//
// The SLA clock is resolved before this transaction opens, using a policy id
// read without a lock. Nothing updates that column, so this should be
// unreachable — which is exactly why it is checked rather than assumed. A
// deadline computed from the wrong policy looks entirely plausible.
var ErrPolicyChangedUnderUs = errors.New("ticket: the SLA policy changed while the clock was being resolved")

// Repository holds the writes that span more than one statement.
//
// The generated queries take a DBTX, so they work inside a transaction; what
// they cannot do is decide where a transaction begins and ends. That is this
// type's only job.
type Repository struct {
	pool *pgxpool.Pool
	q    *ticketdb.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: ticketdb.New(pool)}
}
