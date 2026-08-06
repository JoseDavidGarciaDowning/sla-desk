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

-- name: GetTicketPolicyID :one
-- The policy a ticket was created under, read without locking anything.
--
-- It exists so the SLA clock can be resolved before Transition opens its
-- transaction, which is what keeps another module's I/O out of ours. The read
-- is safe unlocked because sla_policy_id is written once by CreateTicket and no
-- query updates it — and Transition does not take that on trust: it compares
-- this against the locked row before writing.
SELECT sla_policy_id FROM tickets
WHERE id = $1;

-- name: ListTicketsForQueue :many
-- One page of EVERY ticket, in deadline order. The agent queue.
--
-- A separate query rather than ListTicketsByRequester with the predicate made
-- conditional, and that is the decision slice 2 exists to make
-- (tasks/slice-2/plan.md decision B). Passing a role in and skipping
-- WHERE requester_id when it says 'agent' would put a boolean in charge of a
-- security predicate — and T14a's mutation testing already caught that exact
-- shape once, when a query was scoped for the case a test happened to run and
-- open for the one it did not. The customer's query keeps its predicate, takes
-- no new parameter, and cannot be talked into returning someone else's ticket.
--
-- What replaces the predicate is where this query may be called from: only
-- handlers mounted under /api/agent, behind RequireRole. Nothing here enforces
-- anything, and that is stated rather than hidden.
--
-- ORDER BY the deadline, not created_at. A queue is read to find what breaches
-- next; ordering by age makes the agent hunt for it, and sorting the loaded
-- page in the browser sorts one page of many — the same lie client-side
-- filtering told in T14a.
--
-- COALESCE(sla_due_at, sentinel) rather than NULLS LAST, because the cursor has
-- to compare against the same expression and (NULL, id) > (x, id) is NULL, not
-- false. The sentinel is 9999-12-31T23:59:59.999999Z rather than 'infinity'
-- because the generated cursor parameter is *time.Time, which cannot hold one.
-- See db/migrations/004. The matching expression index is what makes this an
-- Index Cond rather than a Filter.
--
-- The cursor is (deadline, id) and not (created_at, id): a keyset cursor IS a
-- position in the sort order, so it must carry the key being sorted on.
-- Carrying a different one asks the database for "what comes after the row
-- created at 10:00" in a list ordered by deadline, which means nothing.
--
-- Known and accepted: sla_due_at is mutable — pausing a ticket clears it and
-- resuming recomputes it — so a ticket transitioned while someone is paging can
-- move across the cursor and be seen twice or missed. Only the row that moved
-- is affected, which is the one an agent just acted on. OFFSET would shift
-- every row after it instead, on any insert as well.
--
-- assignee is three questions, not one: any assignee, a specific one, or none.
-- @assignee_filter distinguishes them because a NULL parameter already means
-- "no filter" and cannot also mean "unassigned".
SELECT t.*, u.name AS requester_name, u.email AS requester_email
FROM tickets t
JOIN users u ON u.id = t.requester_id
WHERE (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.narg(priority)::text IS NULL OR t.priority = sqlc.narg(priority)::text)
  AND (
    @assignee_filter::text = 'any'
    OR (@assignee_filter::text = 'unassigned' AND t.assignee_id IS NULL)
    OR (@assignee_filter::text = 'one' AND t.assignee_id = sqlc.narg(assignee_id)::uuid)
  )
  AND (
    sqlc.narg(after_due_at)::timestamptz IS NULL
    OR (COALESCE(t.sla_due_at, '9999-12-31 23:59:59.999999+00'::timestamptz), t.id) >
       (sqlc.narg(after_due_at)::timestamptz, sqlc.narg(after_id)::uuid)
  )
ORDER BY COALESCE(t.sla_due_at, '9999-12-31 23:59:59.999999+00'::timestamptz), t.id
LIMIT @page_size;

-- name: GetTicketByID :one
-- One ticket, for a caller who is not its requester.
--
-- The counterpart to GetTicketForRequester, and a separate query rather than
-- that one with the predicate made conditional — the same decision as the queue
-- (tasks/slice-2/plan.md decision B). The scoped one keeps its predicate and
-- takes no new parameter, so it cannot be talked into returning someone else's
-- ticket by any argument.
--
-- Reachable only from a handler mounted behind RequireRole. Nothing here
-- enforces that, which is stated rather than hidden.
--
-- No rows still means 404 at the boundary. The agent group answers 403 to a
-- customer because there is no id in its path to confirm; an id that names no
-- ticket is a different question, and §11's rule applies to it unchanged.
SELECT * FROM tickets WHERE id = $1;
