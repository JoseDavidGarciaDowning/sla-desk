# Checkpoint C — Slice 1 backend

Date: 2026-08-02
Covers: T1 – T11
Gate: **review before Phase 3 (frontend)**

This is the artefact for the checkpoint the plan asks for. It says what exists, what was
decided and why, what is proven and how, and what is deliberately still missing. It is
written to be read by someone deciding whether to let the frontend start.

---

## 1. What the backend does today

A customer authenticated by Clerk can create a ticket and read their own tickets. The
ticket carries an SLA deadline resolved from its priority, and a clock that runs while the
ticket is `open` and stops while it is not.

| Endpoint | Auth | Behaviour |
|---|---|---|
| `GET /health` | none | Reports the database probe; `503 degraded` when it fails |
| `POST /api/webhooks/clerk` | Svix signature | Provisions users from `user.created` / `user.updated` |
| `POST /api/tickets` | Clerk session | Creates a ticket, snapshots the policy, starts the clock |
| `GET /api/tickets` | Clerk session | The caller's tickets, newest first, keyset-paginated |
| `GET /api/tickets/{id}` | Clerk session | The caller's ticket, or `404` |

Not exposed over HTTP yet, but built and tested: the ticket state machine and the
transition write path. The frontend cannot move a ticket until an endpoint is added; the
domain and the store can.

---

## 2. Shape

```
cmd/api                 entrypoint: config, pool, router, graceful shutdown
internal/
  ticket                DOMAIN — statuses, roles, categories, the state machine
  sla                   DOMAIN — the only deadline arithmetic in the system
  store                 sqlc-generated queries + the two transactional repos
  auth                  Clerk verification, RequireAuth, provisioning
  api                   handlers, DTOs, problem documents, the router
  config                environment loading, no globals
db/
  migrations            three goose migrations, all reversible
  queries               the SQL sqlc compiles
```

The rule that holds it together: **`internal/ticket` and `internal/sla` import nothing** —
no database, no HTTP, no framework. It is enforced by a test that walks the import graph
transitively, not by convention. Adding `net/http` to `internal/ticket` fails the build of
`make check`.

---

## 3. The decisions, and where each is written down

### Recorded as ADRs

| | |
|---|---|
| [0001](adr/0001-single-calculation-path-for-the-sla-clock.md) | `sla.Reconstruct` is the only function that computes clock state, and the write order around it is fixed |
| [0002](adr/0002-validation-ownership-between-sla-and-ticket.md) | `internal/sla` validates only its own preconditions; transition legality belongs to `internal/ticket` |
| [0003](adr/0003-health-endpoint-is-not-healthz.md) | The health endpoint is `/health`, because Cloud Run intercepts `/healthz` before the container sees it |

### Taken during the tasks

**CHECK constraints, not native enums.** Measured against Postgres 16: a new enum value
cannot be used in the transaction that adds it, and goose runs every migration in a
transaction — so a migration that adds a value and backfills with it has to be split or
give up atomicity. `ALTER TYPE ... DROP VALUE` does not exist at all. sqlc overrides map
the columns onto `ticket.Priority`, `ticket.Role`, `ticket.Status` and `ticket.Category`,
so the vocabulary exists once instead of once in the domain and once in generated code.

**`sla_consumed_micros`, not `sla_consumed_minutes`.** The spec specified minutes *and* a
consistency test that compares reconstruction to cache exactly; both cannot hold. A ticket
bouncing eight times at 3m40s has consumed 29m20s, which minutes record as 24m — five
minutes of drift on a 60-minute budget. `TIMESTAMPTZ` resolves to one microsecond, so
microseconds round-trip exactly.

**Time comes from Postgres, once per transaction.** The same instant is written to
`sla_clock_started_at`, used to compute `sla_due_at`, and stamped on the history row.
Two reasons: the cache and the history would otherwise differ by however long the insert
took, and the breach worker evaluates `sla_due_at < now()` on the database's clock — a
deadline from an API instance's clock is shifted by that instance's drift, measured at
928µs against a database on the same machine.

**Two invariants are CHECK constraints, not handler logic.**

```sql
CHECK ((status = 'open') = (sla_clock_started_at IS NOT NULL))
CHECK ((sla_clock_started_at IS NULL) = (sla_due_at IS NULL))
```

"A paused ticket cannot breach" is therefore a property of the schema, and holds against
direct SQL as well as against our own code.

**Keyset pagination, not `OFFSET`.** `OFFSET` cannot be stable: a ticket created while
someone is paging shifts every later row down and they see one twice. The cursor carries
`(created_at, id)` — `id` because two tickets created in the same transaction share a
timestamp.

**Errors are RFC 9457 problem documents.** Validation reports every rejected field at
once. A 500 carries no detail at all, because Go error text accumulates driver messages,
table names and connection strings.

**No `ON DELETE CASCADE` anywhere.** §10 forbids hard-deleting a ticket or a history row,
and a cascade does exactly that from a distance.

---

## 4. What is proven, and how

**215 tests.** 2,636 lines of code, 4,757 lines of test.

