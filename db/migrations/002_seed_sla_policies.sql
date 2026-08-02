-- +goose Up

-- The four default budgets from docs/spec.md §4.2. This is reference data the
-- application depends on, not a test fixture: creating a ticket resolves a
-- policy from its priority, so a missing row here means tickets cannot be
-- created at all.
--
-- Seeded as data precisely so it can be edited as data. Changing a budget is an
-- UPDATE, never a code change.
INSERT INTO sla_policies (name, priority, budget_minutes) VALUES
    ('Urgent - 1 hour',   'urgent',    60),
    ('High - 4 hours',    'high',     240),
    ('Normal - 24 hours', 'normal',  1440),
    ('Low - 72 hours',    'low',     4320);

-- +goose Down

-- Deletes exactly the four rows this migration inserted, identified by name.
-- Deleting by priority would also remove policies created afterwards.
DELETE FROM sla_policies
WHERE name IN (
    'Urgent - 1 hour',
    'High - 4 hours',
    'Normal - 24 hours',
    'Low - 72 hours'
);
