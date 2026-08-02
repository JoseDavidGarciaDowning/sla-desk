-- name: CreateTicket :one
-- A new ticket is always open, so the clock is always running and the two
-- CHECK constraints require both clock columns to be set. They are parameters
-- rather than expressions because every deadline in this system is computed by
-- internal/sla and nowhere else (docs/spec.md §4.2).
--
-- The caller must write the matching ticket_status_history row (NULL -> open)
-- in the same transaction. No history row, no transition — §4.1.
INSERT INTO tickets (
    requester_id,
    title,
    description,
    category,
    priority,
    sla_policy_id,
    status,
    sla_clock_started_at,
    sla_due_at
)
VALUES ($1, $2, $3, $4, $5, $6, 'open', $7, $8)
RETURNING *;

-- name: GetTicketForRequester :one
-- The requester predicate lives in the SQL, not in the handler. A forgotten
-- check in Go must not be enough to leak another customer's ticket
-- (docs/spec.md §4.3). Returning no rows rather than a row the caller must then
-- reject is also what lets the API answer 404 instead of 403, which would
-- confirm the ticket exists.
SELECT * FROM tickets
WHERE id = $1
  AND requester_id = $2;

-- name: ListTicketsByRequester :many
-- Matches the (requester_id, created_at DESC) index exactly.
SELECT * FROM tickets
WHERE requester_id = $1
ORDER BY created_at DESC;
