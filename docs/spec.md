# Spec: Customer Support & SLA Desk

Status: **Draft — awaiting review (Phase 1 gate)**
Last updated: 2026-07-31

---

## 1. Objective

A support desk with two distinct experiences around a single ticket domain:

- **Customers** open tickets and follow their own incidents.
- **Agents** triage, assign, reply, escalate, filter, and manage SLA deadlines.

**Why this domain:** it is narrow enough to model correctly and rich enough to
demonstrate real business rules. The hiring signal is not the feature count — it is
the ownership of auth, data modeling, a state machine, background work, real-time
delivery, and documented tradeoffs.

**Definition of success:**

- A deployed, publicly reachable application, not a local demo.
- A ticket cannot reach an invalid state through any path, including direct API calls.
- The SLA clock is provably correct: reconstructing it from state history matches the
  stored value for every ticket.
- A reader can open this file and understand every non-obvious decision and its tradeoff.

**Non-goals:** this is not a Jira/Zendesk clone. No projects, sprints, boards, custom
fields, workflows-as-configuration, or a plugin system.

---

## 2. Scope

### Slice 1 — the first deployable vertical slice

Ships end to end (DB → API → UI → production) before anything else is started.

- Customer sign-up / sign-in via Clerk.
- Clerk user provisioned into our `users` table with a role, via webhook plus a lazy
  upsert fallback (§4.5).
- Create a ticket: title, description, category, priority.
- SLA policy resolved from priority; clock starts.
- The ticket state machine and `ticket_status_history`, with the clock pausing in
  `pending`. **Pulled forward from slice 3 during T11** — the consistency test this project
  owes itself is over transition *sequences*, and a slice with no transitions cannot
  produce one.
- Customer ticket list and ticket detail (own tickets only).
- Deployed and reachable.

**Explicitly NOT in Slice 1:** agent dashboard, comments, attachments, WebSockets,
breach worker, search, tags, audit log, rate limiting.

### Roadmap after Slice 1

Each slice is deployable on its own. Order is by dependency, not by interest.

| Slice | Content | Unlocks |
|---|---|---|
| 2 | Agent role, agent ticket list, assignment | RBAC beyond ownership |
| ~~3~~ | ~~Ticket state machine + status history~~ | **Delivered in slice 1** — see above |
| 4 | Comments: public replies vs internal notes | Visibility rules |
| 5 | SLA clock pause/resume + breach worker | Background work |
| 6 | WebSocket updates, post-commit emission | Real-time + the dual-write problem |
| 7 | Attachments via signed URLs | Object storage |
| 8 | Full-text search, saved filters, tags | Query surface |
| 9 | Audit log, rate limiting, observability | Production hardening |

---

## 3. Tech Stack

Exact versions are pinned in `go.mod` / `package.json` at setup time; this table
records the choice and its reason.

| Layer | Choice | Reason |
|---|---|---|
| API language | Go 1.22+ | Explicit errors, cheap concurrency for the worker, portfolio differentiation |
| Router | `go-chi/chi` v5 | `net/http`-compatible, no framework lock-in. **Not** Gin, **not** Echo |
| DB access | `sqlc` | Generated type-safe Go from real SQL. No ORM — the point of choosing Go |
| Migrations | `pressly/goose` | Plain SQL up/down, no DSL |
| Database | PostgreSQL 16 | Ticket flow is relational; also gives us FTS and `LISTEN/NOTIFY` |
| Cache / queue | Redis 7 | Breach queue, rate limiting, WebSocket fan-out |
| Identity | Clerk (`clerk-sdk-go/v2`) | Identity and session only — see §4.3 |
| Frontend | Next.js App Router + TypeScript | Server components for the shell, client components for live data |
| Server state | TanStack Query | Cache is the integration point for real-time events |
| Client state | Zustand | Filters, sidebar, connection status — nothing that belongs to the server |
| Styling | Tailwind CSS + shadcn/ui | |
| Object storage | Cloudflare R2 (S3 API) | Signed URLs; S3-compatible keeps it portable |
| Local infra | Docker Compose | Postgres + Redis. Nothing installed on the host |

