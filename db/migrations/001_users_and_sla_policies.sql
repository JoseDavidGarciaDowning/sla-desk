-- +goose Up

-- Users provisioned from Clerk. Clerk answers who you are; the role column here
-- answers what you may do (docs/spec.md §4.3).
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Our identity key. The upsert in §4.5 conflicts on this column, so the
    -- webhook and the lazy fallback converge on one row no matter which arrives
    -- first.
    clerk_user_id TEXT NOT NULL UNIQUE,

    -- Deliberately NOT unique. Two Clerk identities can carry the same address
    -- when a user signs up through more than one provider without linking them.
    -- A unique index here would make the upsert fail on a path we do not
    -- control.
    email TEXT NOT NULL,

    -- Clerk does not guarantee a name.
    name TEXT,

    -- The default exists as a second line of defence. Every write path sets the
    -- role explicitly, but if one ever forgets, the row lands on the least
    -- privileged role rather than on NULL or an error.
    role TEXT NOT NULL DEFAULT 'customer'
        CONSTRAINT users_role_valid
        CHECK (role IN ('customer', 'agent', 'admin')),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Maintained by the queries, not by a trigger. There is exactly one write
    -- path for this table (the upsert), so an explicit `updated_at = now()` is
    -- visible in the SQL instead of hidden in a trigger. Add the trigger if the
    -- write paths ever multiply.
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- SLA budgets. This table is the reason no priority budget is hardcoded
-- anywhere in Go (docs/spec.md §4.2, and §10 "Never").
CREATE TABLE sla_policies (
    -- Not a UUID. Policies are internal reference data and never appear in a
    -- URL, so there is nothing to enumerate. internal/sla.Policy already
    -- declares ID as int64.
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    name TEXT NOT NULL,

    priority TEXT NOT NULL
        CONSTRAINT sla_policies_priority_valid
        CHECK (priority IN ('urgent', 'high', 'normal', 'low')),

    budget_minutes INTEGER NOT NULL
        CONSTRAINT sla_policies_budget_positive
        CHECK (budget_minutes > 0),

    -- Only one legal value today, on purpose. internal/sla ships Always24x7 and
    -- nothing else, so a row the code cannot interpret must not be creatable.
    -- Adding 'business_hours' is one migration, shipped with the code that
    -- implements it.
    schedule_mode TEXT NOT NULL DEFAULT '24x7'
        CONSTRAINT sla_policies_schedule_mode_valid
        CHECK (schedule_mode IN ('24x7')),

    active BOOLEAN NOT NULL DEFAULT TRUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Resolving a policy from a priority must return exactly one row. Without this
-- index two active policies could share a priority and the resolution would
-- depend on row order — a bug that only shows up in production, under load,
-- after someone edits a policy.
--
-- Partial, so deactivated policies stay in the table as history.
CREATE UNIQUE INDEX sla_policies_one_active_per_priority
    ON sla_policies (priority)
    WHERE active;

-- +goose Down

DROP TABLE sla_policies;
DROP TABLE users;
