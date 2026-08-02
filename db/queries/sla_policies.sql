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
