-- name: InsertTicketStatusHistory :one
-- Written in the same transaction as the ticket update it describes. This table
-- is the fact the SLA clock is rebuilt from; the sla_* columns on tickets are a
-- cache of it (docs/spec.md §4.2).
--
-- from_status is NULL only on the row that records creation.
--
-- created_at is a parameter rather than the column default, and that is what
-- makes the SLA clock reconstructible. The clock is rebuilt by measuring
-- intervals between these timestamps, and the sla_* columns on tickets cache
-- the result. If the cache were computed from one instant and this row stamped
-- with another, the two would disagree by that difference and the consistency
-- test in docs/spec.md §9 could never hold. The caller reads now() once per
-- transaction and passes the same value to both.
INSERT INTO ticket_status_history (
    ticket_id,
    from_status,
    to_status,
    actor_id,
    actor_role,
    reason,
    created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListTicketStatusHistory :many
-- The input to sla.Reconstruct, and the ticket timeline in the UI.
--
-- Ordered by id as well as created_at so that two rows sharing a timestamp
-- still come back in a fixed order. Reconstruction walks this sequence, so an
-- unstable order would make the clock depend on how Postgres felt about the
-- tie.
SELECT * FROM ticket_status_history
WHERE ticket_id = $1
ORDER BY created_at, id;
