-- name: UpsertUserFromClerk :one
-- The idempotent provisioning path from docs/spec.md §4.5. Both the Clerk
-- webhook and the lazy fallback in RequireAuth call this, so whichever request
-- arrives first creates the row and the other one is a harmless update. That is
-- what removes the signup race entirely.
--
-- The role is a parameter rather than a literal since slice 2, and the only
-- thing that fills it is our own configuration — never a webhook payload and
-- never a token claim, neither of which reaches this far (§4.5).
--
-- It stays absent from the update clause, which is the guarantee it always was:
-- re-running this to refresh someone's name must not be able to change what
-- they are allowed to do, in either direction. Promotion is GrantUserRole's job.
INSERT INTO users (clerk_user_id, email, name, role)
VALUES ($1, $2, $3, @role)
ON CONFLICT (clerk_user_id) DO UPDATE
SET email      = EXCLUDED.email,
    name       = EXCLUDED.name,
    updated_at = now()
RETURNING *;

-- name: GrantUserRole :one
-- Raises an existing user to a role the operator granted them in configuration.
--
-- The predicate is the whole point: this statement cannot write 'customer', so
-- there is no argument that turns it into a demotion. Removing an id from the
-- grant list — or mistyping one — therefore cannot strip an agent's role on
-- their next request, which would otherwise happen silently and mid-shift.
--
-- Enforced here rather than in the caller because a rule that lives in an if
-- statement is a rule the next caller can forget to write. Taking a role away
-- is an explicit action and belongs to the admin surface in slice 9.
--
-- Returns no rows when the role would be a demotion, which the repository
-- reports as ErrNoSuchUser: nothing was written, and the caller must not be
-- handed a row suggesting otherwise.
UPDATE users
   SET role       = @role,
       updated_at = now()
 WHERE clerk_user_id = @clerk_user_id
   AND @role::text <> 'customer'
RETURNING *;

-- name: GetUserByClerkID :one
SELECT * FROM users
WHERE clerk_user_id = $1;