**Deploy target — every component on a permanent free tier:** web on **Vercel** (Hobby);
Go API on **Google Cloud Run** (2M requests/month, *"no end date"*); Postgres on **Neon**
free (permanent, no card); Redis on **Upstash** free (permanent, no card, slice 5 onward);
the SLA breach check triggered by **Cloud Scheduler** against an authenticated endpoint on
the Cloud Run service. Rationale, cost guardrails and rejected alternatives:
`tasks/plan.md`.

---

## 4. Domain Rules

This section is the heart of the spec. Everything else is plumbing.

### 4.1 Ticket state machine

States: `open`, `pending`, `resolved`, `closed`.

```
              ┌────────── customer replies, or agent reopens ─────────┐
              │                                                       │
              ▼                agent sets pending                     │
 create   ┌────────┐ ───────────────────────────────────────▶ ┌─────────┐
 ───────▶ │  open  │                                          │ pending │
          └────────┘                                          └─────────┘
            ▲    │                                                  │
            │    │ agent resolves                    agent resolves │
   reopen   │    ▼                                                  │
            │  ┌──────────┐ ◀───────────────────────────────────────┘
            └─ │ resolved │
               └──────────┘
                    │
                    │ agent closes
                    ▼
               ┌──────────┐
               │  closed  │   terminal — nothing leaves
               └──────────┘
```

The edges, exhaustively, with who may take each:

| From | To | Roles | Why |
|---|---|---|---|
| `open` | `pending` | agent, admin | Waiting on the customer; the clock pauses |
| `open` | `resolved` | agent, admin | Answered |
| `pending` | `open` | **customer**, agent, admin | The customer replied; the clock resumes |
| `pending` | `resolved` | agent, admin | Answered while waiting |
| `resolved` | `open` | **customer**, agent, admin | Reopened — the answer did not land |
| `resolved` | `closed` | agent, admin | Finished |
| `closed` | — | — | Terminal |

A customer never calls an endpoint that sets a status. The two edges they hold are
consequences of replying and of reopening, which is what §4.3's "only implicitly, by
replying" means.

> **Corrected 2026-08-02 (T11).** An earlier version of the diagram labelled an arrow
> "reopen (customer or agent)" and drew it touching `closed`, which contradicts the
> terminality stated twice below it. The edge is `resolved → open`. The table above exists
> so the next reader does not have to decide which of the two the code should follow.

Rules:

- `closed` is **terminal**. A closed ticket is never reopened; a new ticket is created
  and linked. This is deliberate: a terminal state makes the SLA clock and the audit
  trail unambiguous.
- Any transition not drawn above is rejected with `409 Conflict`, including transitions
  attempted directly against the API.
- Every accepted transition writes exactly one `ticket_status_history` row **in the same
  transaction** as the ticket update. No history row, no transition.
- The transition function is pure and lives in `internal/ticket`. It takes
  `(current, target, actor role)` and returns the new state or an error. It touches no
  database.

### 4.2 SLA clock

**Model:** a 24/7 clock that **pauses while the ticket is in `pending`** (waiting on the
customer). The SLA measures *team response time*, not calendar time — time spent waiting
on the customer does not consume budget.

The clock runs **only** in `open`. It is paused in `pending`, `resolved`, and `closed`.

**The budget is data, not code.** It comes from `sla_policies`, keyed by priority. No
`switch` on priority anywhere in the codebase.

**All deadline arithmetic lives in one module** (`internal/sla`). HTTP handlers, the
breach worker, and the frontend consume the resulting `due_at` and nothing else. None of
them recompute anything.

#### The source-of-truth decision

`ticket_status_history` is the **fact**. The SLA columns on `tickets` are a **derived
cache**.

| | Fact | Cache |
|---|---|---|
| Where | `ticket_status_history` | `tickets.sla_*` |
| Written | On every transition, same transaction | Same transaction, recomputed from the transition |
| Read by | Recalculation / audit / consistency test | Breach worker, API responses, UI |

Why both, when the history alone is sufficient: making the worker aggregate the full
history of every open ticket on each run does not scale and does not index. The cache
gives the worker a single indexed predicate. The history gives us the ability to rebuild
every ticket from zero when a policy changes.

