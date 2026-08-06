# Todo: Slice 2 — the agent

Plan: [`plan.md`](./plan.md) · Spec: [`docs/spec.md`](../../docs/spec.md) §2, §4.3

Every task is test-first (spec §9, strict TDD). Write the failing test, watch it fail, then
make it pass. Numbering continues from slice 1, which ended at T16.

---

## Phase 1: Becoming an agent

### T17: Role bootstrap from server config ✅

**Description:** Give the system a way for a `users` row to hold `agent` or `admin`. The
list of Clerk subjects that get promoted comes from the process environment and is applied
by the identity module when it writes the row — never from a token, never from a webhook
payload. Resolves spec §12.4.

**Acceptance criteria:**
- [x] `AGENT_CLERK_USER_IDS` and `ADMIN_CLERK_USER_IDS` read by `internal/platform/config`,
      comma-separated, **empty by default** — an unconfigured deploy has no agents
- [x] A subject in a list lands as that role whether the row is written by the Clerk webhook
      or by the lazy upsert in `RequireAuth` — both go through `EnsureUser`, so there is one
      place to change and no way for the two paths to disagree
- [x] An existing `customer` row whose subject is listed is promoted on the next
      `EnsureUser`, not only at creation
- [x] A subject in **both** lists fails the boot with a named error, rather than resolving by
      map iteration order — the error names the subject, so an operator knows which entry to remove
- [x] Removing a subject from the list does **not** demote — **enforced by the SQL**, see below
- [x] Startup logs the **count** in each list, never the ids

**Verification:**
- [x] Integration, against a real Postgres: a granted subject lands as `agent` on insert; an
      existing `customer` is promoted and keeps **the same row id**, proving it was updated
      rather than recreated
- [x] Integration: an agent survives an upsert that passes `customer`, and the rest of that
      update still applies — a conflict clause that protected the role by doing nothing at
      all would have passed the first assertion alone
- [x] Integration: `GrantRole` moves an agent to admin, refuses a demotion, and reports an
      unknown subject as `ErrNoSuchUser`
- [x] Unit: 8 domain cases and 3 config cases — empty, one id, several, whitespace and
      trailing commas, a repeat within one list, the same id in both, and the zero value
- [x] Unit: the service promotes an existing customer, does **not** write when the role
      already matches, and does **not** demote when the list is emptied
- [x] **7 mutations, 7 dead**: `EnsureUser` skipping the grant on an existing row; `applyGrant`
      losing the already-matching guard; `NewRoleGrants` accepting a subject in both lists;
      `splitList` not trimming; `GrantUserRole` without its predicate; `Upsert` writing the
      literal instead of the parameter; the `ON CONFLICT` clause writing the role
- [x] `make check` and `make test-int` clean

**Dependencies:** None
**Files:** `internal/modules/identity/domain/grants.go`, `internal/modules/identity/application/service.go`,
`internal/modules/identity/infrastructure/postgres/queries/users.sql`,
`internal/modules/identity/infrastructure/postgres/repository.go`, `internal/modules/identity/module.go`,
`internal/platform/config/config.go`, `cmd/api/main.go`, `docs/local-development.md`, plus tests
**Scope:** M

**Decisions taken during T17:**

- **The no-demotion rule is a SQL predicate, not an `if`.** The card asked for it to be
  *asserted*; it is enforced instead. `GrantUserRole` carries `AND @role::text <> 'customer'`,
  so there is no argument to the method that takes a role away. A rule that lives in a caller
  is a rule the next caller can forget to write, and this one protects against a typo in an
  environment variable silently stripping an agent mid-shift.
- **Promotion is a second query, not a widened upsert.** The role stays out of the upsert's
  conflict clause, which is the guarantee that existed before slice 2 turned it into a
  parameter. Refreshing someone's name must not be able to change what they may do, in either
  direction, and `Upsert` is the method the webhook calls with whatever Clerk sent.
- **`identity.New` and `NewWith` now return an error.** A subject in both lists has no
  defensible winner, and Go's map iteration order is deliberately random — resolving it
  silently would make a deployed role depend on something no reader can see and no test can
  pin. Same rule as the Clerk webhook secret: a configuration that cannot be obeyed stops the
  process at startup.
- **`applyGrant` has two early returns and neither is an optimisation.** Skipping when the
  role already matches is what stops every request an agent makes from becoming a write —
  `EnsureUser` runs on all of them. Skipping when the grant is `customer` is the no-demotion
  rule restated in Go, so the SQL predicate is never even reached in the ordinary case.
- **`domain.RoleGrants` has a usable zero value.** A caller who forgets to build one gets
  "nobody is privileged" rather than a nil-map panic on the first request.

**Two things this cost, both worth recording:**

