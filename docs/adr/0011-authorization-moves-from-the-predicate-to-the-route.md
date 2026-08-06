# ADR 0011: Authorization moves from the predicate to the route

- **Status:** Accepted
- **Date:** 2026-08-06
- **Context:** Slice 2 — the agent reads tickets that are not theirs
- **Related:** [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md),
  [ADR 0010](./0010-a-name-that-lies-is-a-bug.md), `docs/spec.md` §4.3, §11

## Context

Until slice 2, every read in this system answered one question — *is this yours?* — and the
answer lived in SQL:

```sql
SELECT * FROM tickets WHERE requester_id = $1
```

That placement is the guarantee, not an implementation detail. Spec §4.3 says authorization
is enforced *"in the data layer, not only in handlers"*, and the reason is stated there too:
a forgotten check in Go must not be enough to leak another customer's ticket. A query that
never returns the row cannot be forgotten about.

Slice 2 breaks the premise. An agent reads **every** ticket, so there is no requester to
scope by, and the mechanism that carried the guarantee for three slices simply does not
apply. Something else has to carry it, and the choice of what is the decision this slice
exists to make.

## The option that was rejected

The obvious implementation is to widen the query the customer already uses:

```sql
-- rejected
WHERE (@role = 'agent' OR requester_id = @requester_id)
```

One query, one code path, no duplication. It is also wrong, for a reason this project has
already measured rather than feared.

**T14a's mutation testing caught exactly this shape.** A queue query was scoped for the
filter the test happened to exercise and open for the one it did not; every assertion
passed. The lesson recorded then was that one case for a whole surface is not coverage. The
lesson available now is stronger: **a boolean that disables a security predicate is a
boolean that can be wrong**, and every call site becomes a place to get it wrong.

Three specific costs:

- The predicate's protection becomes conditional on an argument, so reading the SQL no
  longer tells you whether a row can leak. You have to read every caller.
- A default-constructed filter — a zero value, a forgotten field, a struct literal missing a
  key — becomes an open query. Go's zero values make that the easy mistake, not the hard one.
- The customer's endpoints and the agent's now share a code path, so a change made for one
  is a change made for both, and the test that would catch it is the one nobody wrote.

## Decision

**Two query sets that do not share a code path, and a route group that decides who reaches
which.**

| Customer path — unchanged | Agent path — new |
|---|---|
| `ListTicketsByRequester` | `ListTicketsForQueue` |
| `GetTicketForRequester` | `GetTicketByID` |
| `ListTicketStatusHistoryForRequester` | `ListTicketStatusHistory` *(already existed)* |

The customer's queries keep their predicate, **take no new parameter**, and cannot be
talked into returning someone else's ticket by any argument. That is checkable by diffing
the `.sql` files rather than by reasoning about callers.

The agent's queries have no predicate at all. What replaces it is **where the handler is
mounted**:

```
r.Group(Authenticate)                                  ← RequireAuth: who are you
  ├─ /api/tickets/*                                     queries carry the predicate
  └─ r.Route("/api/agent", RequireRole(agent, admin))   ← what may you do
       ├─ GET  /me
       ├─ GET  /assignable
       ├─ GET  /tickets
       ├─ GET  /tickets/{id}
       ├─ GET  /tickets/{id}/history
       ├─ PATCH /tickets/{id}/assignee
       └─ POST /tickets/{id}/transitions
```

A prefix rather than a role branch inside the existing handlers, and the difference is what
can be checked:

- The boundary is visible in the router, in the URL, and in a test that walks **every path
  under the prefix** as a customer. That list is a value in the test file, so an endpoint
  added later is covered without the test being edited.
- A new agent endpoint is added inside a group that already refuses everyone else.
  Forgetting the check is not something a reviewer has to notice.
- Because chi routes before it runs a group's middleware, an unmounted path answers 404
  while a mounted one answers 403. The same test therefore proves the route is protected
  **and that it exists** — the gap between T8 and T10, where handlers were written and never
  wired, would surface there.

The role is read from the `users` row that `RequireAuth` resolved, never from the session
claims. A role in a token is not a role (§4.3).

## Consequences

**The guarantee is weaker in kind, and that is the honest part of this decision.** A SQL
predicate cannot be bypassed by a handler; a route group can be bypassed by mounting a
handler in the wrong place. What the design buys back is that mounting is *visible* —
one file, one list, one test — while a forgotten `WHERE` clause is invisible until someone
reads the query.

**403 here, not 404.** Spec §11's rule is the opposite, and it still holds where it was
written: a ticket that is not yours answers 404, because a 403 would confirm the id names a
real ticket. Nothing in `/api/agent` carries an id to confirm, so refusing the prefix
teaches a customer nothing. `GET /api/agent/tickets/{id}` still answers 404 for an id that
names nothing — the two rules apply to different questions.

**An empty role list admits nobody.** `RequireRole()` with the arguments forgotten refuses
everyone. The alternative reading — "no restriction" — is the exact trap this project was
built around: `clerkhttp.WithHeaderAuthorization` looks mounted and rejects nothing (§4.3).

**Duplication is the price, and it is bounded.** Three queries exist twice. They are three
`SELECT` statements whose difference is a `WHERE` clause, they are covered by integration
tests on both sides, and one of them — `ListTicketStatusHistory` — was already unscoped
because `sla.Reconstruct` reads it inside a transaction that has established which ticket it
is working on. Its comment anticipated this caller in slice 1: *"an agent transitions tickets
that are not theirs."*

**What would falsify this.** If a fourth caller appears that needs a third scoping rule — a
supervisor who sees one team's tickets, say — three query sets stop being duplication and
start being a pattern that does not scale. At that point the answer is a scoping *value*
computed once per request and passed as a parameter the query cannot ignore, not a boolean.
That is a different ADR, and it should be written when the third rule exists rather than
imagined now.
