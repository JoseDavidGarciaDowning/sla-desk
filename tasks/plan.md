# Implementation Plan: Slice 1 — Customer creates and tracks a ticket

Status: **Draft — awaiting review (Phase 2 gate)**
Spec: [`docs/spec.md`](../docs/spec.md)
Last updated: 2026-07-31

---

## Overview

Slice 1 opens one complete path from database to production: a customer signs up through
Clerk, is provisioned into our `users` table, creates a ticket, has an SLA policy resolved
and a clock started, and sees their own tickets — deployed and publicly reachable.

No agents, no comments, no attachments, no WebSockets, no breach worker, no search. Those
are slices 2–9 and are out of scope here (spec §2).

---

## Architecture Decisions

### Deploy targets — resolves spec §12 open question 5

Every component sits on a **permanent** free tier — not a trial, not a student credit, and
nothing that expires on a date. Verified 2026-07-31.

| Component | Provider | Free allowance |
|---|---|---|
| Web (Next.js) | **Vercel** Hobby | Free for non-commercial projects |
| API (Go) | **Google Cloud Run** | 2M requests, 180,000 vCPU-seconds, 360,000 GB-seconds, 1 GB egress from North America — per month. *"The free tier has no end date"* |
| Breach checker | **Cloud Scheduler** → authenticated endpoint on the same Cloud Run service | Consumes the request budget above; no second service |
| Postgres | **Neon** free | 0.5 GB per project, 100 CU-hours. *"The Free plan is permanent (not a trial); no credit card required"* |
| Redis | **Upstash** free | 256 MB, 500K commands/month, 10 GB bandwidth. Permanent, no credit card. Not provisioned until slice 5 |

#### The Heroku plan is dead — student credit already expired

The previous revision of this plan used Heroku funded by the GitHub Students offer. That
credit has already been consumed on this account and *"Previous participants... cannot
reapply."* Heroku has no free tier otherwise, so the entire allocation collapses.

#### Why Cloud Run rather than the obvious alternatives

| Option | Verdict — all verified 2026-07-31 |
|---|---|
| **Heroku** | No free tier since 2022. Student credit expired. Eliminated |
| **Fly.io** | *"Fly.io no longer offers plans to new customers."* Legacy organizations keep their allowance; new ones get none. ~$2.02/mo per `shared-cpu-1x` 256 MB machine |
| **Render** free | Three disqualifiers: background workers *"don't support Free instances"* at all; free Postgres *"expire 30 days after creation"* and is then deleted; free web services take ~1 minute to wake after 15 minutes idle |
| **Koyeb** | Acquired by Mistral AI in February 2026. New users can no longer sign up for the free tier. Eliminated |
| **Railway** | $5 one-time trial credit expiring in 30 days, then $1/month — not enough for an always-on service. Hobby is $5/mo |
| **Oracle** Always Free | Genuinely permanent, but halved to 2 OCPU / 12 GB in June 2026 with no announcement, ARM capacity is frequently unavailable, and it is a bare VM: you would own Docker, TLS renewal, Postgres backups and OS patching. That is an operations detour away from the architecture this project exists to demonstrate |

#### Slice 6 — Cloud Run makes the WebSocket problem better, not worse

Cloud Run supports WebSockets with no extra configuration, up to a **60-minute** request
timeout and 1000 concurrent connections per instance. The documented caveat:

> *"clients connecting to your Cloud Run service might end up being serviced by different
> instances that do not coordinate or share data"* — external state such as Redis Pub/Sub
> is required to synchronise them.

This is exactly what spec §3 already allocated Redis for. The platform enforces the design
that was already correct. Two consequences worth stating in the README:

- **Fan-out through Redis Pub/Sub is mandatory, not optional.** Any single-instance
  in-memory hub would break the moment Cloud Run autoscales.
- **The client must implement reconnection**, because the 60-minute ceiling guarantees
  disconnects. Real WebSocket clients need this regardless; the platform just removes the
  option of pretending otherwise.

#### Slice 5 — the breach checker becomes a third deployment mode

With no worker process available, Cloud Scheduler calls an authenticated
`POST /internal/breach-check` on the existing Cloud Run service.

The breach logic therefore lives in `internal/sla/breach` as a package, with three thin
callers and **no change to the logic between them**:

| Caller | Shape | Used by |
|---|---|---|
| `cmd/worker` | Long-running ticker | A host with worker processes |
| `cmd/breachcheck` | Run once, exit | A scheduler that spawns one-off processes |
| `POST /internal/breach-check` | HTTP handler | Cloud Scheduler, today |

The endpoint must be authenticated (OIDC token or shared secret) and must **never** be
reachable by a normal user. It is idempotent: the query selects *every* overdue ticket, so
a missed or duplicated invocation is self-correcting.

**Rejected on Cloud Run: an in-process `time.Ticker` goroutine.** This is the obvious
fourth shape — run the check on a ticker inside the API process, no extra service. It
works on a VM, a Heroku dyno, or Oracle. On Cloud Run it silently does not run:

