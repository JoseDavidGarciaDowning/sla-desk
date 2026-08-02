-- name: UpsertUserFromClerk :one
-- The idempotent provisioning path from docs/spec.md §4.5. Both the Clerk
-- webhook and the lazy fallback in RequireAuth call this, so whichever request
-- arrives first creates the row and the other one is a harmless update. That is
-- what removes the signup race entirely.
--
-- The role is written as a literal on insert and is deliberately absent from the
-- update clause. A webhook payload must never be able to promote a user, and
-- re-running this on an existing agent must not demote them back to customer.
INSERT INTO users (clerk_user_id, email, name, role)
VALUES ($1, $2, $3, 'customer')
ON CONFLICT (clerk_user_id) DO UPDATE
SET email      = EXCLUDED.email,
    name       = EXCLUDED.name,
    updated_at = now()
RETURNING *;

-- name: GetUserByClerkID :one
SELECT * FROM users
WHERE clerk_user_id = $1;