- **Widening `UserRepository` broke every stub.** Four test doubles across three packages
  needed the new method. That is the price of a consumer-declared interface and it is the
  right price — the compiler found all of them, and each one had to state what it does when
  asked to grant a role.
- **`Config` stopped being comparable.** Two existing tests asserted `got != Config{}`, which
  no longer compiles once the struct holds a slice. They now use `reflect.DeepEqual`, and the
  assertion they were making is unchanged.

**Left for the human:** `.env.example` needs the two new variables added by hand. A local
permission rule keeps the assistant out of `.env*`, the same rule recorded in T1.

---

> **Checkpoint F — an agent exists in the database and can do nothing new yet.**

---

## Phase 2: Reading every ticket

### T18: `RequireRole` and the `/api/agent` route group ⚠️ the slice's load-bearing task ✅

**Description:** The middleware that turns a role into an authorization decision, and the
route group that is the only place the unscoped queries will ever be reachable from. Ships
with one trivial endpoint so the guard is provable before anything depends on it.

**Acceptance criteria:**
- [x] `RequireRole(roles ...domain.Role)` in `identity/transport/http`, reading the role from
      the `domain.User` that `RequireAuth` put in the context — **never** from session claims
- [x] A request with no user in the context answers 401, not 403: that is a wiring mistake,
      the same failure mode `CallerResolver` already handles
- [x] A user whose role is not listed answers **403** with an RFC 9457 problem document
- [x] The composition root mounts an `/api/agent` group inside the authenticated group,
      carrying `RequireRole(agent, admin)`
- [x] `GET /api/agent/me` returns the caller's role **and our own user id** — see below
- [x] An empty role list admits **nobody**, beyond the stated criteria

**Verification:**
- [x] A customer receives 403 on every path under `/api/agent`, driven from `agentPaths()` so
      a route added later is covered without the test being edited
- [x] An agent and an admin both receive 200 on every one of them — which also proves each
      path **exists**, because chi routes before it runs the group's middleware, so an
      unmounted path would answer 404 rather than 403
- [x] An unauthenticated request receives 401
- [x] The customer's ticket endpoints still admit a customer — slice 2 must not close slice 1
- [x] 7 unit tests on the middleware in isolation, including a role our CHECK constraint
      cannot currently produce
- [x] **5 mutations, 5 dead**: mounting the group without the guard; an empty list meaning
      "everyone"; 403 instead of 401 for a missing caller; refusing and calling the next
      handler anyway; naming the required roles in the refusal
- [x] `make check` and `make test-int` clean; `make arch` unchanged

**Dependencies:** T17
**Files:** `internal/modules/identity/transport/http/require_role.go`, `.../me.go`,
`internal/app/router.go`, plus tests
**Scope:** M

**Decisions taken during T18:**

- **The roles are named in the composition root, not inside the module.** `RequireRole` takes
  them as arguments and `internal/app` supplies `RoleAgent, RoleAdmin`. That is plan decision
  C made concrete: the boundary is readable in the router rather than buried in a helper
  called `RequireAgent`, and `internal/app` is already the one package allowed to know what
  both modules mean by a role — the same reason `actorRole` lives in `adapters.go`.
- **An empty role list fails closed.** `RequireRole()` with the arguments forgotten refuses
  everyone. The alternative — treating it as "no restriction" — is the exact shape of the
  trap this project was built around: `clerkhttp.WithHeaderAuthorization` looks mounted and
  rejects nothing (§4.3).
- **The 403 names neither the caller's role nor the ones that would have worked.** It tells
  someone how to describe an account worth attacking, and withholding it costs an honest
  caller nothing.
- **`/me` returns the role and our user id, not the email or the name.** The role is what the
  frontend's agent layout decides on, since Clerk holds no role. The id is the value that
  goes in `assignee_id`, and the browser cannot derive it — Clerk knows a subject and nothing
  about our `users` table — so T24's "assign this to me" has no other source. The email and
  name are already in the browser from Clerk, and re-serving them would create a second copy
  to drift.

**What cost the most time, and it was not the feature.** Four tests passed in isolation and
two failed together. The cause was the gotcha T7 recorded and this task walked straight back
into: **Clerk's JWK cache is global to the process and keyed by key id alone**, with no
scoping by instance or issuer. The helper minted a fresh RSA key per router but reused one
key id, so the first key won for the rest of the run and later tokens got a 401 that had
nothing to do with what the test was asserting. Each router now gets its own key id.

Worth stating because the diagnosis nearly went the wrong way: the first reading was that
the agent group had broken the customer routes, which is what the failing assertion says on
its face. Running the two tests alone is what separated "this code is wrong" from "these
tests interfere", and it took one command.

---

### T19: The agent queue ✅