| Cloud Run behaviour | Effect on the goroutine |
|---|---|
| Scales to zero with no traffic | No instance, no process, no goroutine |
| CPU allocated during requests only | Between requests the goroutine gets almost no CPU; the ticker does not fire |
| Instances are ephemeral | The container can be killed mid-check at any time |

The failure mode is the dangerous kind: no error, no log, no alarm — the SLA check simply
never happens. On Cloud Run the trigger **must** come from outside, so that the work runs
inside a real request.

This is the concrete justification for keeping breach logic in a package rather than in a
`main()` or a handler. The deploy target changed four times while this plan was written,
and the correct execution shape changed with it three times. The logic did not.

#### The cost of this stack — stated plainly

Cloud Run's always-free tier *"requires an active billing account, though not necessarily a
paid one."* A card must be on file, which means **misconfiguration can produce a real
bill**. Mandatory guardrails, all set in T3:

- `--min-instances=0` — a warm instance leaves the free tier immediately
- `--max-instances` capped to a small number, so a traffic spike or a loop cannot scale out
- CPU allocated **during requests only**, never "always allocated"
- A GCP budget alert at a low threshold, e.g. $1

Neon and Upstash need no card at all.

### Deploy a do-nothing application first (Task 3)

The third task deploys a health endpoint and an empty page. Nothing else.

This looks like wasted effort. It is the opposite. Deployment is where portfolio projects
die: everything works locally, then two weeks of accumulated code meets its first
`Dockerfile`, its first managed-Postgres SSL requirement, its first CORS failure between
two different domains — all at once, with no way to tell which change broke what.

Proving the deployment path while there is nothing to deploy means every later task ships
against a pipeline that is already known to work. **High-risk work goes first.**

### Domain before infrastructure (Task 5 before Task 7)

`internal/sla` is written and fully tested before any HTTP handler exists. It has no
database and no `context.Context` — pure functions over `(policy, history, now)`. It runs
with no Docker container up.

The SLA clock is the single most likely place for a subtle, silent bug in this project. It
gets tested in isolation, exhaustively, before anything can hide it.

### `ticket_status_history` is included in Slice 1 — a deliberate scope call

The state machine is Slice 3. But the **history table** and the initial `NULL → open` row
ship now, in the same transaction as ticket creation.

Reason: spec §4.2 makes history the fact and the `tickets.sla_*` columns a derived cache.
If tickets exist before history does, that invariant is false from day one and has to be
backfilled. One extra `INSERT` now avoids a data migration later.

What is *not* in Slice 1: transitions, `Transition()`, and any status change after creation.

### Test-first, per spec §9

The project runs in strict TDD mode. Every task below writes the failing test first.

---

## Dependency Graph

```
T1 repo + compose + go module + health endpoint
 ├──▶ T2 Next.js scaffold
 │     └──▶ T3 DEPLOY WALKING SKELETON ◀── de-risks everything downstream
 │           │
 ├──▶ T4 migration 001 (users, sla_policies) + sqlc setup + seed
 │     └──▶ T6 migration 002 (tickets, ticket_status_history) + queries
 │           │
 └──▶ T5 internal/sla  (pure domain — no dependency on ANY of the above)
             │
             ▼
       T7 Clerk JWT verify + RequireAuth + lazy upsert   ◀── needs T4
             ├──▶ T8 Clerk webhook (Svix)
             └──▶ T9 POST /api/tickets                    ◀── needs T5 + T6
                   └──▶ T10 GET list + GET by id
                         └──▶ T11 consistency + architecture tests
                               │
                               ▼
                         T12 Next.js + Clerk wiring       ◀── needs T3 + T7
                               └──▶ T13 create ticket form
                                     └──▶ T14 list + detail
                                           │
                                           ▼
                                     T15 CI  ──▶ T16 ship + README
```

T5 is independent of everything. It can be done in parallel with T1–T4 by a second
session, or first if you want the hardest thinking out of the way while you are fresh.

---

## Task List

### Phase 0: Rails and a proven deployment path

- [ ] **T1** — Repo, Docker Compose, Go module, health endpoint
- [ ] **T2** — Next.js App Router scaffold
- [ ] **T3** — Deploy the walking skeleton to production

#### Checkpoint A — after T3
- [ ] `make up` brings Postgres and Redis up locally
- [ ] `make api` serves `GET /healthz` → `200`
- [ ] The Next.js app is live on a Vercel URL
- [ ] The Go API is live on a Cloud Run URL and reachable from the browser (CORS verified)
- [ ] The API connects to Neon over TLS
- [ ] All four cost guardrails are set and verified: `min-instances=0`, `max-instances` capped, CPU during requests only, budget alert active
- [ ] Cold start measured and the number written into this plan
- [ ] **Review with human before proceeding**

### Phase 1: Domain and data

- [ ] **T4** — Migration 001 (`users`, `sla_policies`) + sqlc setup + seed policies
- [x] **T5** — `internal/sla`: `Schedule`, `Always24x7`, `Policy`, deadline arithmetic ✅ 100% coverage, mutation-checked
- [ ] **T6** — Migration 002 (`tickets`, `ticket_status_history`) + sqlc queries

