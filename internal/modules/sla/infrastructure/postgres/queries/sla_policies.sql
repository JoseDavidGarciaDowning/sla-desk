-- name: GetActiveSLAPolicyByPriority :one
-- Resolves the policy for a new ticket. The partial unique index on
-- (priority) WHERE active guarantees this returns at most one row, so :one is
-- a claim the database enforces rather than an assumption.
SELECT * FROM sla_policies
WHERE priority = $1
  AND active;

-- name: ListActiveSLAPolicies :many
-- Ordered by budget so the result reads from most to least urgent. Sorting by
-- the priority label would be alphabetical, which is meaningless here.
SELECT * FROM sla_policies
WHERE active
ORDER BY budget_minutes;

-- name: GetSLAPolicyByID :one
-- The policy a ticket was snapshotted with. Looked up by id rather than by
-- priority so that editing a policy does not retroactively move the deadlines
-- of tickets created under the old budget.
SELECT * FROM sla_policies
WHERE id = $1;