**Description:** One page of **every** ticket, filtered and paginated, through a query that
has no requester predicate at all.

**Acceptance criteria:**
- [x] `ListTicketsForQueue` — a **new** query. `ListTicketsByRequester` is untouched and
      carries no new parameter
- [x] Filters: status, priority, and assignee (`me`, a specific id, `unassigned`, `any`)
- [x] Keyset pagination — on `(deadline, id)`, **not** `(created_at, id)`; see below
- [x] Ordered by the deadline ascending with paused tickets last
- [x] `GET /api/agent/tickets`, mounted in the T18 group
- [x] The response carries the requester's display name

**Verification:**
- [x] Integration: the queue contains tickets belonging to customers other than the caller
- [x] Integration: one case per filter and one for a pair — five subtests
- [x] Integration: paging one row at a time reaches the paused tickets at the end, and no row
      appears on two pages
- [x] Unit: the DTO falls back to the email when Clerk holds no name; the service refuses a
      filter with no assignee scope and does not call the repository
- [x] An unknown filter value is 400, not silently ignored
- [x] **6 mutations, 6 dead**: the queue query gaining a requester predicate; ordering by
      `created_at`; the cursor comparing against a bare NULL; `unassigned` behaving like
      `any`; the service accepting an unset scope; the DTO dropping the email fallback
- [x] The agent group's route tests cover `/api/agent/tickets` **without being edited**, from
      the `agentPaths()` list T18 introduced
- [x] `make check` and `make test-int` clean

**Dependencies:** T18
**Files:** `db/migrations/004_ticket_queue_index.sql`,
`internal/modules/ticket/infrastructure/postgres/queries/tickets.sql`, `.../repository.go`,
`internal/modules/ticket/application/{ports,service}.go`,
`internal/modules/ticket/transport/http/{queue,dto,caller}.go`, `internal/app/router.go`,
plus tests
**Scope:** L — larger than planned, because the card contradicted itself

**The card was wrong, and finding out cost a migration.**

It asked for keyset pagination on `(created_at, id)` *and* ordering by `sla_due_at`. Those
cannot both hold. **A keyset cursor is a position in the sort order**, so it has to carry the
key being sorted on; carrying a different one asks the database for "what comes after the row
created at 10:00" in a list ordered by deadline, which means nothing. The pages would skip and
repeat.

Worse, and not noticed at planning time: **`sla_due_at` is mutable.** Pausing a ticket clears
it and resuming recomputes it, so a cursor over it cannot promise what T10's cursor promised.

Resolved by choosing what the queue is *for*. Ordering by creation date is stable and answers
the wrong question — an agent reads this list to find what breaches next, and sorting the
loaded page in the browser sorts one page of many, which is the lie T14a already rejected for
filters. So: **order by the deadline, carry the deadline in the cursor, and accept that a
ticket transitioned mid-paging can move across it.** Only the row that moved is affected, and
it is the one an agent just acted on. OFFSET would shift every row after it, on any insert.

**Decisions taken during T19:**

- **The sentinel is `9999-12-31T23:59:59.999999Z`, not `'infinity'`.** `'infinity'` is the
  natural Postgres value and was the first implementation, until the generated cursor
  parameter turned out to be `*time.Time` — which has no such value, so a cursor pointing at a
  paused ticket could not be sent at all. The sentinel is exact in both directions:
  `timestamptz` resolves to one microsecond and RFC3339Nano writes six digits.
- **`COALESCE(...)` rather than `NULLS LAST`**, and the reason is the cursor rather than the
  ordering. Measured against Postgres 16 before committing to it:
  `(NULL, id) > (x, id)` is **NULL**, not false, and `WHERE NULL` discards the row — so with
  `NULLS LAST` every paused ticket would sort correctly and then vanish from the second page
  onward, silently. `TestPagingReachesThePausedTicketsAtTheEnd` is the test for it.
- **Migration 004 adds an expression index matching the ORDER BY.** A plain index on
  `sla_due_at` does not serve `COALESCE(sla_due_at, ...)`. Verified rather than assumed: the
  `text -> timestamptz` cast is STABLE, which an index expression normally refuses, and it is
  accepted here because a literal argument is constant-folded at parse time. `EXPLAIN` shows
  the row comparison as an **Index Cond**, not a Filter.
- **`AssigneeScope` is three values, not a nullable id.** A nil id already means "no filter",
  so it cannot also mean "unassigned". The zero value is refused rather than defaulted to
  "any", because defaulting would turn a caller who forgot into "return every ticket" on the
  one query with no predicate to fall back on.
- **`AgentRoutes` is a separate mount function from `Routes`.** Everything in it is backed by
  an unscoped query, so it is handed a router that already carries the role check. A handler
  added to the wrong list is a handler behind the wrong guard, and two lists make that visible.