The obligation this creates: **a consistency test that reconstructs the clock from
history and asserts it equals the cached value.** Divergence is a bug. This test is
non-optional — it is what makes the double representation safe.

#### Cached fields on `tickets`

| Column | Meaning |
|---|---|
| `sla_policy_id` | Policy snapshotted at creation. Changing a policy does not silently move old deadlines |
| `sla_consumed_micros` | Accumulated running time, closed intervals only, in microseconds |
| `sla_clock_started_at` | Non-NULL **iff** the clock is running (i.e. status is `open`) |
| `sla_due_at` | `sla_clock_started_at + (budget − consumed)`. Non-NULL iff running |
| `sla_breached_at` | Set once by the worker; never recomputed by request handlers |

**Why microseconds and not minutes.** This column was specified as
`sla_consumed_minutes` until T6. It cannot be: the consistency test in §9 requires the
reconstruction to equal the cache *exactly*, and `sla.Reconstruct` returns a
`time.Duration`. Storing minutes forces that test to carry a ±1 minute tolerance, which
makes it blind to precisely the class of bug it exists to catch.

The error is not academic. A ticket that bounces between `open` and `pending` eight times
at 3m40s per interval has really consumed 29m20s; truncated to minutes the cache records
24m. On an `urgent` budget of 60 minutes the deadline lands over five minutes late and the
breach worker fires at the wrong time.

Microseconds are exact rather than merely finer. `ticket_status_history.created_at` is
`TIMESTAMPTZ`, whose resolution is one microsecond, so every duration the reconstruction
can produce is a whole number of microseconds. Round-tripping through a `BIGINT` of
microseconds loses nothing, and the drift is zero for any number of transitions. Seconds
would reintroduce truncation with no bound on the accumulated error.

**A paused ticket has `sla_due_at IS NULL` and therefore cannot breach.** That falls out
of the model rather than being a special case in the worker. Both halves of it are CHECK
constraints on `tickets`, so the invariant holds against direct SQL as well as against
our own handlers.

Transitions:

- Entering `open`: `sla_clock_started_at = now()`, recompute `sla_due_at`.
- Leaving `open`: `sla_consumed_micros += now() − sla_clock_started_at`, then set
  `sla_clock_started_at = NULL` and `sla_due_at = NULL`.

Breach worker query — one indexed predicate:

```sql
SELECT id FROM tickets
WHERE sla_due_at < now()
  AND sla_breached_at IS NULL;
```

#### Designed-for extension: business hours

A future policy must be able to choose between 24/7 and a defined business schedule
(weekdays, time window, timezone, holidays) **without changing any caller**. The seam:

```go
// internal/sla — the only place deadline arithmetic exists.
type Schedule interface {
    // Elapsed returns budget-consuming time between two instants.
    Elapsed(from, to time.Time) time.Duration
    // DueAt returns when `remaining` budget will be exhausted starting at `from`.
    DueAt(from time.Time, remaining time.Duration) time.Time
}

type Policy struct {
    ID       int64
    Priority Priority
    Budget   time.Duration
    Schedule Schedule // Always24x7 today; BusinessHours later
}
```

`Always24x7` is trivial arithmetic. `BusinessHours` iterates working windows. Callers
never learn which one they got. **Slice 5 implements `Always24x7` only** — the interface
exists so the later change is additive, not a refactor.

Default policies (seeded, editable as data):

| Priority | Budget |
|---|---|
| `urgent` | 60 min |
| `high` | 240 min |
| `normal` | 1440 min |
| `low` | 4320 min |

### 4.3 Authentication and authorization

**The split is the whole point:** Clerk answers *who you are*. Our API answers *what you
may do*.

- Clerk issues the session JWT. The Go API verifies it against Clerk's JWKS.
- The **role lives in our `users` table**, never in Clerk metadata. A client-supplied
  token can never assert a role.
- Roles: `customer`, `agent`, `admin`.

**Documented trap** (verified against `clerk-sdk-go/v2` docs, 2026-07-31):
`clerkhttp.WithHeaderAuthorization()` attaches session claims to the request context but
**does not reject unauthenticated requests**. Mounting it and assuming the route is
protected leaves the endpoint open. Our own `RequireAuth` middleware runs after it and
performs the rejection.

