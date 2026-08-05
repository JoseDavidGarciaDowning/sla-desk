# Implementation Plan: Slice 2 — the agent

Status: **Draft — awaiting review**
Spec: [`docs/spec.md`](../../docs/spec.md) §2, §4.3
Slice 1: [`tasks/plan.md`](../plan.md) · [`tasks/todo.md`](../todo.md)
Last updated: 2026-08-05

---

## Overview

Slice 2 is the first authorization that is not ownership. Until now every read answered
one question — *is this yours?* — and the answer lived in a SQL predicate. An agent reads
tickets that are not theirs, so that predicate cannot be the mechanism any more.

What ships: a way to become an agent, a queue of every ticket, assignment, and the status
transitions the state machine has been able to perform since T11 but no route has ever
exposed.

Not in this slice: comments, attachments, the breach worker, WebSockets, search, an admin
UI for SLA policies. Those are slices 4–9 (spec §2).

---

## What reading the code changed

Three things the spec's one-line description of slice 2 does not say, found before
planning rather than during implementation.

### 1. The assignment schema already exists

Migration `003_tickets_and_status_history.sql` created it:

```sql
assignee_id  UUID REFERENCES users (id),
...
-- The agent dashboard: one assignee, filtered by status.
CREATE INDEX tickets_assignee_status ON tickets (assignee_id, status);
```

T6 wrote the column and the index the agent dashboard needs and left them unused. **No
migration is required for assignment.** What is missing is the query, the use case, the
endpoint and the UI.

### 2. The transition write path is complete and unreachable

`domain.Transition(current, target, actor)` holds the §4.1 edge table including which
roles may take each edge. `Service.Transition` resolves the clock, takes `FOR UPDATE`,
writes the history row, rebuilds the clock and updates the cache — all built and mutation-
tested in T11.

`tickethttp.Routes` mounts four endpoints: create, list, get, history. **None of them is a
transition.** The whole write path is reachable only from integration tests.

That makes the transition endpoint cheap — the hard half is written and proven — and makes
leaving it out expensive: production would keep carrying a tested write path that nothing
can call, which is the kind of code that rots because no user ever exercises it.

### 3. The role plumbing is already in place, and nothing can create an agent

| Already there | Missing |
|---|---|
| `identity/domain.Role` with `RoleAgent`, `RoleAdmin` | Any way for a row to hold one |
| `users.role` CHECK accepts all three | — |
| `ticket/domain.Role`, and the `allowed` transition map keyed by it | — |
| `tickethttp.Caller{ID, Role}` and `callerFromContext` converting between the two vocabularies | Any middleware that *checks* the role |

The role reaches the ticket module already. Nothing anywhere reads it to allow or refuse a
request, and every user in the database is a `customer`.

---

## Architecture Decisions

### A. The first agent comes from server config, not from a migration and not from an endpoint

Resolves spec §12.4, which has been open since slice 1.

**A goose SQL migration cannot do this.** Three reasons, each sufficient:

1. Migrations cannot read the environment, and the `clerk_user_id` of the first agent
   differs in local, CI and production. Hardcoding one commits an environment-specific
   identifier to git and applies it to all three.
2. The `users` row may not exist yet. It is written by the Clerk webhook when that person
   first signs up, which is usually *after* the migration ran.
3. `db/migrations` is the schema's history. A grant to a named human is not schema.

**Decision:** `AGENT_CLERK_USER_IDS` and `ADMIN_CLERK_USER_IDS` in `internal/platform/config`,
comma-separated, empty by default. The identity module consults them when it writes a
`users` row and promotes the row if the subject is listed.

This keeps spec §4.5 exactly as written — *"Role is never taken from the webhook payload or
the JWT"* — because the list is ours and arrives from the process environment, never from a
request. It also works on Cloud Run, which has no shell to run a one-off command in.

| Rejected | Why |
|---|---|
| Goose Go migration reading `os.Getenv` | Migrations run once per environment and the user may not exist yet. Re-granting would mean a new migration per agent |
| `cmd/grantrole` one-shot CLI | Correct and auditable, but Cloud Run gives no way to run it against production without adding a Cloud Run Job. Worth building in slice 9 alongside the admin surface |
| `PATCH /api/users/{id}/role` now | Has no bootstrap answer — someone must already be an admin to call it. It is the *second* step and belongs with the admin surface in slice 9 |

**Cost, stated plainly:** promotion happens at provisioning time, so a user who is added to
the list *after* signing up is not promoted until their row is next written. T17 therefore
also applies the list on every `EnsureUser`, which `RequireAuth` calls on every
authenticated request — so the promotion lands on their next request rather than never.
Removal from the list does **not** demote; that is deliberate, because a config typo must
not silently strip an agent mid-shift. Demotion is slice 9's admin endpoint.

### B. Agent reads get their own queries. The scoped ones are not widened