- **Two rules moved layers while writing the tests**, and both moves were improvements rather
  than accommodations. Refusing an unset scope is a use-case precondition and belongs in the
  service, not the adapter. Falling back to the email when Clerk holds no name is a display
  decision and belongs in the DTO. Each is now unit-testable where it lives, and the repository
  went back to being only a mapping.

**Two things the tests caught that the code did not:**

- `newTicketWith` stamps **every** ticket with the same 24-hour deadline whatever its priority
  — the policy id varies, `sla_due_at` does not. The first ordering test seeded an urgent and a
  low ticket and asserted the urgent came first; they tied, and the uuid tiebreaker decided it
  by coin flip. The test now sets the deadlines explicitly. The failure was the test being
  wrong about a helper, and "fixing" the query would have broken it.
- One mutation was written badly: `t.requester_id = t.requester_id` is a tautology and scopes
  nothing, so it survived and looked like a gap in the tests. Rewritten to scope to an
  arbitrary user, it died immediately. **A mutation that does not change behaviour proves as
  little as a test that cannot fail.**

---

### T20: Agent ticket detail and unscoped history ✅

**Description:** One ticket and its full timeline, for a caller who is not its requester.

**Acceptance criteria:**
- [x] `GET /api/agent/tickets/{id}` and `GET /api/agent/tickets/{id}/history`
- [x] Backed by `GetTicketByID` (new) and `ListTicketStatusHistory` (**already existed** — it
      is the input to `sla.Reconstruct` and has never carried a requester predicate)
- [x] 404 for an id that does not exist
- [x] The agent history reuses the customer's DTO, so it carries the actor's role and still
      does not carry `actor_id`

**Verification:**
- [x] Integration: a ticket and a timeline are read without supplying a requester
- [x] Integration: the scoped query still refuses another customer's ticket
- [x] Integration through the **real repository**: `OneByID` and `Timeline` succeed with no
      requester, and an unknown id is `ErrTicketNotFound`
- [x] Both new paths joined `agentPaths()`, so the 403 / 200 / 401 tests cover them
- [x] **4 mutations, 4 dead**: `OneByID` pointed at the scoped query; the detail route
      unmounted; `GetTicketByID` ignoring its argument; `Timeline` not translating an empty
      result into not-found
- [x] `make check` and `make test-int` clean

**Dependencies:** T19
**Files:** `internal/modules/ticket/infrastructure/postgres/queries/tickets.sql`,
`.../repository.go`, `internal/modules/ticket/application/{ports,service}.go`,
`internal/modules/ticket/transport/http/{queue,caller}.go`, plus tests
**Scope:** S

**Two mutations survived, and both were real.**

The first was a **gap between two covered layers**. Pointing `OneByID` at the scoped
`GetTicketForRequester` left the entire suite green: the queue's integration tests run against
the generated queries, and the router tests run against a stub repository, so nothing anywhere
exercised the repository's *choice* of query. Each layer was covered and the seam between them
was not. Closed by testing through the real `Repository` in `internal/app`, where a pool-backed
fixture already existed for the lifecycle tests.

The second was **a test that was true for the wrong reason**. `TestAnUnknownTicketIDReturnsNoRows`
seeded nothing, so the table was empty inside its transaction and "no rows" held for any query
at all — including one whose predicate ignored its argument entirely. It now seeds a ticket
first, so the assertion is about the id rather than about the table being empty.

**And one mutation reported as surviving had never been applied.** The substitution silently
matched nothing, and `grep -c` returned 0 while the result read as a coverage gap. Checking
that a mutation actually landed is part of running one — an unapplied mutation and a
well-tested one produce the same green.

---

> **Checkpoint G — an agent can read every ticket through the API, and a customer receives
> 403 on every agent path.**

---

## Phase 3: Acting on a ticket

### T21: Assignment ✅

**Description:** Put an agent on a ticket, take them off, and refuse anyone who is not an
agent — with the check declared by the ticket module and answered by the composition root.

**Acceptance criteria:**
- [x] `PATCH /api/agent/tickets/{id}/assignee`, body `{"assignee_id": "<uuid>|null"}`
- [x] `application.AssigneeDirectory` — a consumer-declared port answering *may this id hold
      tickets?*, implemented in `internal/app` against identity, following `SLAPolicies`
- [x] Assigning a customer's id and assigning an id that does not exist give the **same**
      answer, so this endpoint cannot be used to enumerate which uuids name users
- [x] `null` unassigns, and is distinguishable from an absent field
- [x] **No `ticket_status_history` row is written**, and no clock column moves
- [x] Assigning an already-assigned ticket overwrites; it is not an error