| Package | Coverage |
|---|---|
| `internal/ticket` | 100% |
| `internal/sla` | 100% |
| `internal/config` | 100% |
| `internal/auth` | 91.1% |
| `internal/api` | 88.6% |

Coverage is the weaker measure. The stronger one is that **every guarantee was verified by
deliberately breaking it**: 61 mutations across the schema, the queries, the handlers, the
wiring and the write path.

| Task | Mutations | Red |
|---|---|---|
| T4 — users, policies, seed | 14 | 14 |
| T6 — tickets, history, indexes | 15 | 15 |
| T8 — the Clerk webhook | 7 | 7 |
| T9 — creating a ticket | 11 | 10 — the clock-source one is not detectable, §5 |
| T10 — reads and wiring | 8 | 8 |
| T11 — the write path | 6 | 6 |

Four of them are the write-path defects ADR 0001 names, and they are the reason the
consistency test exists:

```
cache update skipped                 red
fact and cache in separate txs       red
history read before the insert       red
partial write committed              red
```

The consistency test itself is property-based: fifteen random sequences of up to six
transitions, checking after **every** step that the clock rebuilt from
`ticket_status_history` equals the cached columns exactly. The seed is logged so a failure
can be replayed.

Three things mutation testing found that review had not:

- The `DEFAULT 'customer'` on `users.role` was never exercised, because the upsert writes
  the role explicitly.
- The upsert's conflict branch was not checked for refreshing the name.
- `TestTheTicketRoutesRequireASession` stayed **green** with `RequireAuth` deleted from the
  router, because every handler also refuses a request with no caller. The system failed
  closed — right direction — but the wiring would have been broken with nothing to say so.
  A suite that only asserts requests are *refused* cannot detect missing middleware when
  the handler is defensive too.

---

## 5. Known gaps, deliberate

**The `id` tiebreaker in `ListTicketStatusHistory` has no test.** Rows written in one
transaction share `created_at`, and Postgres returns those ties in insertion order through
every plan a test can provoke — including after an UPDATE that moves the tuple, because a
HOT update leaves the index entry pointing at the original item. It stays because Postgres
guarantees no order for equal sort keys. Correctness is not defined by what a test can
reach.

**The *choice* of clock has no test.** Swapping `TransactionTime` for `time.Now()` leaves
the whole suite green, consistency test included, because the cache and the history would
move together — to the wrong clock. What it breaks is invisible locally: the skew is
sub-millisecond on one machine. The reasoning is recorded at the call site.

**`svix listen` against a real Clerk instance has not been run.** The webhook is fully
tested against signatures this project generates, but the end-to-end delivery from Clerk
has not been exercised. It needs a Clerk instance.

---

## 6. Where the plan was wrong

Two gaps, both found by trying to do the task rather than by reading it.

**Nothing wired the router.** T8 and T9 built handlers that nothing mounted. Without doing
it inside T10, this checkpoint would have been reached with an API still serving only
`/health` in production.

**T11 could not be met as written.** It asks for a property test over generated transition
*sequences*, and slice 1 had no transitions — every history was one row long. The state
machine was pulled forward from slice 3, which is recorded in [spec §2](spec.md#2-scope).
Two things fell out of it: the clock can now pause and resume, which is the domain's
headline behaviour and was untested until then, and `resolved → open` finally had a
definition.

**And one place where the spec contradicted itself.** §4.1's diagram labelled an arrow
"reopen (customer or agent)" and drew it touching `closed`, while the prose states twice
that `closed` is terminal. The prose won. §4.1 now carries an exhaustive edge table so the
next reader does not have to choose between two halves of the same document.

---

## 7. Before this runs again

`internal/config` now **requires** two variables that did not exist before. The API will
not start without them, on purpose: a process that booted without them could only answer
`/health`, and nobody would find out until a user failed to sign in.

```
CLERK_SECRET_KEY=sk_...
CLERK_WEBHOOK_SECRET=whsec_...
```

Local: add them to `.env`. Production: add them to the Cloud Run deploy.

Optional: `CLERK_AUTHORIZED_PARTY` (defaults to `CORS_ALLOWED_ORIGIN`) and `CLERK_API_URL`.

---

## 8. What remains in slice 1

| | |
|---|---|
| T12 | Clerk wiring, customer layout, protected routes — ⚠️ Next 16 renamed `middleware.ts` to `proxy.ts` and dropped edge runtime there; Clerk compatibility is unverified |
| T13 | Create-ticket form |
| T14 | Ticket list and detail with the SLA timer |
| T15 | CI pipeline |
| T16 | Production deploy + README |

Against [spec §11](spec.md#11-success-criteria), the backend half of slice 1 is done: a
customer is provisioned, a ticket is created with a resolved policy and a running clock,
the list returns only the caller's tickets, forged and expired tokens are refused, the
domain packages import nothing, and `make check` is clean. What is outstanding there is
the frontend, the deploy, and the README.

---

## 9. The question for this gate

Is the backend a foundation the frontend can be built on, or is there something here to
change first? Everything above is reversible more cheaply now than after three tasks of UI
depend on it.