RBAC matrix:

| Action | customer | agent | admin |
|---|---|---|---|
| Create ticket | ✅ | ✅ | ✅ |
| Read own ticket | ✅ | ✅ | ✅ |
| Read any ticket | ❌ | ✅ | ✅ |
| Assign ticket | ❌ | ✅ | ✅ |
| Transition status | ❌ (only implicitly, by replying) | ✅ | ✅ |
| Post public reply | ✅ | ✅ | ✅ |
| Post internal note | ❌ | ✅ | ✅ |
| Read internal notes | ❌ | ✅ | ✅ |
| Manage SLA policies | ❌ | ❌ | ✅ |

**Assignment is flat, and that was never decided until now.** Both `agent` and `admin`
carry `Assign ticket`, so any agent may hand any ticket to any other — there is no lead,
no supervisor and no queue owner. The matrix has said this since it was written, but
nobody chose it: the question of *who* may assign to *whom* was never asked, and the row
above is the answer that fell out of not asking.

Recorded here as a decision so it stops being an accident. The flat model is the ordinary
one for a desk this size — an agent picks up work, or hands it to whoever knows the area —
and it assumes a team small enough to trust with that. The alternative, where a lead
distributes the queue and agents only take what they are given, buys load balancing at the
cost of a role, a guard and a second bootstrap path.

`admin` is where a hierarchy would go if one is ever wanted: it already exists, already has
its own column, and differs from `agent` in exactly one row today (`Manage SLA policies`,
slice 9). Restricting assignment to it would move one ✅ rather than redesign anything.

Authorization is enforced **in the data layer, not only in handlers**: customer-scoped
queries carry `WHERE requester_id = $1`. A missing handler check must not be sufficient
to leak another customer's ticket.

### 4.4 Comment visibility

- `visibility`: `public` | `internal`.
- Customers can never read, write, or infer the existence of `internal` comments —
  they are excluded from the query, not filtered in the response.
- Comments are **soft-deleted** (`deleted_at`), never hard-deleted. Body text is
  immutable once written; a correction is a new comment.

### 4.5 User provisioning (Clerk → our database)

A Clerk identity must become a row in `users` before any authorization decision can be
made, because the role lives in our database (§4.3).

**Primary path: `user.created` / `user.updated` webhook.** Clerk posts to
`POST /api/webhooks/clerk` on the Go API directly — not through Next.js, which would add
a hop for no reason.

Clerk signs webhooks with **Svix**, not with a Clerk-specific scheme. There is no Go
equivalent of the JS `verifyWebhook` helper (verified 2026-07-31: Clerk's helper ships
only for JS frameworks). In Go we verify the `svix-id` / `svix-timestamp` /
`svix-signature` headers with `github.com/svix/svix-webhooks/go`. Local development uses
the Clerk CLI (`npx clerk@latest webhooks listen`) to relay deliveries to localhost — no
ngrok required, and it supersedes the `svix listen` this section originally named.

**Setup, the two request paths, and the failure modes that produce a silent `401` are
documented in [clerk-integration.md](clerk-integration.md).**

**The race this introduces.** The webhook is an independent HTTP request from Clerk's
servers to ours. The browser, meanwhile, already holds a valid JWT the instant signup
completes:

```
signup completes ──▶ browser gets valid JWT ──▶ GET /api/tickets  (t+50ms)
                                                      │
                                                      ▼
                                            JWT valid, users row MISSING
       Clerk servers ──▶ POST /api/webhooks/clerk  (t+200ms … or t+30s on retry)
```

Every new user's first page load can fail, intermittently. Unacceptable.

**Resolution: webhook primary, idempotent lazy upsert as fallback.** Both paths call the
same function:

```sql
INSERT INTO users (clerk_user_id, email, name, role)
VALUES ($1, $2, $3, 'customer')
ON CONFLICT (clerk_user_id) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name
RETURNING *;
```

`RequireAuth` calls it when a verified token resolves to no local user. Whichever path
arrives first wins; the other is a no-op.