This is the decision the slice exists to make, and it is the one an ADR gets.

The obvious implementation is a flag: pass the role into `ListForRequester` and skip the
`requester_id` predicate when it is `agent`. **Do not do this.** T14a's mutation testing
already caught exactly this shape once — a query that was scoped for the case the test
happened to run and open for the one it did not. A boolean that disables a security
predicate is a boolean that can be wrong, and every call site becomes a place to get it
wrong.

**Decision:** two query sets that do not share a code path.

| Customer path — unchanged | Agent path — new |
|---|---|
| `ListTicketsByRequester` | `ListTicketsForQueue` |
| `GetTicketForRequester` | `GetTicketByID` |
| `ListTicketStatusHistoryForRequester` | `ListTicketStatusHistory` *(exists — it is the input to the clock)* |

The customer queries keep their predicate, are never called with a role, and cannot be
talked into returning someone else's ticket. The agent queries have no predicate at all and
are **only reachable from routes mounted behind a role check**, which is the second half of
the decision:

### C. Authorization moves to the route group, and the router is what proves it

With no predicate in the SQL, the guarantee has to come from somewhere. It comes from where
the route is mounted:

```
r.Group(RequireAuth)                 → /api/tickets/*        customer-scoped queries
    r.Group(RequireRole(agent,admin)) → /api/agent/tickets/*  unscoped queries
```

`RequireRole` lives in `identity/transport/http` next to `RequireAuth`, and the composition
root mounts it. A separate path prefix rather than role-branching inside the existing
handlers, for three reasons:

- The boundary is visible in the router, in the URL, and in a test that requests every
  agent path as a customer and requires 403 on each.
- A new agent endpoint is added inside a group that already carries the check. Forgetting is
  a compile-time-shaped mistake rather than a silent one — the same property `tickethttp.Routes`
  already gives authentication.
- The frontend's agent views hit a namespace, not a shape that changes meaning with the
  caller.

**403 here, not 404.** The opposite of §11's rule, and deliberately: `/api/agent/tickets`
existing is not a secret — a customer learns nothing from being refused it, because there is
no id in the path to confirm. The 404-not-403 rule protects the existence of *a specific
ticket*, which is why `GET /api/agent/tickets/{id}` still answers 404 for an id that does
not exist.

### D. Assignment is a ticket-module use case, and the assignee must be an agent

`assignee_id REFERENCES users (id)` lets any user id land there, including a customer's. The
ticket module cannot check that — it does not know the identity module exists (ADR 0005).

**Decision:** the check is a consumer-declared port, like `SLAPolicies`. The ticket module
declares what it needs — *can this id take tickets?* — and the composition root implements it
against identity. `application.AssigneeDirectory` with one method, following the shape
`SLAPolicies` already established.

Rejected: a database CHECK or trigger joining `users.role`. It would enforce the rule at the
right layer but freeze the answer: demoting an agent who holds open tickets would then fail
at write time on unrelated updates.

### E. Assignment does not touch the SLA clock, and is not a status transition

Assigning a ticket writes one column. It does **not** write a `ticket_status_history` row,
because the history is the fact the clock is rebuilt from (§4.2) and assignment does not move
the ticket's status. Adding a row for it would pad the timeline the clock walks, and the
consistency test would be right to fail.

The audit trail for *who assigned what to whom* is slice 9's audit log. Recorded here so the
gap is a decision rather than an oversight.

### F. Delivery: four chained PRs, not one

Estimated at well over the 400-line review budget. Each PR below leaves `main` deployable and
green, and each is a vertical slice with its own tests.

| PR | Content | Deployable meaning |
|---|---|---|
| 1 | T17 — the role bootstrap | An agent exists in the database and sees nothing new yet |
| 2 | T18 + T19 + T20 — `RequireRole`, the queue, agent detail | An agent can read every ticket through the API |
| 3 | T21 + T22 — assignment and transitions | An agent can act on a ticket through the API |
| 4 | T23 + T24 + T25 — the agent UI, E2E and docs | A human can do it in a browser |

---

## Dependency Graph

```
T17 role bootstrap  ◀── nothing can be an agent until this lands
 │
 ▼
T18 RequireRole + the /api/agent group
 │
 ├──▶ T19 queue: unscoped list + filters + pagination
 │     └──▶ T20 agent detail + unscoped history
 │
 ├──▶ T21 assignment (needs the AssigneeDirectory port)
 │
 └──▶ T22 transition endpoint   ◀── write path already exists (T11)
             │
             ▼
       T23 frontend: (agent) group, role guard, queue     ◀── needs T19
             └──▶ T24 agent detail with actions           ◀── needs T20, T21, T22
                   └──▶ T25 E2E + ADR 0011 + docs
```

T21 and T22 are independent of each other and of T19/T20 once T18 lands. T22 is the cheapest
of the three, because only the HTTP surface is missing.

---

## Task List

### Phase 1: Becoming an agent

