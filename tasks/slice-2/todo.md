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

### T18: `RequireRole` and the `/api/agent` route group ⚠️ the slice's load-bearing task

**Description:** The middleware that turns a role into an authorization decision, and the
route group that is the only place the unscoped queries will ever be reachable from. Ships
with one trivial endpoint so the guard is provable before anything depends on it.

**Acceptance criteria:**
- [ ] `RequireRole(roles ...domain.Role)` in `identity/transport/http`, reading the role from
      the `domain.User` that `RequireAuth` put in the context — **never** from session claims
- [ ] A request with no user in the context answers 401, not 403: that is a wiring mistake,
      the same failure mode `CallerResolver` already handles
- [ ] A user whose role is not listed answers **403** with an RFC 9457 problem document
- [ ] The composition root mounts an `/api/agent` group inside the authenticated group,
      carrying `RequireRole(agent, admin)`
- [ ] `GET /api/agent/me` returns the caller's role — the smallest endpoint that proves the
      chain end to end

**Verification:**
- [ ] A customer receives 403 on every path under `/api/agent`, one case per route, not one
      case for the surface (T14a's lesson)
- [ ] An agent and an admin both receive 200
- [ ] An unauthenticated request receives 401, and reaches neither middleware
- [ ] Mutations: removing `RequireRole` from the group; allowing an empty role list to mean
      "everyone"; reading the role from the token — each turns a test red
- [ ] `make arch` still green: identity's HTTP leaf still depends on nothing in another module

**Dependencies:** T17
**Files:** `internal/modules/identity/transport/http/require_role.go` + test,
`internal/modules/identity/module.go`, `internal/app/router.go`, `internal/app/router_test.go`
**Scope:** M

---

### T19: The agent queue

**Description:** One page of **every** ticket, filtered and paginated the way the customer
list already is, through a query that has no requester predicate at all.

**Acceptance criteria:**
- [ ] `ListTicketsForQueue` in the ticket module's queries — a **new** query, with the
      customer's `ListTicketsByRequester` untouched and carrying no new parameter
- [ ] Filters: status, priority, and **assignee** (`me`, a specific id, and `unassigned`)
- [ ] Keyset pagination on `(created_at, id)`, reusing the cursor encoding from T10 so both
      lists mean the same thing by a cursor
- [ ] Ordered by `sla_due_at` ascending with nulls last, **not** by `created_at` — a queue is
      read to find what breaches next, and a paused ticket has no deadline (§4.2)
- [ ] `GET /api/agent/tickets`, mounted in the T18 group
- [ ] The response carries the requester's display name — an agent queue that shows only ids
      is not usable

**Verification:**
- [ ] Integration: the queue contains tickets belonging to customers other than the caller
- [ ] Integration: each filter, and the pair, return exactly the expected set — one case per
      filter, because a query can be right for the case a test happens to run
- [ ] Pagination is stable across an insert between two page reads, as T10 asserted
- [ ] `EXPLAIN` shows the `tickets_assignee_status` index used by the assignee filter, and no
      sequential scan on the unfiltered queue
- [ ] An unknown filter value is 400, not silently ignored (T14a's rule)
- [ ] Mutations: adding a requester predicate to the queue query; dropping nulls-last from the
      ordering; dropping the filters from the query key — each turns a test red

**Dependencies:** T18
**Files:** `internal/modules/ticket/infrastructure/postgres/queries/tickets.sql`,
`internal/modules/ticket/application/{ports,service}.go`,
`internal/modules/ticket/transport/http/{handlers,dto}.go`, `internal/app/router.go`, plus tests
**Scope:** M

---

### T20: Agent ticket detail and unscoped history

**Description:** One ticket and its full timeline, for a caller who is not its requester.

**Acceptance criteria:**
- [ ] `GET /api/agent/tickets/{id}` and `GET /api/agent/tickets/{id}/history`
- [ ] Backed by `GetTicketByID` (new) and `ListTicketStatusHistory` (**exists** — it is the
      input to `sla.Reconstruct` and already has no requester predicate)
- [ ] 404 for an id that does not exist — the §11 rule still applies to a specific ticket even
      though the group itself answers 403
- [ ] The agent history DTO **does** carry the actor's role, as the customer's does; it still
      does not carry `actor_id` (T14b), because slice 2 adds no reason to expose one

**Verification:**
- [ ] Integration: an agent reads a ticket whose requester is someone else
- [ ] `TestHistoryNeverExposesTheActorsIdentity` extended to the agent route
- [ ] Mutations: pointing the agent handler at the scoped query; returning 403 instead of 404
      for a missing id — each turns a test red

**Dependencies:** T19
**Files:** as T19, plus `internal/modules/ticket/infrastructure/postgres/repository.go`
**Scope:** S

---

> **Checkpoint G — an agent can read every ticket through the API, and a customer receives
> 403 on every agent path.**

---

## Phase 3: Acting on a ticket

### T21: Assignment

**Description:** Put an agent on a ticket, take them off, and refuse anyone who is not an
agent — with the check declared by the ticket module and implemented in the composition root.

**Acceptance criteria:**
- [ ] `PATCH /api/agent/tickets/{id}/assignee`, body `{"assignee_id": "<uuid>|null"}`
- [ ] `application.AssigneeDirectory` — a consumer-declared port answering *may this id hold
      tickets?*, implemented in `internal/app` against identity, following `SLAPolicies`
- [ ] Assigning a customer's id is **422**; assigning an id that does not exist is 422 too,
      and the two are the same answer — an agent must not be able to enumerate user ids
- [ ] `null` unassigns, and is distinguishable from an absent field
- [ ] **No `ticket_status_history` row is written** (plan §E) — the timeline the clock walks
      is not padded
- [ ] Assigning an already-assigned ticket overwrites; it is not an error

**Verification:**
- [ ] Integration: one case per role for the target — agent succeeds, admin succeeds, customer
      refused, unknown id refused
- [ ] Integration: `sla_*` columns and the history row count are **unchanged** by an assignment
- [ ] The consistency property still holds afterwards
- [ ] `make arch`: the ticket module still imports no other business module
- [ ] Mutations: dropping the directory check; treating absent as null; writing a history row —
      each turns a test red

**Dependencies:** T18
**Files:** `internal/modules/ticket/application/{ports,service}.go`,
`internal/modules/ticket/infrastructure/postgres/queries/tickets.sql`,
`internal/modules/ticket/transport/http/{handlers,dto}.go`, `internal/app/adapters.go`, plus tests
**Scope:** M

---

### T22: The transition endpoint

**Description:** Expose the write path built in T11. This is the first time the SLA clock can
be paused and resumed over HTTP — the headline behaviour of the entire domain.

**Acceptance criteria:**
- [ ] `POST /api/agent/tickets/{id}/transitions`, body `{"to": "<status>", "reason": "…"}`
- [ ] Calls `Service.Transition`, which already resolves the clock before the transaction and
      takes `FOR UPDATE` — **no new write logic in this task**
- [ ] An edge the actor's role may not take is **403**; an edge that does not exist at all is
      **422**. The domain already distinguishes them; the handler must not flatten both into one
- [ ] A transition on a `closed` ticket is refused — `closed` is terminal (§4.1)
- [ ] The response is the updated ticket, so the client needs no follow-up read
- [ ] Statuses join the generated contract — already there since T14a, verify no drift

**Verification:**
- [ ] Integration: `open → pending` through the endpoint sets `sla_due_at` to null and
      `sla_clock_started_at` to null, and `pending → open` resumes with the budget already spent
      preserved
- [ ] The §9 consistency property holds after every transition made through the endpoint, not
      only after those made in a test's own transaction
- [ ] A customer calling it is 403 at the group, before the handler
- [ ] Concurrent transitions on one ticket: the second observes the first, because of `FOR UPDATE`
- [ ] Mutations: swapping the 403 and 422 branches; passing the caller's role from the request
      body; skipping the terminal check — each turns a test red

**Dependencies:** T18
**Files:** `internal/modules/ticket/transport/http/{handlers,dto}.go`, `internal/app/router.go`,
plus tests
**Scope:** M

---

> **Checkpoint H — review with human before the frontend.**

---

## Phase 4: The agent in a browser

### T23: `(agent)` route group, role guard, queue view

**Description:** The agent's half of the app. A second route group beside `(customer)`,
guarded the same way §4.3 guards the API — and, like T12, the guard here is UX and the real
boundary stays on the API side.

**Acceptance criteria:**
- [ ] `web/app/(agent)/` group with its own layout, calling `auth.protect()` **and** checking
      the role from the API — Clerk does not hold the role, our database does
- [ ] A customer visiting `/agent` is redirected; a signed-out visitor goes to sign-in
- [ ] Queue table: requester, title, priority, status, assignee, SLA remaining
- [ ] `sla-timer.tsx` reused unchanged — no second implementation of the countdown
- [ ] Filters in URL query params, as T14a established
- [ ] Loading, empty and error states, including the 403 state for a customer who forced the URL

**Verification:**
- [ ] Component tests for the table, the filters and the 403 state
- [ ] `pnpm build`, `pnpm lint`, `tsc --noEmit` clean
- [ ] Measured, not assumed: the actual redirect target is inspected, because T12 shipped a
      correct `307` pointing at the wrong host

**Dependencies:** T19
**Files:** `web/app/(agent)/layout.tsx`, `web/app/(agent)/queue/`, `web/lib/use-agent-tickets.ts`,
`web/lib/agent.ts`, plus tests
**Scope:** M

---

### T24: Agent ticket detail with actions

**Description:** The detail view an agent works from: the timeline, the assign control and the
transition control.

**Acceptance criteria:**
- [ ] Reuses `status-timeline.tsx` and `sla-timer.tsx` unchanged
- [ ] Assign control lists agents and offers "unassign"; "assign to me" is one click
- [ ] Transition control offers **only the edges this actor may take from the current status** —
      derived from the contract, not hardcoded in the component
- [ ] A rejected action renders the API's own sentence, verbatim from the problem document
- [ ] Both actions invalidate the queue and the detail, and neither loses the view's scroll state

**Verification:**
- [ ] Component tests: the offered edges change with the current status; a 403 renders; a 422
      renders its field errors
- [ ] Mutations: offering every status regardless of the current one; invalidating `all`
      instead of the two specific keys — each turns a test red

**Dependencies:** T20, T21, T22
**Files:** `web/app/(agent)/queue/[id]/`, `web/components/{assign-control,transition-control}.tsx`,
`web/lib/use-agent-tickets.ts`, plus tests
**Scope:** M

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