This is not indecision. The webhook handler **must** be idempotent regardless, because
Svix retries any non-2xx response — so the upsert gets written either way. Reusing it in
the middleware costs a few lines and removes the race entirely.

**Role is never taken from the webhook payload or the JWT.** New users are always seeded
as `customer`. Promotion to `agent` / `admin` is a separate, privileged path (Slice 2).

### 4.6 Tenancy

**B2C.** A customer is an individual. There is no organization entity. A customer sees
their own tickets and nothing else.

This is a deliberate, recorded decision: retrofitting multi-tenancy touches every query,
index, and permission check. We are choosing not to have it rather than deferring it.

### 4.7 Ticket categories

`category` was listed as a ticket field from the start without its values ever being
defined. They are: `billing`, `technical`, `account`, `other`.

A fixed set constrained by the database, not free text. The agent dashboard filters and
groups by category, and free text turns that into guesswork the moment `Billing`,
`billing` and `facturación` coexist. Widening the set is a one-line migration.

Not a `categories` table with a foreign key: unlike SLA budgets, which the product exists
to let people tune, categories change on the order of never. The extra table, join and
admin CRUD would buy nothing in this slice.

---

## 5. Data Model (initial sketch)

Refined in Phase 2. Recorded here so the shape is reviewable now.

```
users              (id, clerk_user_id UNIQUE, email, name, role, created_at)
sla_policies       (id, name, priority, budget_minutes, schedule_mode, active, created_at)
tickets            (id, requester_id → users, assignee_id → users NULL,
                    title, description, category, priority, status,
                    sla_policy_id → sla_policies,
                    sla_consumed_micros, sla_clock_started_at,
                    sla_due_at, sla_breached_at,
                    created_at, updated_at)
ticket_status_history
                   (id, ticket_id → tickets, from_status NULL, to_status,
                    actor_id → users, actor_role, reason NULL, created_at)
comments           (id, ticket_id → tickets, author_id → users,
                    body, visibility, created_at, deleted_at NULL)
attachments        (id, ticket_id → tickets, uploader_id → users,
                    object_key, filename, content_type, size_bytes, created_at)
```

Indexes that must exist from day one:

- `tickets (sla_due_at) WHERE sla_breached_at IS NULL` — the worker's only predicate.
- `tickets (requester_id, created_at DESC)` — the customer portal list.
- `tickets (assignee_id, status)` — the agent dashboard.
- `ticket_status_history (ticket_id, created_at)` — clock reconstruction.

---

## 6. Commands

Driven from a root `Makefile`. Created in Slice 1 — these do not exist yet.

```bash
make up              # docker compose up -d  (postgres, redis)
make down            # docker compose down
make migrate-up      # goose up
make migrate-down    # goose down
make migrate-new name=add_tickets
make sqlc            # sqlc generate
make api             # go run ./cmd/api
make worker          # go run ./cmd/worker
make web             # cd web && pnpm dev

make test            # go test ./... && cd web && pnpm test
make test-go         # go test ./... -race -cover
make test-int        # go test ./... -race -tags=integration
make test-e2e        # cd web && pnpm playwright test
make lint            # golangci-lint run && cd web && pnpm lint
make check           # lint + test — must pass before every commit
```

---

## 7. Project Structure

Single Go module at the root (idiomatic Go), Next.js app in `web/`.

```
.
├── cmd/
│   ├── api/main.go            # HTTP API entrypoint
│   └── worker/main.go         # SLA breach worker entrypoint
├── internal/
│   ├── ticket/                # DOMAIN: state machine. No DB, no HTTP.
│   ├── sla/                   # DOMAIN: the only deadline arithmetic. No DB, no HTTP.
│   ├── httperr/               # what a failure looks like on the wire. Imports nothing here
│   ├── auth/                  # Clerk verification + RBAC middleware
│   ├── store/                 # sqlc-generated code + hand-written repos
│   ├── api/                   # chi handlers, DTOs, request validation
│   ├── realtime/              # WebSocket hub, Redis fan-out  (slice 6)
│   └── config/                # env loading, no globals
├── db/
│   ├── migrations/            # goose .sql files
│   └── queries/               # sqlc source .sql files
├── web/                       # Next.js App Router
│   ├── app/(customer)/        # customer portal layout
│   ├── app/(agent)/           # agent dashboard layout
│   ├── components/
│   ├── lib/
│   └── e2e/                   # Playwright
├── docs/
│   ├── spec.md                # this file
│   └── adr/                   # architecture decision records
├── tasks/                     # plan.md, todo.md
├── compose.yaml
├── Makefile
├── sqlc.yaml
└── go.mod
```