#### Checkpoint B — after T6
- [ ] `make migrate-up` and `make migrate-down` both run clean
- [ ] `make sqlc` generates without error and the result compiles
- [ ] `internal/sla` unit tests pass with no Docker running
- [ ] `internal/sla` coverage ≥ 90%

### Phase 2: Auth and the tickets API

- [ ] **T7** — Clerk JWT verification, `RequireAuth`, idempotent lazy upsert
- [ ] **T8** — `POST /api/webhooks/clerk` with Svix signature verification
- [ ] **T9** — `POST /api/tickets`
- [ ] **T10** — `GET /api/tickets` and `GET /api/tickets/{id}`
- [ ] **T11** — SLA consistency test + architecture boundary test

#### Checkpoint C — after T11
- [ ] The full API flow works against a real Postgres via integration tests
- [ ] Customer B receives `404` (not `403`) for customer A's ticket
- [ ] Missing, expired, and forged tokens all receive `401`
- [ ] The clock reconstructed from history equals the cached value, property-tested
- [ ] `internal/sla` and `internal/ticket` import nothing from `store`, `api`, or `database/sql`
- [ ] **Review with human before proceeding**

### Phase 3: Frontend

- [ ] **T12** — Clerk wiring, customer layout, protected routes
- [ ] **T13** — Create-ticket form with validation
- [ ] **T14** — Ticket list and detail with SLA display

#### Checkpoint D — after T14
- [ ] Sign up → create ticket → see it in the list works in the browser
- [ ] Loading, empty, and error states exist on every view
- [ ] Filters live in URL query params, not component state

### Phase 4: Ship

- [ ] **T15** — CI: lint + test on every push
- [ ] **T16** — Production deploy of the real application + README

#### Checkpoint E — Slice 1 complete
- [ ] Every success criterion in spec §11 is checked
- [ ] `make check` passes clean
- [ ] The application is live and a stranger can sign up and create a ticket
- [ ] The README explains the SLA clock model and the fact-vs-cache decision

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| SLA clock has a subtle arithmetic bug that stays silent for months | **High** | Pure domain in T5, no I/O. Property-based tests over generated transition sequences. Consistency test in T11 reconstructs from history and asserts equality |
| `WithHeaderAuthorization` does not reject unauthenticated requests — endpoints silently open | **High** | Documented in spec §4.3. T7 writes the failing `401` test *first*, for missing, expired, and forged tokens |
| Deployment surprises pile up and land all at once at the end | **High** | T3 deploys the walking skeleton before any real code exists |
| Provisioning race: valid JWT, no `users` row, first page load fails intermittently | **High** | Resolved in spec §4.5. T7 implements the lazy upsert; T8 adds the webhook. Test asserts a first request with an unknown-but-valid subject succeeds |
| Authorization leak: a forgotten handler check exposes another customer's ticket | **High** | Spec §4.3 pushes scoping into SQL (`WHERE requester_id = $1`). T10's acceptance requires `404`, not `403` |
| Scope creep from slices 2–9 (attachments, WebSockets, agent dashboard) | **High** | Spec §2 lists Slice 1 exclusions explicitly. Anything not in this file does not get built |
| Clerk Go SDK API differs from expectation | Medium | Verified against `/clerk/clerk-sdk-go` docs before T7. Re-verify at implementation time rather than trusting memory |
| Svix Go verification API unconfirmed | Medium | Only the library and headers are confirmed. T8 starts by reading the Go docs, not by writing code |
| A Cloud Run misconfiguration produces a real bill on the card that must be on file | **High** | T3 sets `--min-instances=0`, a low `--max-instances` cap, CPU-during-requests-only, and a GCP budget alert at $1. Verify all four before the first deploy, not after |
| Cold start on scale-to-zero makes the portfolio link feel broken | Medium | **Magnitude unverified.** T3 measures it directly and records the number. Go binaries start fast, but "fast" is not a number. If it turns out unacceptable, the tradeoff is a warm instance and leaving the free tier |
| 1 GB/month egress is the tightest limit in the stack | Medium | Vercel serves all static assets; the API returns JSON only. Add a Cloud Monitoring alert on egress in T3 |
| Google changes the always-free tier | Low | Documented as *"no end date"* with 30 days' notice for changes. Neon and Upstash are card-free fallbacks if it ever moves |
| sqlc / goose configuration friction | Low | Isolated in T4, early, with nothing depending on it yet |

---

## Parallelization

- **Independent:** T5 (`internal/sla`) shares nothing with T1–T4. Start it any time.
- **Strictly sequential:** T4 → T6 (migrations must be ordered), T9 → T10 → T11.
- **Needs a contract first:** T13/T14 depend on the response shape from T9/T10. Freeze the
  DTOs when T9 lands, then frontend and backend can move independently.

---

## Open Questions

Carried from spec §12; none block Slice 1.

1. **Dual write on WebSocket emission** — Slice 6. Leaning outbox table.
2. **Clock behavior on reopen from `resolved`** — Slice 5.
3. ~~User provisioning~~ — resolved, spec §4.5.
4. **Agent role assignment** — Slice 2. Seed migration or admin endpoint?
5. ~~Deploy provider~~ — resolved above.