- [ ] **T17** — Role bootstrap from config, applied on every `EnsureUser`

#### Checkpoint F — after T17
- [ ] A `clerk_user_id` in `AGENT_CLERK_USER_IDS` lands as `role = 'agent'`, whether the row
      is created by the webhook or by the lazy upsert
- [ ] A user **not** in the list is still `customer`, and no webhook payload can change that
- [ ] Removing an id does not demote — asserted, because it is a decision and not an accident
- [ ] `make check` green

### Phase 2: Reading every ticket

- [ ] **T18** — `RequireRole`, the `/api/agent` route group, and the test that proves it
- [ ] **T19** — Agent queue: unscoped list, filters, keyset pagination, assignee filter
- [ ] **T20** — Agent ticket detail and unscoped history

#### Checkpoint G — after T20
- [ ] A customer requesting any `/api/agent/*` path receives **403**, one case per route
- [ ] An agent's queue contains tickets belonging to customers other than themselves
- [ ] The customer endpoints are **unchanged** — their tests pass untouched, and the scoped
      queries have no new parameter
- [ ] `GET /api/agent/tickets/{id}` answers 404 for an id that does not exist
- [ ] `make test-int` green

### Phase 3: Acting on a ticket

- [ ] **T21** — `PATCH /api/agent/tickets/{id}/assignee`, with the `AssigneeDirectory` port
- [ ] **T22** — `POST /api/agent/tickets/{id}/transitions`

#### Checkpoint H — after T22
- [ ] Assigning to a customer's id is refused; assigning to an agent succeeds
- [ ] Unassigning (`null`) works and is distinguishable from "field absent"
- [ ] An agent moving `open → pending` pauses the clock, and `sla_due_at` becomes null —
      the headline behaviour of the domain, reachable over HTTP for the first time
- [ ] The consistency property still holds after every transition made through the endpoint
- [ ] An edge the actor's role may not take is refused with 403 and writes nothing
- [ ] **Review with human before Phase 4**

### Phase 4: The agent in a browser

- [ ] **T23** — `(agent)` route group, role guard, the queue view
- [ ] **T24** — Agent ticket detail with assign and transition actions
- [ ] **T25** — E2E coverage, ADR 0011, spec and README updates

#### Checkpoint I — Slice 2 complete
- [ ] A signed-in agent sees every ticket; a signed-in customer visiting `/agent` is refused
- [ ] Assign and transition work end to end against the deployed API
- [ ] ADR 0011 records decision B — why the scoped queries were not widened
- [ ] Spec §12.4 is struck through and resolved, like §12.3 and §12.5 before it
- [ ] `make check` and `make test-int` green; the E2E job passes on the PR

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| An agent query is called from a route outside the role group, exposing every ticket to a customer | **High** | Decision C: the unscoped queries are only reachable through handlers mounted in the guarded group. T18 asserts 403 on every agent path as a customer, one case per route — T14a proved that one case for the whole surface is not enough |
| The customer queries are widened "just this once" to serve both callers | **High** | Decision B. Checkpoint G requires the scoped queries to have **no new parameter**. A reviewer can check it by diffing the `.sql` files |
| A config typo silently makes someone an agent, or silently makes nobody one | **High** | T17 logs, at startup, how many ids each list holds — never the ids. A malformed entry fails the boot rather than being skipped, same rule as the Clerk webhook secret |
| Role read from the token instead of the database, by an agent-only handler taking the shortcut | **High** | `RequireRole` reads the role off the `domain.User` that `RequireAuth` put in the context, which came from our `users` row. There is no other path — the session claims are not in scope by then |
| Assignment lands a customer's id in `assignee_id` | Medium | Decision D. Port checked in the use case, one integration test per role |
| The transition endpoint lets a customer take an agent's edge | **High** | The `allowed` map already refuses it in the domain (T11). T22 asserts it at the HTTP boundary too, because the domain refusing is not evidence the handler asks |
| Slice 2's PRs blow the 400-line budget and get reviewed shallowly | Medium | Decision F: four chained PRs, each deployable |
| The agent UI duplicates the customer's ticket list into a second, drifting copy | Medium | `sla-timer.tsx` and `status-timeline.tsx` are reused as-is. Only the queue table and the action bar are new |
| `ticket_status_history.actor_role` starts disagreeing with `users.role` for the same person | Low | It is supposed to: T6 denormalised it on purpose so promoting someone does not rewrite the audit trail. Recorded here so nobody "fixes" it |

---

## Open Questions

1. **Does an agent create tickets on a customer's behalf?** The RBAC matrix says agents may
   create tickets, but `requester_id` comes from the authenticated caller — so today an agent
   creating one becomes its requester. Out of scope here; revisit when comments land in
   slice 4.
2. **Does assignment notify anyone?** No notification surface exists until slice 6.
3. ~~Agent role assignment~~ — resolved, decision A.