**The structural rule:** `internal/ticket` and `internal/sla` import nothing from
`store`, `api`, or `database/sql`. They are pure domain and unit-testable with no
Docker running. If a deadline calculation ever appears outside `internal/sla`, that is a
review blocker.

**The mirror rule, for `internal/httperr`:** it imports nothing from this module. It is not
a domain package — it is allowed `net/http`, which the domain is not — but it is a leaf, and
it has to stay one. Every layer above it reports failures through it, and a single import
would put it above whatever it imported, out of reach of the layer that needed it next. Both
rules are enforced by the import-graph walker in `internal/ticket/architecture_test.go`.
See [ADR 0004](adr/0004-one-way-to-report-an-http-failure.md).

**Packages are named for what they provide, never for the fact that several callers use
them.** A package named `shared`, `common` or `util` has an admission rule that can never
reject anything, so it only grows and its name says nothing. If an extraction is proposed as
"shared", the concern behind it has not been identified yet.

---

## 8. Code Style

### Go

```go
// internal/ticket/transition.go
package ticket

// Transition validates a status change. It is pure: no database, no clock,
// no context. Everything it needs is an argument.
func Transition(current, target Status, actor Role) (Status, error) {
	allowed, ok := transitions[current]
	if !ok {
		return "", fmt.Errorf("%w: unknown status %q", ErrInvalidTransition, current)
	}

	rule, ok := allowed[target]
	if !ok {
		return "", fmt.Errorf("%w: %s → %s", ErrInvalidTransition, current, target)
	}
	if !rule.permits(actor) {
		return "", fmt.Errorf("%w: %s may not move %s → %s", ErrForbidden, actor, current, target)
	}

	return target, nil
}
```

Conventions:

- Errors are values. Wrap with `%w`, define sentinels (`ErrInvalidTransition`), and let
  the HTTP layer — not the domain — decide the status code.
- `context.Context` is the first parameter on anything that does I/O. Never stored in a struct.
- No `interface{}` / `any` in domain code.
- Interfaces are declared by the **consumer**, kept small, and defined where they are used.
- No global state, no `init()` side effects. Dependencies are passed explicitly.
- Table-driven tests. Go test conventions follow `~/.claude/skills/go-testing/SKILL.md`.

### TypeScript

- `strict: true`. No `any`. Conventions follow the `typescript` skill.
- Server state belongs to TanStack Query. Client state belongs to Zustand. A value never
  lives in both.
- Filters live in URL query params, not component state — a filtered view must be a
  shareable link.
- API response types are generated or hand-mirrored from the Go DTOs in one place, never
  redeclared per component.

---

## 9. Testing Strategy

| Level | Tool | Scope | Requirement |
|---|---|---|---|
| Domain unit | `go test` | `internal/ticket`, `internal/sla` | **100% of transitions and clock branches.** No Docker needed |
| Integration | `go test -tags=integration` | Handlers + real Postgres | Every endpoint: happy path, authz denial, invalid transition |
| Consistency | `go test -tags=integration` | SLA reconstruction vs cache | See below — mandatory |
| Frontend unit | Vitest + Testing Library | Components, hooks | Optimistic update + rollback paths |
| E2E | Playwright | Critical flows | Customer creates ticket → agent resolves → customer sees it |

**Development is test-first** (`red → green → refactor`), per the `tdd` skill. The
project has strict TDD mode enabled.

**The mandatory consistency test:**

```
Given a ticket with an arbitrary sequence of status transitions in history,
When the SLA clock is reconstructed from ticket_status_history alone,
Then it equals tickets.sla_consumed_minutes / sla_due_at exactly.
```

Property-based over generated transition sequences, not a handful of examples. This test
is what earns the right to keep a derived cache.