**Verification:**
- [x] Integration: one case per role for the target — agent and admin succeed, a customer and
      an unknown id are both refused with `ErrNotAssignable`
- [x] Integration: the history row count and **every** `sla_*` column are unchanged by an
      assignment, and so is the status
- [x] Integration: reassignment overwrites, `nil` clears the column, an unknown ticket is
      `ErrTicketNotFound`
- [x] Unit: the directory is not consulted when unassigning, and a directory **failure** is
      not reported as a refusal
- [x] Handler: absent field, `null`, a malformed uuid, a non-string, and unparseable JSON
- [x] **6 mutations, 6 dead**: the service skipping the directory; a directory failure read as
      a refusal; an absent field treated as null; `MayHoldTickets` admitting anyone; an unknown
      id answering "yes"; the assignment touching `sla_due_at`
- [x] `make check` and `make test-int` clean

**Dependencies:** T18
**Files:** `internal/modules/ticket/application/{ports,service}.go`,
`internal/modules/ticket/infrastructure/postgres/{queries/tickets.sql,repository.go}`,
`internal/modules/ticket/transport/http/{assign,caller}.go`, `internal/modules/ticket/module.go`,
`internal/modules/identity/{application/service.go,infrastructure/postgres/*}`,
`internal/app/adapters.go`, `cmd/api/main.go`, plus tests
**Scope:** M

**Decisions taken during T21:**

- **It is a 400, not the 422 the card named.** Every other validation failure in this API is a
  400 carrying an `errors` member, and the frontend's `ApiError.fieldErrors` already reads it
  (T13). A second status for the same shape of answer would buy a semantic distinction nobody
  consumes at the cost of a second code path in the client.
- **`MayHoldTickets` names the roles, the ticket module does not.** The port asks "may this
  person hold tickets"; that the answer happens to be "their role is agent or admin" is the
  identity module's business. The ticket module never learns the word *agent* for this.
- **An unknown id answers `false`, not "no such user".** The caller is deciding whether to
  accept an assignee, and "does not exist" and "is a customer" have to be the same answer
  there — the same reasoning that makes another customer's ticket a 404 rather than a 403.
- **A directory that cannot answer is not a refusal.** Reporting an outage as "that user may
  not hold tickets" would be a lie the caller acts on, so the cause travels up and the handler
  answers 500.
- **`agentPaths()` now carries the method.** Slice 2 mounts writes as well as reads, and
  requesting a PATCH route with GET answers 405 — which would read as a guard that let the
  request through. The guard tests cover the new endpoint without being rewritten.

**Three things measured rather than assumed:**

- **Neither obvious way of telling `null` from absent works.** A `*uuid.UUID` field leaves nil
  for both — and so does a `*json.RawMessage`, because `encoding/json` sets a pointer to nil
  when it reads `null`, whatever it points at. That second one was the implementation until a
  test failed on it. Decoding into a `map[string]json.RawMessage` and asking whether the key is
  present is what actually distinguishes them, and the difference matters: a client sending
  `{}` by accident would otherwise silently unassign a ticket somebody is working on.
- **The test cleanup hit migration 003's deliberate lack of `ON DELETE CASCADE`.** Deleting a
  seeded agent failed because a ticket still pointed at them — exactly what T6 wanted, stated
  as "deleting a user or a policy that is still referenced fails loudly". The cleanup now
  releases the reference first. The schema was right and the test was wrong.
- **Seeded Clerk ids are unique per call, not derived from the test name.** `clerk_user_id` is
  UNIQUE, so the first failed run left a row that poisoned every later run with a duplicate-key
  error that had nothing to do with what was being tested.

---

### T22: The transition endpoint ✅

**Description:** Expose the write path built in T11. This is the first time the SLA clock can
be paused and resumed over HTTP.

**Acceptance criteria:**
- [x] `POST /api/agent/tickets/{id}/transitions`, body `{"to": "<status>", "reason": "…"}`
- [x] Calls `Service.Transition` — **no new write logic in this task**
- [x] An edge the actor's role may not take is **403**; an edge that does not exist is a field
      error. The domain already tells them apart and the handler does not flatten them
- [x] A transition on a `closed` ticket is refused, with **no special case** in the handler —
      it is terminal, so no edge leaves it and the state machine refuses it like any other
      impossible move
- [x] The response is the updated ticket, so a client needs no follow-up read to see the new
      deadline — which is the point of the request when it is a pause
- [x] Statuses were already in the generated contract since T14a; no drift

**Verification:**
- [x] Handler: the actor's role comes from the caller and a role in the body is ignored; 403
      and the field error are distinct; an unknown status never reaches the state machine; a
      whitespace reason is stored as absent; an overlong one is refused; an unknown ticket is 404
