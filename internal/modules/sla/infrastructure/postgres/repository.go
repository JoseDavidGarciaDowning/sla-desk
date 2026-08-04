// Package postgres stores and reads SLA policies.
//
// It is the only package in this module that knows sla_policies is a table.
// Everything above it receives domain.Policy values, so widening the table or
// changing a column type stops here.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/sla/infrastructure/postgres/sladb"
)

// ErrUnsupportedSchedule means a policy names a schedule the domain does not
// implement.
//
// The CHECK constraint on schedule_mode should make this unreachable; it exists
// so that widening the constraint without shipping the schedule fails loudly
// instead of computing a wrong deadline.
var ErrUnsupportedSchedule = errors.New("sla: policy uses a schedule that is not implemented")

// PolicyRepository reads sla_policies.
type PolicyRepository struct {
	q *sladb.Queries
}

var _ application.PolicyRepository = (*PolicyRepository)(nil)

// NewPolicyRepository takes a DBTX rather than a pool so the caller decides
// whether these reads run on the pool or inside a transaction it already owns.
func NewPolicyRepository(db sladb.DBTX) *PolicyRepository {
	return &PolicyRepository{q: sladb.New(db)}
}

func (r *PolicyRepository) ActiveByPriority(ctx context.Context, p domain.Priority) (domain.Policy, error) {
	row, err := r.q.GetActiveSLAPolicyByPriority(ctx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Policy{}, fmt.Errorf("%w: %s", application.ErrNoPolicyForPriority, p)
		}
		return domain.Policy{}, fmt.Errorf("reading the active policy for %s: %w", p, err)
	}
	return policyFrom(row)
}

func (r *PolicyRepository) ByID(ctx context.Context, id int64) (domain.Policy, error) {
	row, err := r.q.GetSLAPolicyByID(ctx, id)
	if err != nil {
		return domain.Policy{}, fmt.Errorf("reading policy %d: %w", id, err)
	}
	return policyFrom(row)
}

// policyFrom turns a stored policy into the domain one.
//
// The schedule_mode column is a name; the Schedule is behaviour. Resolving one
// into the other is exactly the translation an infrastructure layer exists for,
// and it is why the domain never has to know that a row can say something it
// cannot honour.
func policyFrom(row sladb.SlaPolicy) (domain.Policy, error) {
	var schedule domain.Schedule
	switch row.ScheduleMode {
	case "24x7":
		schedule = domain.Always24x7{}
	default:
		return domain.Policy{}, fmt.Errorf("%w: %q", ErrUnsupportedSchedule, row.ScheduleMode)
	}

	return domain.Policy{
		ID:       row.ID,
		Priority: row.Priority,
		Budget:   time.Duration(row.BudgetMinutes) * time.Minute,
		Schedule: schedule,
	}, nil
}