**Coverage:** ≥90% on `internal/ticket` and `internal/sla`. No global target — a
coverage number over the whole repo measures nothing.

---

## 10. Boundaries

**Always:**

- Run `make check` before every commit.
- Write the failing test before the implementation.
- Write the `ticket_status_history` row in the same transaction as the ticket update.
- Scope customer queries by `requester_id` in SQL, not only in the handler.
- Conventional commits. No AI attribution, no `Co-Authored-By`.

**Ask first:**

- Any database schema change after Slice 1 ships.
- Adding a Go or npm dependency.
- Changing the deploy target or CI configuration.
- Any deadline arithmetic that would live outside `internal/sla`.
- Reintroducing tenancy, or any change to the state machine.

**Never:**

- Commit secrets. `.env` is gitignored; `.env.example` is committed.
- Trust a role, a `user_id`, or any permission claim coming from the client.
- Emit a WebSocket event before the database transaction commits.
- Hard-delete a comment, a ticket, or a history row.
- Hardcode an SLA budget anywhere outside `sla_policies`.
- Skip or delete a failing test to make the build green.

---

## 11. Success Criteria

Slice 1 is done when all of the following hold:

- [x] A new customer signs up via Clerk and is provisioned in `users` with role `customer`.
      Verified live against a real Clerk instance during T8 — `200 POST /api/webhooks/clerk`,
      and the row landed with `role=customer`.
- [x] `POST /api/tickets` creates a ticket with a resolved `sla_policy_id`, a running
      clock, and a non-NULL `sla_due_at`. Integration-tested, and exercised by hand through
      the form in T13.
- [x] `GET /api/tickets` returns only the caller's tickets. `TestGetAnswers404ForATicketThatIsNotYours`
      at the handler, `TestGetTicketForRequesterHidesAnotherCustomersTicket` and
      `TestHistoryForRequesterHidesAnotherCustomersTicket` against a real database. The
      predicate is in the SQL, so the handler cannot tell "absent" from "not yours" either.
- [x] A request with no token, an expired token, or a forged token receives `401`.
      `TestEveryAuthenticationFailureAnswers401` covers five cases: no header, not a JWT,
      three undecodable segments, expired, and signed by an untrusted key.
- [x] `internal/ticket` and `internal/sla` have zero imports from `store`, `api`, or
      `database/sql` — enforced by a test that walks the import graph.
      `internal/httperr` is held to the mirror rule and imports nothing from this module.
- [x] `make check` passes clean, and CI runs it on every push and pull request.
- [x] The application is deployed and reachable at a public URL: **https://sla-desk.josegd.me**.
      Verified by hand end to end — sign-up, ticket creation, the list, the detail timeline,
      and a filtered URL surviving a reload. The Clerk webhook delivers `200` straight to
      Cloud Run, with no relay in between.
- [x] `README.md` explains the SLA clock model and the fact-vs-cache decision.

---

## 12. Open Questions

Resolved before the slice that needs them, not now.

1. **Dual write on WebSocket emission (Slice 6).** Transaction commits, process dies
   before publishing, client never learns. Outbox table polled by the worker /
   Postgres `LISTEN/NOTIFY` / accept the loss and rely on refetch-on-reconnect? Leaning
   outbox — it is the honest answer and the one worth explaining in an interview.
2. **Clock behavior in `resolved` (Slice 5).** Currently the clock stops on `resolved`.
   If a resolution SLA is added later, `resolved → open` reopening must not restart a
   fresh budget. Decide when reopening is implemented.
3. ~~**User provisioning (Slice 1).**~~ **RESOLVED 2026-07-31:** webhook primary +
   idempotent lazy upsert fallback. See §4.5.
4. **Agent role assignment (Slice 2).** How does a user become an `agent`? Seeded in a
   migration, or an admin-only endpoint?
5. ~~**Deploy provider.**~~ **RESOLVED 2026-07-31:** Cloud Run + Vercel + Neon + Upstash,
   all permanent free tiers. See `tasks/plan.md` for the verified comparison against
   Heroku, Fly.io, Render, Koyeb, Railway and Oracle.