- [x] Integration: a transition appends exactly one history row per move, and a customer is
      refused an agent's edge through the whole service path
- [x] **5 mutations, 5 dead**: 403 answered as a field error; the actor's role taken from
      somewhere other than the caller; an unknown status reaching the use case; a whitespace
      reason stored; the route left unmounted
- [x] `make check` and `make test-int` clean

**Dependencies:** T18
**Files:** `internal/modules/ticket/transport/http/{transition,caller}.go`, plus tests
**Scope:** M

**Decisions taken during T22:**

- **`POST .../transitions`, plural, and not `PATCH .../status`.** A transition is a thing that
  happened, appended to a history; the status column is a cache of that history (§4.2). The URL
  says which of the two a client is adding to.
- **403 and the field error stay different answers.** The domain distinguishes "this move does
  not exist" from "it exists and is not yours to make", and flattening them would tell an agent
  their client is broken when the truth is that somebody else has to do it.
- **The impossible-edge case is a 400, not the card's 422** — the same reasoning as T21.
- **An unknown status is rejected before the state machine sees it.** The domain would refuse
  it anyway, but as "you cannot go from open to blorp", which reads like an edge that might
  exist somewhere else.
- **A reason of whitespace is stored as absent**, because a blank line in a timeline an agent
  reads is worse than no line.

**What was deliberately not written.** The first draft of the integration tests re-asserted
that pausing clears the deadline, that resuming keeps the spend, and that `closed` is terminal.
All three were already covered against this same fixture by
`TestPausingStopsTheClockAndResumingKeepsWhatWasSpent` and `TestTransitionRefusesToLeaveClosed`,
written in T11. The duplicates were removed rather than kept: a second copy of a passing
assertion is slower to run and no more convincing, and it makes the file harder to read for
whoever comes next.

**Review feedback from CodeRabbit on PR #13, and what was done with it.**

Two findings, both acted on, neither applied as proposed.

*Reject a value after the body.* Valid. `json.Decoder` reads one value and
stops, so `{"to":"pending"}{}` decoded happily and the trailing object was never
seen. Nothing downstream was wrong about it — it simply was not read — but a
client shipping garbage after a valid body should be told, because the next
thing it ships may be the half the caller meant.

Fixed in **all three** endpoints that take a body rather than the one flagged.
Two left lax is a rule that holds where somebody happened to look. `decodeBody`
now owns it, and it closed a second inconsistency found on the way: the create
endpoint capped its body with `io.LimitReader`, which **truncates silently**, so
an oversized body arrived as invalid JSON and was reported as a syntax error
rather than as a size one. All three now use `http.MaxBytesReader`.

*Reconstruct the SLA clock in `TestTheCacheStillMatchesTheHistoryAfterATransition`
and compare the `sla_*` columns.* **The suggestion was declined; the finding was
not.** That reconstruction already exists, and is stronger:
`TestCacheAlwaysMatchesTheHistoryItWasBuiltFrom` is property-based over 15
generated sequences of up to 6 transitions and asserts it after creation and
after every step. Three fixed steps here would be slower and strictly weaker —
the same reason three other tests were deleted from this task.

But the finding pointed at a real defect that the proposed fix would have
buried: **the test's name claimed it compared a cache, and it counted rows.**
That is [ADR 0010](docs/adr/0010-a-name-that-lies-is-a-bug.md) applied to a test
name — a name is a defect when it lies about what the thing does. Renamed to
`TestEveryTransitionAppendsExactlyOneHistoryRow`, which is what it asserts and
what the property test does *not*: a write path appending two rows, or none,
would still satisfy a reconstruction, because `Reconstruct` reads whatever rows
are there. It also now checks the last row records the move just made, so the
right number of rows in the wrong order does not pass.

Both fixes were mutation-checked: removing the EOF check turns the trailing-value
test red, and recording the wrong status in the history row turns the renamed one
red.

**The test cleanup hit the schema a second time.** T21 released `tickets.assignee_id` before
deleting a seeded user; transitions add `ticket_status_history.actor_id`, which is NOT NULL and
therefore cannot be released — those rows have to go. §10 forbids hard-deleting history *in the
application*; a fixture removing rows it created is the one place that rule does not reach, and
saying so in the cleanup is cheaper than someone re-deriving it later.

---

> **Checkpoint H — review with human before the frontend.**

---

## Phase 4: The agent in a browser

### T23: `(agent)` route group, role guard, queue view ✅

**Description:** The agent's half of the app. A second route group beside `(customer)`,
guarded the same way §4.3 guards the API — and, like T12, the guard here is UX while the real
boundary stays on the API side.

**Acceptance criteria:**
- [x] `web/app/(agent)/` group with its own layout, calling `auth.protect()` **and** reading
      the role from the API — Clerk holds no role, our database does
