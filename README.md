# SLA Desk

**Live: [sla-desk.josegd.me](https://sla-desk.josegd.me)** — sign up and raise a ticket; the
clock starts immediately.

A customer support desk where every ticket carries a deadline, and that deadline can be
rebuilt from scratch at any time.

Customers raise tickets. Each priority buys a budget of working time — an hour for urgent,
three days for low — and the clock against that budget runs only while the ticket is `open`.
Send it back to the customer and the clock pauses; they reply and it resumes where it left
off.

**Go + chi + sqlc + PostgreSQL · Next.js App Router + TanStack Query · Clerk**

---

## The one idea worth reading

Most systems that track a deadline store it and hope. This one stores the deadline **and the
evidence for it**, and a test proves the two agree.

```
ticket_status_history      the fact.  Append-only. Every status change, with its instant.
tickets.sla_*              a cache.   Derived from the above, and reproducible from it.
```

`sla.Reconstruct(policy, history)` walks the history, accumulates the intervals the ticket
spent `open`, and returns the budget used and the resulting deadline. It is the **only** place
in the system where a deadline is computed. Nothing else adds a duration to a timestamp.

A worked example. A ticket at `normal` priority carries a 24-hour budget:

```
10:00  ─ open                     clock starts
14:00  ─ pending  (we asked)      4h consumed, clock pauses, no deadline exists
16:30  ─ open     (they replied)  clock resumes with 20h left → due at 12:30 tomorrow
```

While the ticket is `pending`, `sla_due_at` is **NULL**. That is not a missing value: a paused
clock has no deadline, so a paused ticket cannot breach. Two `CHECK` constraints in the schema
enforce it, which means it holds against a hand-written `UPDATE` too, not only against our own
code.

Which statuses consume budget is a fact about tickets, so it lives in the ticket domain —
`Status.RunsClock()` — and `internal/sla` asks rather than deciding. Duplicating that rule is
how two answers appear.

### Why keep a cache at all

Because a breach worker has to find the tickets about to miss their deadline, and it cannot do
that by reconstructing every open ticket's clock on every scan. It needs an indexed column to
query. So the deadline is memoised on the ticket row, and `sla_due_at` carries a partial index
for exactly that scan.

### What earns the right to keep it

A cache you cannot check is a second source of truth pretending to be an optimisation. This
one is checked by a property-based test that generates random transition sequences and, after
**every single step**, reads both the stored history and the cached columns back out of the
database, reconstructs the clock from the history, and asserts they are equal.

```
15 random sequences × up to 6 transitions each, seed logged on failure
```

It compares values read back from Postgres rather than values the code just returned, so it
cannot pass by agreeing with itself.

`docs/adr/0001` fixes the write order that makes this hold, and it is not the obvious one:

```
1.  insert the history row                     the fact, first
2.  read the FULL history, including that row
3.  sla.Reconstruct(policy, history)
4.  write the cache
```

Reconstructing before inserting would compute a clock that does not know about the change
being made — a plausible-looking corruption that no unit test of the arithmetic would ever
find, because the arithmetic is right and the input is stale. All four steps run in one
transaction, behind `SELECT … FOR UPDATE`, so two concurrent transitions queue instead of
overwriting each other.

### Microseconds, not minutes

The specification originally said `sla_consumed_minutes`. It contradicted itself: the same
document made the consistency test above mandatory, and a test that compares a reconstructed
`time.Duration` against a column rounded to minutes can never hold.

The drift is not academic. A ticket that bounces eight times, each interval 3m40s, loses
5 minutes to rounding — **8% of an urgent ticket's entire budget**. The column is
`sla_consumed_micros BIGINT`. `BIGINT` rather than `INTEGER` because microseconds overflow
`int32` at about 36 minutes, and the `low` policy budgets 72 hours.

### One clock, read once

Every timestamp in a transition — the history row's `created_at`, the clock start, the
computed deadline — comes from a **single** `now()` read from Postgres at the top of the
transaction. Not from Go.

Measured, this machine: Go and Postgres disagree by about **928µs**. Two Cloud Run instances
disagree by an unbounded amount. If the cache were computed from one clock and the history
stamped with another, the two would differ by that skew, and the consistency test above could
never hold — not because anything is wrong, but because the inputs were never the same.

---

## Identity is not authorization

Clerk answers *who is this*. It never answers *what may they do*.

```
Clerk           →  a verified subject id, and an email
users.role      →  customer | agent | admin        ← ours, in our database
```

A token with a forged `role` claim changes nothing, because nothing reads a role from a token.
`RequireAuth` resolves the verified subject into **our** row and attaches that. There is a test
named for it: `TestRoleComesFromOurTableNotFromTheToken`.

The same principle runs down to the SQL. Scoping is a `WHERE` clause, not a check in Go:

```sql
SELECT * FROM tickets WHERE id = $1 AND requester_id = $2;
```

Another customer's ticket returns **no rows**, so the handler answers `404`. Not `403` — a
`403` would confirm the id names a real ticket, which is exactly what the caller must not
learn. The handler cannot leak it even by accident, because it cannot tell the two cases
apart either.

### The provisioning race, and why there are two paths

A Clerk `user.created` webhook is the primary way a user appears in our database. But a user
can sign up and reach the API before that webhook lands — the two are racing, and the webhook
does not always win.

So `RequireAuth` also provisions, lazily, for any verified subject it has no row for. Both
paths call the same `auth.Provision`, which is an idempotent upsert, so whichever arrives
first wins and the second is a no-op. The upsert also refuses to demote an existing agent back
to `customer`, which a naive one would do on every `user.updated`.

The webhook is mounted **outside** the authenticated group. It carries a Svix signature, not a
session JWT, so behind `RequireAuth` every delivery would be answered `401`, Svix would retry
each one until it gave up, and the primary provisioning path would be silently dead. There is
a test whose only job is to notice that: `TestTheClerkWebhookIsNotBehindRequireAuth`.

---

## How the tests are judged

Coverage measures which lines ran. It says nothing about whether a test would notice if the
line were wrong. So every non-trivial test here has been checked by **mutation**: break the
code deliberately, and confirm a *named* test turns red.

It has repeatedly caught tests that looked strong and proved nothing:

| The mutation | Why it survived |
|---|---|
| Drop `required` from the form's title field | The test submitted an empty form — still blocked by the *other* required fields. It proved *some* field was required, never which |
| Bypass `requester_id` when a **status** filter is present | The scope test only exercised a *priority* filter. The query was scoped for the case the test ran and open for the one it did not |
| Drop the filters from the TanStack query key | Every assertion about the request still passed. The bug is in the cache: each view shows the previous filter's rows until the refetch lands |
| `invalidateQueries(list())` → `invalidateQueries(all)` | `all` is a prefix of `list`, so the list is still invalidated — and the freshly seeded ticket detail is silently invalidated too |

Two of these were data-scoping bugs that a green suite was hiding.

**Some things are deliberately untested, and saying so is part of the record.** Swapping the
Postgres clock for `time.Now()` leaves every test green, because the cache and the history
move together — the comment at that call site says so rather than implying coverage that does
not exist. The `id` tiebreaker in the history ordering is likewise untestable: rows written in
one transaction share a timestamp, and the attempt to expose the tie failed because a HOT
update leaves the index entry pointing at the original tuple. It stays, and the reasoning is
written down.

---

## Deployment

| | |
|---|---|
| Web | **Vercel** — `sla-desk.josegd.me`, a subdomain of the author's own domain |
| API | **Google Cloud Run** — scale to zero, capped at three instances |
| Database | **Neon** — serverless Postgres |
| Identity | **Clerk** — a production instance on `clerk.sla-desk.josegd.me` |

Every component sits on a permanent free tier, chosen for that rather than for convenience —
the comparison against Heroku, Fly.io, Render, Koyeb, Railway and Oracle is in
[tasks/plan.md](tasks/plan.md). Cloud Run is deployed with `--min-instances=0` and
`--max-instances=3`, and a billing alert at $2, because a misconfiguration on a card-on-file
account bills real money.

A Clerk **production** instance needs DNS records on a domain you own — Clerk cannot issue
them for a `*.vercel.app` address. That is why the app lives on a subdomain rather than on
the Vercel URL, and it is worth knowing before planning a deployment around one.

---

## Running it

Everything from a fresh clone — the four processes, both environment files, and the traps that
have actually caught someone — is in **[docs/local-development.md](docs/local-development.md)**.

```bash
make up          # Postgres and Redis
make migrate-up
make api         # :8080
cd web && pnpm dev   # :3000
```

`goose`, `sqlc` and `golangci-lint` are pinned as tool dependencies in `go.mod`. A fresh clone
needs nothing installed beyond Go, and CI lints with the same version you do.

```bash
make check       # lint + tests + build. Must pass before every commit
make test-int    # integration tests, against a real Postgres
```

### The generated contract

`web/lib/contract.ts` is written by `cmd/gencontract` from the same declarations
`internal/api` validates against, and a Go test fails while it is stale.

The frontend needs the category and priority vocabularies to render its selects at all — that
copy is not optional. The only question was whether it is generated or transcribed. Beyond
that the form validates almost nothing: `required` and `maxlength` are the browser's own, and
everything else is decided by the API, whose RFC 9457 field errors are rendered verbatim.

---

## What is deliberately not built

This is slice one: the customer's path, end to end, deployed. Everything below is designed for
and intentionally absent, rather than forgotten.

| Not built | Why not yet |
|---|---|
| Comments and internal notes | Slice 2. The public-reply / internal-note split is the point, and it needs the agent experience to exist first |
| Agent dashboard, assignment, escalation | Slice 2–3. The RBAC matrix exists; nothing exposes it |
| Status transitions over HTTP | The state machine and the store path are built and tested. No endpoint exposes them yet — a customer cannot move their own ticket in slice 1 |
| Attachments, signed URLs | Slice 4 |
| SLA breach worker | Slice 5. The index it will scan exists, and `sla_breached_at` is already read by the API |
| Real-time updates | Slice 6, and it needs an answer to the dual-write problem first (`docs/spec.md` §12) |
| Full-text search, saved filters | Slice 7 |
| Rate limiting | Slice 9. Deliberately *not* faked with `X-Forwarded-For`, which is spoofable — a limit that can be bypassed with a forged header is worse than none, because it looks like protection |
| End-to-end browser test | T17, against the deployed URL rather than a stack assembled in a runner |

---

## Decisions worth reading

| | |
|---|---|
| [ADR 0001](docs/adr/0001-single-calculation-path-for-the-sla-clock.md) | One calculation path for the SLA clock, and the write order that makes the cache checkable |
| [ADR 0002](docs/adr/0002-validation-ownership-between-sla-and-ticket.md) | Which malformed inputs `internal/sla` rejects, and which belong to someone else |
| [ADR 0003](docs/adr/0003-health-endpoint-is-not-healthz.md) | Why the health endpoint is `/health` — Cloud Run intercepts `/healthz` before the request reaches the container |
| [ADR 0004](docs/adr/0004-one-way-to-report-an-http-failure.md) | One way to report an HTTP failure, and why the package is not called `shared` |

| | |
|---|---|
| [docs/spec.md](docs/spec.md) | What is being built, and the success criteria it is judged against |
| [docs/clerk-integration.md](docs/clerk-integration.md) | The two request paths, `azp` versus CORS, and the silent 401 |
| [docs/local-development.md](docs/local-development.md) | Running it, and the traps |
| [tasks/todo.md](tasks/todo.md) | Every task, with what each one actually taught |

---

## Structure

```
cmd/api            the HTTP entrypoint
cmd/gencontract    writes the frontend's vocabulary from the API's own declarations

internal/ticket    DOMAIN. The state machine. No database, no HTTP
internal/sla       DOMAIN. The only deadline arithmetic in the system
internal/httperr   what a failure looks like on the wire. Imports nothing else here
internal/store     sqlc output plus the hand-written transactional repositories
internal/auth      Clerk verification, and the resolution of a subject into one of our users
internal/api       handlers, DTOs, request validation
internal/config    environment loading, no globals

web/               Next.js App Router
db/migrations      goose
db/queries         sqlc source
```

`internal/ticket` and `internal/sla` import nothing from `store`, `api`, `database/sql` or
`net/http`. `internal/httperr` imports nothing from this module at all — it has to stay below
every layer that reports an error, or the next layer that needs it finds it out of reach.

Both rules are enforced by a test that walks the import graph, not by convention.
