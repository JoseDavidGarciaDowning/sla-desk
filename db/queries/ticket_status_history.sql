-- name: InsertTicketStatusHistory :one
-- Written in the same transaction as the ticket update it describes. This table
-- is the fact the SLA clock is rebuilt from; the sla_* columns on tickets are a
-- cache of it (docs/spec.md §4.2).
--
-- from_status is NULL only on the row that records creation.
INSERT INTO ticket_status_history (
    ticket_id,
    from_status,
    to_status,
    actor_id,
    actor_role,
    reason
)
VALUES ($1, $2, $3, $4, $5, $6)
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