- [x] A customer visiting `/queue` is redirected; a signed-out visitor goes to sign-in
- [x] Queue table: requester, title, priority, status, SLA remaining
- [x] `sla-timer.tsx` reused unchanged — no second implementation of the countdown
- [x] Filters in URL query params, as T14a established
- [x] Loading, two empty states, and the 403 state for a customer who forced the URL

**Verification:**
- [x] 7 component tests: the requester is shown; rows keep the API's order; every filter
      reaches the request and absent ones do not; each filter combination caches separately;
      a 403 renders a sentence rather than a status code; the two empty states differ
- [x] `pnpm build`, `pnpm lint` and `tsc --noEmit` clean; `/queue` builds as a dynamic route
- [x] **5 mutations, 5 dead**: reordering the rows in the browser; dropping the filters from
      the query key; rendering the 403 as a plain status code; collapsing the two empty
      states; omitting the requester's name
- [x] **Measured, not assumed**, the way T12 required — see below

**Dependencies:** T19
**Files:** `web/lib/agent.ts`, `web/app/(agent)/layout.tsx`,
`web/app/(agent)/queue/{page,agent-queue,queue-filters}.tsx`, plus tests
**Scope:** M

**Decisions taken during T23:**

- **The role check runs on the server, in the layout.** A client-side check would flash the
  agent chrome before redirecting a customer. It costs one request per navigation into the
  group, against `/api/agent/me` — the cheapest endpoint in the API — with `cache: "no-store"`,
  because a cached role would outlive a promotion or survive a demotion.
- **Any failure of that request redirects, and the reasons are deliberately not told apart.**
  A 403 means "not staff"; an unreachable API means we cannot know. Showing the agent shell on
  "cannot know" is the one outcome worth avoiding: failing closed sends a real agent to their
  own tickets during an outage, which is recoverable, while failing open shows a customer a
  queue that errors on every request.
- **`agentKeys` is a separate cache tree from `ticketKeys`.** Not tidiness: invalidating the
  customer's list must not refetch the queue, and a ticket read as an agent is not the same
  cached value as the same ticket read by its requester — one is reachable and the other
  answers 404.
- **The assignee filter's vocabulary is transcribed, not generated.** Status and priority come
  from the generated contract; `any`/`unassigned`/`me` exist only on this endpoint and there is
  nothing in the shared contract to generate them from. Acceptable because an unknown value is
  answered with a 400 naming the field — it fails loudly on the first click rather than quietly
  returning the wrong rows.
- **The ordering is stated on the page.** A list of rows does not show its own sort order, and
  an agent who assumes newest-first reads the whole screen wrong when the point is that the top
  row is the next breach.

**Measured rather than assumed.** T12 shipped a correct `307` that pointed at Clerk's hosted
sign-in — a page that is not part of this app — so a status code is not evidence here. The
signed-out surface, read off a running dev server:

```
/          200
/queue     307 → http://localhost:3000/sign-in?redirect_url=…  [x-middleware-rewrite: /queue]
/tickets   307 → http://localhost:3000/sign-in?redirect_url=…
/sign-in   200
```

Two things that a bare 307 would not have shown: the target is **our** sign-in route rather
than the hosted one, and `x-middleware-rewrite` is present, which is what proves the proxy
passed the request through instead of short-circuiting it. Without that header the 307 could
equally be Clerk's development-instance handshake, which produces the same status.

**Not verified this way, and it needs a real session:** that a signed-in *customer* is
redirected from `/queue` to `/tickets`. The component test covers the 403 the API answers; the
layout's redirect on a non-staff role is covered by neither, and belongs in the E2E work of T25.

---

### T24: Agent ticket detail with actions ✅

**Description:** The detail view an agent works from: the timeline, the assign control and
the transition control.

**Acceptance criteria:**
- [x] Reuses `status-timeline.tsx` and `sla-timer.tsx` unchanged
- [x] Assign control lists agents and offers "unassign"; "assign to me" is one click
- [x] Transition control offers **only the edges this actor may take from the current
      status** — derived from the contract, not hardcoded
- [x] A rejected action renders the API's own sentence, verbatim from the problem document
- [x] Both actions invalidate what they invalidate, and they differ — see below

**Verification:**
- [x] 4 component tests on the detail, 8 on the transition control, 9 on the assign control,
      6 on `lib/agent.ts`, plus 4 Go tests on the published table and 3 on the DTO shapes
- [x] Integration: the roster holds staff and nobody else, orders unnamed colleagues last,
      and an empty roster is not an error
- [x] **17 mutations, 17 dead** across the six commits
- [x] `make check`, `make test-int` and the whole web suite clean

