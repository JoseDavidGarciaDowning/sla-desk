// Package postgres is the SLA module's database adapter.
//
// It wraps the generated queries so nothing above it names a driver type, and
// it is where a stored row becomes a domain policy — including the one piece of
// interpretation that involves: turning a schedule_mode string into the
// Schedule the arithmetic runs on.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/features/resolve"
	sladb "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres/generated"
)

// ErrUnsupportedScheduleMode means a row names a schedule this build cannot
// compute with.
//
// It is not a validation failure — the CHECK constraint already restricts the
// column — it is the case where the database has been migrated ahead of the
// binary. Failing loudly beats computing a deadline with the wrong schedule,
// which would look entirely plausible.
var ErrUnsupportedScheduleMode = errors.New("sla: unsupported schedule mode")

// PolicyRepository reads the sla_policies table.
type PolicyRepository struct {
	q *sladb.Queries
}

var _ resolve.Policies = (*PolicyRepository)(nil)

// NewPolicyRepository builds the repository against a database handle.
//
// The handle is a DBTX rather than a pool: these are reads of reference data,
// and the caller decides whether they run on the pool or inside a transaction
// it already owns.
func NewPolicyRepository(db sladb.DBTX) *PolicyRepository {
	return &PolicyRepository{q: sladb.New(db)}
}

// ActiveByPriority translates pgx.ErrNoRows into the module's own sentinel.
//
// Without it the caller would have to match on a driver error to tell "our seed
// data is wrong" apart from "the database is down" — and those are answered
// with different status codes.
func (r *PolicyRepository) ActiveByPriority(ctx context.Context, p domain.Priority) (domain.Policy, error) {
	row, err := r.q.GetActiveSLAPolicyByPriority(ctx, p)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Policy{}, fmt.Errorf("%w: %s", resolve.ErrNoPolicyForPriority, p)
	case err != nil:
		return domain.Policy{}, err
	}
	return policyFrom(row)
}

func (r *PolicyRepository) ByID(ctx context.Context, id int64) (domain.Policy, error) {
	row, err := r.q.GetSLAPolicyByID(ctx, id)
	if err != nil {
		return domain.Policy{}, err
	}
	return policyFrom(row)
}

// policyFrom turns a stored policy into the domain one.
//
// The schedule is the only real interpretation here, and it belongs on this
// side of the boundary: the domain works with a Schedule interface and must not
// know that a column somewhere spells one of them "24x7".
func policyFrom(row sladb.SlaPolicy) (domain.Policy, error) {
	var schedule domain.Schedule
	switch row.ScheduleMode {
	case "24x7":
		schedule = domain.Always24x7{}
	default:
		return domain.Policy{}, fmt.Errorf("%w: %q", ErrUnsupportedScheduleMode, row.ScheduleMode)
	}

	return domain.Policy{
		ID:       row.ID,
		Priority: row.Priority,
		Budget:   time.Duration(row.BudgetMinutes) * time.Minute,
		Schedule: schedule,
	}, nil
}
