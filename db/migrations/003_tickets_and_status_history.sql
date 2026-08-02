-- +goose Up

CREATE TABLE tickets (
    -- UUID because ticket ids appear in URLs. Sequential ids would let anyone
    -- probe for other customers' tickets and read the business's volume off the
    -- counter.
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- No ON DELETE clause anywhere in this file. Deleting a user who has
    -- tickets, or a policy a ticket points at, must fail loudly rather than
    -- take rows with it: docs/spec.md §10 forbids hard-deleting a ticket or a
    -- history row, and a cascade would do exactly that from a distance.
    requester_id UUID NOT NULL REFERENCES users (id),
    assignee_id  UUID REFERENCES users (id),

    title TEXT NOT NULL
        CONSTRAINT tickets_title_length CHECK (length(title) BETWEEN 1 AND 200),

    description TEXT NOT NULL
        CONSTRAINT tickets_description_length CHECK (length(description) BETWEEN 1 AND 10000),

    category TEXT NOT NULL
        CONSTRAINT tickets_category_valid
        CHECK (category IN ('billing', 'technical', 'account', 'other')),

    priority TEXT NOT NULL
        CONSTRAINT tickets_priority_valid
        CHECK (priority IN ('urgent', 'high', 'normal', 'low')),

    status TEXT NOT NULL DEFAULT 'open'
        CONSTRAINT tickets_status_valid
        CHECK (status IN ('open', 'pending', 'resolved', 'closed')),

    -- Snapshotted at creation. Editing a policy must not silently move the
    -- deadlines of tickets that were created under the old budget.
    sla_policy_id BIGINT NOT NULL REFERENCES sla_policies (id),

    -- Microseconds, not minutes. TIMESTAMPTZ resolves to one microsecond, so
    -- every duration the reconstruction can produce is a whole number of them
    -- and this round-trips exactly. Minutes would force the mandatory
    -- consistency test in docs/spec.md §9 to carry a tolerance, which would
    -- blind it to the drift it exists to catch. Reasoning in §4.2.
    sla_consumed_micros BIGINT NOT NULL DEFAULT 0
        CONSTRAINT tickets_sla_consumed_not_negative CHECK (sla_consumed_micros >= 0),

    sla_clock_started_at TIMESTAMPTZ,
    sla_due_at           TIMESTAMPTZ,
    sla_breached_at      TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The two invariants of docs/spec.md §4.2, enforced by the database rather
    -- than trusted to handlers. "A paused ticket cannot breach" is then a
    -- property of the schema: no status other than open can carry a due date,
    -- and the breach worker's predicate can never match one.
    CONSTRAINT tickets_clock_runs_only_while_open
        CHECK ((status = 'open') = (sla_clock_started_at IS NOT NULL)),

    CONSTRAINT tickets_due_at_set_iff_clock_running
        CHECK ((sla_clock_started_at IS NULL) = (sla_due_at IS NULL))
);

CREATE TABLE ticket_status_history (
    -- Identity rather than UUID: history rows never appear in a URL, and a
    -- monotonic id gives reconstruction a deterministic tiebreaker when two
    -- rows share a timestamp.
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    ticket_id UUID NOT NULL REFERENCES tickets (id),

    -- NULL on the row that records creation: there is no status to come from.
    from_status TEXT
        CONSTRAINT ticket_status_history_from_status_valid
        CHECK (from_status IN ('open', 'pending', 'resolved', 'closed')),

    to_status TEXT NOT NULL
        CONSTRAINT ticket_status_history_to_status_valid
        CHECK (to_status IN ('open', 'pending', 'resolved', 'closed')),

    -- A transition to the status the ticket is already in is a no-op, and
    -- recording it would inflate the history that the SLA clock is rebuilt
    -- from. IS DISTINCT FROM rather than <> so the NULL creation row passes.
    CONSTRAINT ticket_status_history_is_a_real_transition
        CHECK (from_status IS DISTINCT FROM to_status),

    actor_id UUID NOT NULL REFERENCES users (id),

    -- Denormalised on purpose. This is the role the actor held at the time, not
    -- the role they hold now; promoting someone must not rewrite what the audit
    -- trail says they were when they acted.
    actor_role TEXT NOT NULL
        CONSTRAINT ticket_status_history_actor_role_valid
        CHECK (actor_role IN ('customer', 'agent', 'admin')),

    reason TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The breach worker's only predicate (docs/spec.md §4.2). Partial, so the index
-- holds only tickets that can still breach and shrinks as they are marked.
CREATE INDEX tickets_breach_candidates
    ON tickets (sla_due_at)
    WHERE sla_breached_at IS NULL;

-- The customer portal list: one requester, newest first.
CREATE INDEX tickets_requester_recent
    ON tickets (requester_id, created_at DESC);

-- The agent dashboard: one assignee, filtered by status.
CREATE INDEX tickets_assignee_status
    ON tickets (assignee_id, status);

-- Clock reconstruction reads one ticket's history in order.
CREATE INDEX ticket_status_history_by_ticket
    ON ticket_status_history (ticket_id, created_at);

-- +goose Down

DROP TABLE ticket_status_history;
DROP TABLE tickets;