**Dependencies:** T20, T21, T22
**Files:** `internal/modules/ticket/domain/{transition,status,role}.go`,
`internal/modules/ticket/transport/http/{contract,dto,queue,assign,transition}.go`,
`internal/modules/identity/{application/service.go,infrastructure/postgres/*,transport/http/assignable.go}`,
`internal/app/router.go`, `web/lib/agent.ts`, `web/app/(agent)/queue/[id]/*`,
`web/components/{assign-control,transition-control}.tsx`, `docs/spec.md`, plus tests
**Scope:** L — the card asked for two things that did not exist yet

**Two acceptance criteria could not be met as written, and both were gaps rather than
mistakes in the card.**

*"Derived from the contract"* — the transition table was not in the contract. It lives in
`domain.allowed`, in Go, and `web/lib/contract.ts` published only the three vocabularies. So
the contract gained it: `domain.Edges()` hands out a copy, `cmd/gencontract` emits
`TRANSITIONS`, and two Go tests call `domain.Transition` at every published edge in both
directions rather than comparing the table to itself. Transcribing it in TypeScript was the
alternative, and it is the copy that breaks silently — the day an edge changes in §4.1, the
button stays on screen and the API starts refusing it.

*"Lists agents"* — no endpoint listed them. `GET /api/agent/assignable` is new, and it is
**the first response in this project that carries another user's id**. T14b dropped
`actor_id` from the history DTO and T19 sends `requester_name` rather than an id, both to
avoid handing out identifiers to enumerate, so the departure is written down rather than
slipped in. It is unavoidable — `PATCH .../assignee` takes an id — and what bounds it is the
shape of the answer: agents and admins by predicate, inside the group that already refuses
everyone else. The people who can read the roster are the people on it.

**Decisions taken during T24:**

- **`AgentTicketResponse` is a separate wire shape**, because `TicketResponse` is what a
  customer gets back for their own ticket. Adding `assignee_id` there would hand every
  customer the primary key of the agent working their case. The test encodes both shapes and
  greps the wire rather than reading the structs: a field added with the wrong tag would
  still travel while the type looked untouched.
- **The two controls settle their caches differently, and the difference is the point.** A
  transition invalidates the queue *and* the history — the deadline moved and the timeline
  gained a row. An assignment invalidates only the queue, because it writes no history row
  (plan §E). Both write the detail from the response rather than refetching it.
- **`asActorRole` converts at the boundary.** `/api/agent/me` answers with the identity
  module's role and `TRANSITIONS` is keyed on the ticket module's — the same three strings,
  two vocabularies, kept apart on purpose (ADR 0005). Go does the same conversion in its
  composition root; doing it implicitly in TypeScript would have quietly merged them.
- **A closed ticket renders a sentence and no buttons**, rather than a disabled row. A
  greyed-out button invites a click that can never work.

**§4.3 now records that assignment is flat.** Both `agent` and `admin` carry `Assign
ticket`, so any agent may hand any ticket to any other. That was true since the matrix was
written and had never been *chosen* — the question of who may assign to whom was never
asked. It is written down now so it stops being an accident, along with where a hierarchy
would go if one is wanted: `admin` already has its own column and differs from `agent` in
exactly one row.

**A mutation caught a missing assertion in the transition control.** Nothing checked that
the queue was invalidated after a move, so an agent would have kept looking at a queue
showing the ticket where it used to be — on a list ordered by deadline, which a pause
clears. The test now spies on the client and checks all three caches.

---

### T25: E2E, ADR 0011, and the docs

**Description:** Prove the slice in a browser against the deployed stack, and write down the
decision that will otherwise be re-litigated in six months.

**Acceptance criteria:**
- [ ] **ADR 0011** records plan decision B: why the scoped queries were not widened with a
      role flag, and what the route group is doing in the argument
- [ ] Spec §12.4 struck through and marked resolved, as §12.3 and §12.5 were
- [ ] Spec §2 roadmap row for slice 2 marked delivered
- [ ] `docs/architecture.md` gains the `/api/agent` group and `RequireRole`
- [ ] README explains the two query sets — it is the non-obvious part of the design
- [ ] E2E: an agent signs in, opens the queue, assigns a ticket to themselves, moves it to
      `pending`, and the customer's own view shows the paused clock

**Verification:**
- [ ] The E2E job passes on the PR
- [ ] `make check` and `make test-int` green
- [ ] An architecture test asserts the unscoped queries are used by no handler outside the
      agent group — if one can be written; if not, say so and explain why rather than skipping it

**Dependencies:** T23, T24
**Files:** `docs/adr/0011-*.md`, `docs/spec.md`, `docs/architecture.md`, `README.md`, `smoke/`
**Scope:** M

---

> **Checkpoint I — Slice 2 complete.**
