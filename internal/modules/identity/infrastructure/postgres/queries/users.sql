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

-- name: GetUserByID :one
-- Reads a user by our own primary key rather than by their Clerk subject.
--
-- Added in slice 2 for the assignee check: the ticket module holds a uuid that
-- came out of its own assignee_id column, and has never seen a Clerk id.
SELECT * FROM users WHERE id = $1;

-- name: ListAssignableUsers :many
-- The people a ticket may be assigned to: agents and admins, never customers.
--
-- Added in slice 2 for the assignment control. It is the first query in this
-- project that hands another user's id to a caller, and that is a departure
-- worth naming: T14b dropped actor_id from the history DTO and T19 sends
-- requester_name rather than requester_id, both to avoid handing out
-- identifiers to enumerate.
--
-- The departure is unavoidable rather than careless. PATCH .../assignee takes
-- an id, so a UI that lets one agent hand a ticket to another has to know it.
-- What limits the exposure is the shape of the answer: it is the staff roster,
-- not the user table — customers are excluded by the predicate, not filtered
-- afterwards — and it is reachable only from inside the agent route group, so
-- the people who can read it are the people already in it.
--
-- Ordered by name so the control renders the same way twice, with the unnamed
-- at the end: Clerk holds no name for someone who signed up with an email and a
-- password, and an unnamed colleague belongs after the named ones rather than
-- above them.
--
-- NULLS LAST is Postgres's default for ASC and is written anyway. Measured
-- rather than assumed — a mutation that removed it changed nothing, which is
-- how it was noticed. It stays because the default flips to NULLS FIRST the
-- moment somebody writes DESC, and a silent reordering of the roster is not
-- worth the two words saved.
SELECT * FROM users
WHERE role IN ('agent', 'admin')
ORDER BY name NULLS LAST, email;
