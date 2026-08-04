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
-- One page of the caller's tickets, newest first.
--
-- Keyset pagination, not OFFSET. OFFSET counts rows from the start every time,
-- so a ticket created while someone is paging shifts everything down and they
-- see a row twice — and the cost grows with the offset. Carrying the last row's
-- sort key instead makes each page independent of what happened to the ones
-- before it, and the predicate rides the (requester_id, created_at DESC) index.
--
-- The key is (created_at, id), not created_at alone: two tickets created in the
-- same transaction share a timestamp, and a cursor on a non-unique key can
-- either skip rows or repeat them.
--
-- status and priority are optional filters, applied here rather than in the
-- browser. Filtering a loaded page on the client filters only that page, so
-- with pagination it lies: "you have 3 open tickets" because the other seven
-- were on page two. One query rather than four, so the cursor means the same
-- thing under every combination of filters.
--
-- These two parameters come out as *string rather than as *ticket.Status and
-- *ticket.Priority, and that is the least bad of three measured options. Every
-- other column in this file carries its domain type; these do not, because
-- sqlc will give a nullable parameter either the right type or the right
-- nullability, not both:
--
--   sqlc.narg(status)::text              -> *string          (this one)
--   status = COALESCE(sqlc.narg(status), status)
--                                        -> ticket.Status, NOT a pointer.
--                                           An absent filter is then the zero
--                                           value, pgx sends '' rather than
--                                           NULL, COALESCE('', status) is '',
--                                           and asking for no filter returns
--                                           no rows. Silently.
--   ...COALESCE(NULLIF(narg, ''), col)   -> interface{}. Inference gives up.
--
-- A pointer says "absent" and cannot be confused with a value. Losing the
-- domain type on two parameters costs one conversion in the handler, which the
-- handler has to do anyway: it validates the incoming query string against the
-- vocabulary before it gets here.
SELECT * FROM tickets
WHERE requester_id = @requester_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(priority)::text IS NULL OR priority = sqlc.narg(priority)::text)
  AND (
    sqlc.narg(after_created_at)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT @page_size;

-- name: GetTicketForUpdate :one
-- Reads a ticket and holds it for the rest of the transaction.
--
-- FOR UPDATE, not a plain read. Two transitions arriving at once — an agent
-- resolving while the customer replies — would otherwise both read the same
-- current status, both compute a clock from it, and the second commit would
-- overwrite the first with a cache that never accounted for it. The lock makes
-- them queue.
--
-- Not scoped by requester: an agent transitions tickets that are not theirs.
-- Authorisation for that lives in the RBAC matrix, not in this query.
SELECT * FROM tickets
WHERE id = $1
FOR UPDATE;

-- name: UpdateTicketClock :one
-- Writes the derived cache. Step 4 of the order fixed by docs/adr/0001, and it
-- runs only after the history row exists and the clock has been rebuilt from
-- the history including it.
--
-- The two CHECK constraints on tickets mean this cannot store an incoherent
-- pair: a status other than open with a running clock, or a clock without a
-- deadline, is rejected by the database rather than trusted to the caller.
UPDATE tickets
SET status               = @status,
    sla_consumed_micros  = @sla_consumed_micros,
    sla_clock_started_at = @sla_clock_started_at,
    sla_due_at           = @sla_due_at,
    updated_at           = @updated_at
WHERE id = @id
RETURNING *;

-- name: GetTicketSLAPolicyID :one
-- The policy a ticket was created under, and nothing else.
--
-- It exists so the SLA policy can be resolved *before* the transition
-- transaction opens. Calling another module from inside a transaction would
-- hold a second pooled connection for its duration, and pgxpool defaults to
-- max(4, NumCPU) — enough concurrent transitions would then deadlock waiting on
-- each other. See docs/adr/0006.
--
-- Unlocked, and that is safe rather than sloppy: sla_policy_id is written once
-- by CreateTicket and no query updates it. The locked read inside the
-- transaction asserts the value still matches rather than trusting this one.
SELECT sla_policy_id FROM tickets
WHERE id = $1;
