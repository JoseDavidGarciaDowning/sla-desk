-- +goose Up

-- The agent queue reads in deadline order: what breaches next, first. That is
-- the question the queue exists to answer, and ordering it any other way makes
-- an agent hunt for it (docs/spec.md §2, slice 2).
--
-- The expression is COALESCE(sla_due_at, sentinel) rather than NULLS LAST,
-- and the reason is the cursor rather than the ordering. A paused ticket has no
-- deadline (§4.2), and keyset pagination compares the last row's sort key:
--
--   (NULL, id) > ('2026-01-01', id)                      -> NULL
--   (COALESCE(NULL, sentinel), id) > ('2026-01-01', id)  -> true
--
-- WHERE NULL discards the row, so with NULLS LAST every paused ticket would
-- sort correctly and then vanish from the second page onward — silently. Making
-- the value total is what keeps the ORDER BY and the WHERE agreeing.
--
-- The sentinel is 9999-12-31T23:59:59.999999Z rather than 'infinity', and that
-- is forced by the Go side rather than chosen. pgx can express infinity, but
-- sqlc generates the cursor parameter as *time.Time and time.Time has no such
-- value — so a cursor pointing at a paused ticket could not be sent. The
-- sentinel is exactly representable in both: timestamptz resolves to one
-- microsecond, and RFC3339Nano round-trips it unchanged.
--
-- It sorts after every real deadline, so paused tickets land at the end where
-- they belong: they cannot breach.
--
-- The index has to match the ORDER BY expression exactly; a plain index on
-- sla_due_at does not serve COALESCE(sla_due_at, ...). Verified against
-- Postgres 16 before writing this: the text -> timestamptz cast is STABLE
-- rather than IMMUTABLE, which would normally be refused in an index, and this
-- is accepted because a literal argument is constant-folded at parse time. The
-- planner then uses it as an Index Cond on the row comparison, not a Filter.
--
-- tickets_breach_candidates is not a substitute. It is partial
-- (WHERE sla_breached_at IS NULL) and indexes the bare column, so it serves the
-- breach worker's range scan and not this ordering.
CREATE INDEX tickets_queue_by_deadline
    ON tickets (COALESCE(sla_due_at, '9999-12-31 23:59:59.999999+00'::timestamptz), id);

-- +goose Down

DROP INDEX tickets_queue_by_deadline;
