# Todo: Slice 1 — Customer creates and tracks a ticket

Plan: [`tasks/plan.md`](./plan.md) · Spec: [`docs/spec.md`](../docs/spec.md)

Every task is test-first (spec §9, strict TDD). Write the failing test, watch it fail,
then make it pass.

---

## Phase 0: Rails and a proven deployment path

### T1: Repo, Docker Compose, Go module, health endpoint

**Description:** Initialise the repository and get a chi server answering locally with
Postgres and Redis running in containers. Nothing domain-specific.

**Acceptance criteria:**
- [x] `git init`, `.gitignore` (ignores `.env`, `node_modules`, `web/.next`, binaries, local AI tooling)
- [x] `.env.example` and `web/.env.local` created by hand — a local permission rule blocks the assistant from reading or writing `.env*`, which is why they are not machine-verified here
- [x] `compose.yaml` runs Postgres 16 and Redis 7 with named volumes and healthchecks
- [x] `cmd/api` serves `GET /health` → `200 {"status":"ok"}` (NOT `/healthz` — see ADR 0003) via chi, config from env, no globals
- [x] Graceful shutdown on SIGTERM, draining in-flight requests — Cloud Run sends SIGTERM before stopping a container
- [x] `ReadHeaderTimeout` set, so a slow client cannot hold a connection open indefinitely

**Verification:**
- [x] `make up` brings both containers to healthy; `/health` returns `200 {"status":"ok"}` against real Postgres
- [x] `POST /health` → `405`, `GET /nope` → `404`
- [x] SIGTERM logs `draining connections` then `stopped cleanly`
- [x] `go vet ./...` clean, `gofmt` clean, `internal/config` and `internal/api` tested
- [x] `git status` shows no `.env` and no `node_modules`

**Host ports changed to 5433 and 6380.** The machine already runs another project
(`habit-tracker`) on 5432, 6379 and 8080. Rather than stopping it, this project takes its
own ports — which is the right default anyway: anyone running more than one project
collides on the standard ones. Container-side ports are unchanged.

**`.env*` files are outside the assistant's reach** by a local permission rule. The rule is
correct and was not worked around, so those files are written and reviewed by hand.

Note that `.gitignore` ignores `.env` and `.env.*` but re-includes `.env.example` — that
file **is** committed, so it must contain placeholder values only.

`PORT=8081` locally, because another project on this machine holds 8080.

**Dependencies:** None
**Files:** `.gitignore`, `compose.yaml`, `Makefile`, `go.mod`, `cmd/api/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/api/router.go`, `internal/api/router_test.go`
**Scope:** M

---

### T2: Next.js App Router scaffold

**Description:** Scaffold the web app with TypeScript strict, Tailwind, and shadcn/ui.
One page, no auth yet.

**Acceptance criteria:**
- [x] `web/` runs **Next.js 16.2.12** App Router with `strict: true`, React 19.2, Tailwind 4.3
- [x] shadcn/ui installed; a `Button` renders on the landing page, proving the pipeline
- [x] `web/lib/api.ts` reads the API origin from `NEXT_PUBLIC_API_URL` — no hardcoded host, and it throws a named error rather than silently falling back to localhost

**Verification:**
- [x] `pnpm build` succeeds — 4 static routes generated
- [x] `pnpm lint` clean

**Two version findings that would have caused silent bugs:**

1. **`middleware.ts` is renamed to `proxy.ts` in Next 16**, and the exported function
   `middleware` becomes `proxy`. The edge runtime is **not supported** in `proxy` — it
   runs on Node.js and that is not configurable. This directly invalidates T12 as
   originally written; see the updated task.
2. **shadcn's Button now wraps Base UI, not Radix.** There is no `asChild` prop.
   Composition uses `render={<Element />}`, plus `nativeButton={false}` when the rendered
   element is not a native button, which changes keyboard and accessibility handling.

Both were found by reading `web/node_modules/next/dist/docs/` and the Base UI type
definitions rather than writing from memory. The generated `web/AGENTS.md` says outright:
*"This is NOT the Next.js you know... Read the relevant guide before writing any code."*

**Dependencies:** None
**Files:** `web/app/layout.tsx`, `web/app/page.tsx`, `web/lib/api.ts`, `web/lib/utils.ts`, `web/components/ui/button.tsx`, `web/components.json`, `web/package.json`, `web/tsconfig.json`
**Scope:** M

---

### T3: Deploy the walking skeleton ⚠️ HIGH RISK — DO NOT DEFER

**Description:** Put the do-nothing application into production. The Go API on Google
Cloud Run, the Next.js app on Vercel, Neon for Postgres. The browser on the Vercel domain
must successfully call the Cloud Run domain.

This ships nothing a user can see. It proves the pipeline while there is nothing complex
to blame.

**Acceptance criteria — cost guardrails first.** A card must be on file for Cloud Run's
free tier, so a misconfiguration bills real money. Set these *before* the first deploy:

- [x] `--min-instances=0` — verified: the `minScale` annotation is absent, which means 0
- [x] `--max-instances=3` — verified: `autoscaling.knative.dev/maxScale: '3'`
- [x] CPU allocated **during requests only** — verified: `cpu-throttling` is absent, so throttling is on. The annotation only appears when someone passes `--no-cpu-throttling` to disable it
- [x] Budget alert active at **$2**, thresholds at 50/90/100/150%, `INCLUDE_ALL_CREDITS`

`run.googleapis.com/startup-cpu-boost: 'true'` is set by gcloud without being asked, and it
**is billed** — *"You are charged for the allocated boosted CPU for the duration of the
container startup time."* It stays: at a 1.84 s cold start it is earning its cost, and
cold start is the risk this project actually cares about. Disable with `--no-cpu-boost` if
that ever changes.

**A budget does not cap spending.** Cloud Billing documents that an alerts-only budget
*"doesn't automatically cap Google Cloud usage or spending"*, and that the first email may
take *"several hours"*. `maxScale` is the real protection; the budget is a smoke detector.

**Split.** The application half is built and verified locally. The cloud half needs
interactive logins and accounts and was run by hand; those steps live in local operational
notes that are deliberately not committed.

**Done — application, verified locally:**
- [x] Multi-stage `Dockerfile`: static `CGO_ENABLED=0` binary on `distroless/static`, **18 MB**, running as `nonroot`. `docker exec … id` fails because the image contains no shell and no coreutils — that is the point
- [x] The API binds the injected `PORT` — verified by running the container with `PORT=9999`
- [x] `/health` runs injected `Probe`s and returns **503 `degraded`** when any fails, verified by stopping Postgres and watching it recover
- [x] Probe failures never leak detail to the client: driver errors carry hosts and credentials, so they go to the logs and the response says only `failed`
- [x] Each probe is bounded by a 2s timeout, so an unreachable dependency reads as a failure rather than a hang
- [x] Redis is deliberately not probed — nothing uses it until slice 5, and failing health on an unused dependency would take the service down for nothing
- [x] CORS: single configured origin, `Vary: Origin`, preflight `204`, no wildcard ever, and disabled entirely when unconfigured
- [x] `Access-Control-Allow-Credentials` is not set — auth travels in the `Authorization` header, not cookies
- [x] `.dockerignore` keeps `.env*`, `.git`, `web/`, docs and local tooling out of the build context; secrets survive in layers even when a later stage deletes them
- [x] `internal/api` at 97.7% coverage; `make check` green

**Done — cloud:**
- [x] `gcloud` installed and authenticated
- [x] GCP project `sla-desk-josgd` created, billing linked, Run and Artifact Registry enabled
- [x] All cost guardrails set and verified
- [x] Neon project created; `DATABASE_URL` in Secret Manager, read by a **dedicated** runtime service account (`sla-desk-runtime@`) rather than the over-privileged default Compute account
- [x] `curl "$API_URL/health"` returns `database: ok` from the public URL
- [x] Vercel project linked, `NEXT_PUBLIC_API_URL` set **before** building
- [x] `CORS_ALLOWED_ORIGIN` set to the stable production domain, verified in the browser

**Live URLs**

| | |
|---|---|
| Web | <https://sla-desk-phi.vercel.app> |
| API | `https://sla-desk-api-278131323722.us-central1.run.app` |

The Vercel origin must be the **stable production domain**, never a per-deployment URL —
those carry a hash that changes on every deploy, so CORS would break on the next one with
no backend change. And it carries **no trailing slash**: browsers send `Origin` without
one, and the middleware compares exact strings.

The landing page renders `<ApiStatus />`, a **client** component. That is the point: a
server component would fetch server-to-server and never exercise CORS, passing green while
the browser path was broken.
- [x] **Cold start measured**: 1.84 s cold, 0.44 s warm — recorded in `tasks/plan.md`. `startup-cpu-boost` is on (gcloud enables it by default and it is billed per startup); at 1.84 s it is earning its keep, so it stays
- [ ] GCP billing page shows **$0.00**

**Dependencies:** T1, T2
**Files:** `Dockerfile`, `.dockerignore`, `Makefile`, `cmd/api/main.go`, `internal/api/health.go`, `internal/api/health_test.go`, `internal/api/cors.go`, `internal/api/cors_test.go`, `internal/api/router.go`, `web/components/api-status.tsx`, `web/app/page.tsx`
**Scope:** M

> **Checkpoint A — review with human before Phase 1.**

---

## Phase 1: Domain and data

### T4: Migration 001 + sqlc setup + seed SLA policies

**Description:** First migration and the sqlc pipeline. `users` and `sla_policies` only.

**Acceptance criteria:**
- [x] goose migration creates `users` (`id`, `clerk_user_id` UNIQUE NOT NULL, `email`, `name`, `role`, timestamps) and `sla_policies` (`id`, `name`, `priority`, `budget_minutes`, `schedule_mode`, `active`, `created_at`)
- [x] `role` and `priority` are constrained by the database (enum or CHECK) — not only by Go — **CHECK**, see below
- [x] Seed migration inserts the four default policies from spec §4.2 (urgent 60, high 240, normal 1440, low 4320)
- [x] `sqlc.yaml` configured; `make sqlc` generates compiling Go
- [x] `down` migration reverses cleanly

**Verification:**
- [x] `make migrate-up && make migrate-down && make migrate-up` runs clean — after a full down only `goose_db_version` remains
- [x] `make sqlc && go build ./...` succeeds
- [x] `SELECT * FROM sla_policies` returns exactly 4 active rows
- [x] 14 mutations of the schema and the queries each turn the matching test red

**Dependencies:** T1
**Files:** `db/migrations/001_users_and_sla_policies.sql`, `db/migrations/002_seed_sla_policies.sql`,
`db/queries/users.sql`, `db/queries/sla_policies.sql`, `sqlc.yaml`, `internal/ticket/role.go`,
`internal/store/` (generated), `internal/store/store_integration_test.go`
**Scope:** M

**Decisions taken during T4:**

- **CHECK constraints on TEXT, not native enums.** Measured against Postgres 16: a new enum
  value cannot be used in the transaction that adds it, and goose runs every migration in a
  transaction — so any migration that adds a value and backfills with it has to be split or
  lose atomicity. `ALTER TYPE ... DROP VALUE` does not exist at all. A CHECK is a table
  constraint, swapped with `DROP`/`ADD` in one transaction. The enum's one real advantage,
  ordering by declaration, does not apply here: the agent dashboard sorts by `sla_due_at`.
- **sqlc overrides map the columns straight onto `ticket.Priority` and `ticket.Role`**, so the
  vocabulary exists once instead of once in the domain and once in generated code.
  `timestamptz` is spelled bare in the override — sqlc's own docs write `pg_catalog.timestamptz`,
  which silently matches nothing.
- **UUID for `users.id`, identity bigint for `sla_policies.id`.** UUID for anything that reaches
  a URL or an API response; bigint for internal reference data. `internal/sla.Policy.ID` was
  already `int64`.
- **Partial unique index `(priority) WHERE active`**, beyond the stated criteria. Resolving a
  policy from a priority has to return exactly one row, and without it a second active policy
  would make ticket creation pick one by row order.
- **`schedule_mode` accepts only `'24x7'`.** `internal/sla` implements no other schedule, so a
  row the code cannot interpret must not be creatable.
- **`email` is deliberately not unique** — two Clerk identities can carry the same address.
- **goose and sqlc are pinned as `tool` directives in `go.mod`**, run via `go tool`. Cost:
  go.sum went from 28 to 411 lines and the module graph to 240 modules, including ten database
  drivers we do not use. None of it reaches the API binary, which still depends only on chi
  and pgx.

---

### T5: `internal/sla` — the deadline arithmetic ⚠️ HIGHEST-VALUE TASK

**Description:** The entire SLA clock, as pure functions. No database, no `context.Context`,
no HTTP, no `time.Now()` called internally — `now` is always a parameter.

This is the task the whole project rests on. Take the time.

**Acceptance criteria:**
- [x] `Schedule` interface with `Elapsed(from, to)` and `DueAt(from, remaining)`
- [x] `Always24x7` implements it. `BusinessHours` is **not** implemented — only the seam exists
- [x] `Policy` carries `Budget` and `Schedule`; no budget constant appears anywhere in the code
- [x] `Reconstruct(policy, history []StatusChange) (ClockState, error)` derives everything from history alone
- [x] Clock runs only in `open`; paused in `pending`, `resolved`, `closed`
- [x] A paused ticket yields `due_at = nil` — expressed by the model, not by a special case
- [x] Rejects malformed history with `ErrEmptyHistory`, `ErrUnorderedHistory`, `ErrHistoryMustStartOpen`, returning the zero `ClockState` alongside the error ([ADR 0002](../docs/adr/0002-validation-ownership-between-sla-and-ticket.md))

**Signature change from the original card:** the `now` parameter was dropped. Every field of
`ClockState` derives from the policy and the history; `now` only matters when deciding
whether a ticket has *already* breached, which is the breach checker's job comparing
`due_at` against the clock. An unused time parameter is a dependency that makes a pure
function harder to test for no benefit.

**Field names** were changed on review: `Consumed` → `BudgetUsed` (it did not say *of what*)
and `StartedAt` → `RunningSince` (it collided with the ticket's `created_at`).

**Verification:**
- [x] `go test ./internal/sla/... -race -cover` — **100% coverage**, 34 assertions
- [x] Tests run with **no Docker container up** — proves the package is pure
- [x] Table-driven tests cover: never paused; paused once; paused repeatedly; paused at creation; budget exactly exhausted; zero-length pause; reopened from resolved; overspent budget producing a past deadline
- [x] Property tests over 2000 generated histories each: pausing never moves a deadline earlier; running/paused state is internally consistent; `BudgetUsed` stays within the history span; `Reconstruct` is deterministic
- [x] `gofmt` and `go vet` clean
- [x] **Mutation-checked.** Four deliberate defects were introduced and each was caught: removing the negative clamp in `Elapsed`; inverting `DueAt`; letting the clock run in `resolved`; double-counting elapsed time. A test that cannot fail proves nothing

**Dependencies:** None — start any time
**Files:** `internal/sla/schedule.go`, `internal/sla/clock.go`, `internal/sla/schedule_test.go`, `internal/sla/clock_test.go`, `internal/sla/clock_property_test.go`, `internal/ticket/status.go`
**Scope:** M

---

### T6: Migration 002 (`tickets`, `ticket_status_history`) + queries

**Description:** The ticket tables, the SLA cache columns, and the indexes from spec §5.

**Acceptance criteria:**
- [x] `tickets` includes all `sla_*` columns from spec §4.2
- [x] `ticket_status_history` created with `from_status` nullable (creation is `NULL → open`)
- [x] All four indexes from spec §5 exist, including the partial index `tickets (sla_due_at) WHERE sla_breached_at IS NULL`
- [x] `status` and `priority` constrained by the database — plus `category` and `actor_role`
- [x] sqlc queries: create ticket, insert history row, list by requester, get by id **scoped by requester**, read history by ticket

**Verification:**
- [x] `make migrate-up && make migrate-down && make migrate-up` clean — after a full down only `goose_db_version` remains
- [x] `make sqlc && go build ./...` succeeds
- [x] `EXPLAIN` on the breach worker query from spec §4.2 shows an index scan, not a sequential scan
- [x] 15 mutations of the schema and the queries each turn the matching test red
- [x] 34 integration tests, none skipped

**Dependencies:** T4
**Files:** `db/migrations/003_tickets_and_status_history.sql`, `db/queries/tickets.sql`,
`db/queries/ticket_status_history.sql`, `internal/ticket/category.go`, `sqlc.yaml`,
`internal/store/` (generated), `internal/store/tickets_integration_test.go`, `docs/spec.md`
**Scope:** M

**Decisions taken during T6:**

- **`sla_consumed_micros`, not `sla_consumed_minutes`.** The spec specified both minutes and a
  consistency test that compares the reconstruction to the cache *exactly*, and those cannot
  both hold: `sla.Reconstruct` returns a `time.Duration`. A ticket bouncing eight times at
  3m40s has really consumed 29m20s; in minutes the cache records 24m, putting the deadline
  five minutes late on a 60-minute budget. `TIMESTAMPTZ` resolves to one microsecond, so
  microseconds round-trip exactly and the drift is zero. Spec §4.2 and §5 updated.
- **Two clock invariants are CHECK constraints**, not handler logic:
  `(status = 'open') = (sla_clock_started_at IS NOT NULL)` and
  `(sla_clock_started_at IS NULL) = (sla_due_at IS NULL)`. "A paused ticket cannot breach"
  is therefore a property of the schema and holds against direct SQL.
- **`category` values defined for the first time** — `billing`, `technical`, `account`, `other`,
  recorded as spec §4.7. Constrained by the database, mapped to `ticket.Category`.
- **No `ON DELETE CASCADE` anywhere.** Spec §10 forbids hard-deleting a ticket or a history
  row, and a cascade does exactly that from a distance. Deleting a user or a policy that is
  still referenced fails loudly.
- **`from_status IS DISTINCT FROM to_status`.** A move to the status a ticket is already in is
  a no-op, and recording it would pad the history the clock is rebuilt from.
- **`actor_role` is denormalised on purpose** — the role held at the time, so promoting someone
  does not rewrite what the audit trail says they were.
- **Ticket ids are UUID, history ids are identity bigint**, following the rule set in T4.

**Known gap, deliberate:** `ListTicketStatusHistory` orders by `created_at, id`, and no test
covers the `id` tiebreaker. Rows written in one transaction share `created_at`, and Postgres
returns those ties in insertion order through every plan a test can provoke — including after
an UPDATE that moves the tuple, because a HOT update leaves the index entry pointing at the
original item. The tiebreaker stays because Postgres guarantees no order for equal sort keys.
What is covered is that `created_at` is the primary sort key, which is falsifiable and tested.

> **Checkpoint B — migrations reversible, sqlc compiles, `internal/sla` green.**

---

## Phase 2: Auth and the tickets API

### T7: Clerk JWT verification, `RequireAuth`, lazy upsert ⚠️ HIGH RISK

**Description:** Verify the Clerk session JWT against the JWKS, resolve it to a local
user, and reject everything else.

**Re-read the `clerk-sdk-go/v2` docs before writing code. Do not code from memory.**

**Acceptance criteria:**
- [x] JWT verified against Clerk's JWKS; the JWK is cached, not fetched per request
- [x] `RequireAuth` **rejects** unauthenticated requests with `401` — `WithHeaderAuthorization` alone does not do this (spec §4.3)
- [x] The authenticated user, including the role **read from our database**, is placed in the request context
- [x] A verified token with no matching `users` row triggers the idempotent upsert (spec §4.5) and the request succeeds
- [x] New users are always seeded `role = 'customer'`; the role is never read from the token or any client input

**Verification:**
- [x] Tests return `401` for: no header, malformed header, expired token, token signed by the wrong key — plus four more (tampered payload, unknown key id, wrong authorized party, non-Clerk issuer)
- [x] A test with a valid token for an unknown subject returns `200` and provisions exactly one user
- [x] Calling the upsert 16 times concurrently creates exactly one row, and every caller receives the same id
- [x] `go test ./internal/auth/... -race` passes — 20 tests, `internal/auth` at 90.7%

**Dependencies:** T4
**Files:** `internal/auth/clerk.go`, `internal/auth/middleware.go`, `internal/auth/user.go`,
`internal/auth/middleware_test.go`, `internal/auth/clerk_test.go`,
`internal/store/store_integration_test.go` (concurrency)
**Scope:** M

**What the SDK actually does, measured against v2.7.0:**

- `WithHeaderAuthorization` does not reject, as the spec said — but the detail matters. A
  request with **no token and one with a token it cannot decode both pass straight through**.
  Only a token that decodes and then fails verification reaches the failure handler.
- `RequireHeaderAuthorization` **does** reject, which the spec did not mention, but with
  **403**. Combined with the failure handler's 401 that gives a mixed status surface, so it is
  not used. `Middleware` + our `RequireAuth` answers 401 to all nine failure modes.
- `jwt.Verify` does **not** cache the JWK; caching moved to the caller in v2. The HTTP
  middleware does cache, by key id, for an hour — so mounting `WithHeaderAuthorization`
  satisfies the caching requirement and no cache of ours is needed.
- **The cache is process-global and keyed by key id alone**, with no scoping by instance or
  issuer. Harmless with one Clerk instance; it made tests leak keys into each other until each
  got its own key id.
- `jwt.Verify` validates the issuer's **shape**, not its identity:
  `HasPrefix(iss, "https://clerk.")` or a `.clerk.accounts` substring. Any Clerk instance's
  issuer passes. What binds a token to us is the JWKS — and the authorized party, which is why
  `AuthorizedPartyMatches` is enabled.
- The session token carries the subject and **not** the email or name. Provisioning fetches
  them from the Backend API, once per user, and picks the address Clerk marks primary rather
  than the first in the list.

**Deferred to T10:** `Config` is filled by the caller; `internal/config` does not read
`CLERK_SECRET_KEY` yet, so the API still boots without it. Wiring the middleware into the
router makes it required.

---

### T8: Clerk webhook with Svix verification

**Description:** `POST /api/webhooks/clerk`, handling `user.created` and `user.updated`.

**Read the `svix-webhooks/go` docs first** — only the library name and the header names are
confirmed so far.

**Acceptance criteria:**
- [x] Signature verified with `github.com/svix/svix-webhooks/go` using `svix-id`, `svix-timestamp`, `svix-signature`
- [x] An invalid or missing signature returns `400` and writes nothing
- [x] The route is exempt from `RequireAuth` — Clerk does not send a session JWT — **handler built; mounting is T10**
- [x] The handler calls the **same** upsert function as T7 — both now go through `auth.Provision`
- [x] Replaying an identical event produces no duplicate row and still returns `2xx` (Svix retries on non-2xx)
- [x] Unknown event types return `200` and are ignored, not treated as errors

**Verification:**
- [x] A correctly signed fixture payload provisions the user, with the address Clerk marks primary
- [x] A tampered body returns `400` and writes nothing
- [x] Delivering the same event twice returns `200` both times and reaches the idempotent write
- [x] A delivery stamped ten minutes ago is refused — Svix enforces a five minute tolerance
- [x] A write failure answers `500` **on purpose**, so Svix retries rather than dropping the event
- [x] 7 mutations of the handler and the mapping each turn the matching test red
- [x] Manual: a real Clerk signup reaches localhost and the row appears — **verified 2026-08-03**.
  `npx clerk@latest webhooks listen` relayed a `user.created` from a user created in the
  dashboard; the handler answered `200` and the row landed with `role = customer`. This is the
  one thing the tests could not cover: every other webhook test signs its fixture with the same
  library that verifies it, so a signature Clerk actually produced had never been through this
  code. Note the CLI supersedes `svix listen` — see [clerk-integration.md](../docs/clerk-integration.md)

**Dependencies:** T7
**Files:** `internal/api/webhooks.go`, `internal/api/webhooks_test.go`,
`internal/api/testdata/user_created.json`, `internal/auth/provision.go`
**Scope:** M

**Notes:**

- `svix.NewWebhook` / `Verify` / `Sign` confirmed against v1.99.1. `Sign` being exported is what
  lets the tests sign their fixtures with the same library that verifies them, instead of a
  reimplementation of the scheme that would agree with itself whatever it did.
- The module is `github.com/svix/svix-webhooks` and the verifier shares a package with the whole
  Svix API client. Measured rather than feared: linking it adds **~100 KB** to a binary, because
  the linker drops what is unused.
- `auth.Provision` and `auth.IdentityFromClerkUser` were extracted so the webhook and the lazy
  fallback cannot disagree about which address is a user's. The role is not a parameter of
  either — the query writes it as a literal and omits it from the conflict clause, so no webhook
  payload can create or demote an agent.
- The body is read whole and verified **before** anything is decoded from it: the signature
  covers the exact bytes Clerk sent. Capped at 1 MiB.
- `ClerkWebhookHandler` returns an error rather than a handler that fails at request time, so an
  unusable signing secret stops the deploy instead of becoming 500s nobody is watching.

**Deferred to T10:** mounting the route outside `RequireAuth`, and reading
`CLERK_WEBHOOK_SECRET` in `internal/config`.

---

### T9: `POST /api/tickets`

**Description:** Create a ticket: resolve the SLA policy from priority, start the clock,
and write the initial history row — all in one transaction.

**Acceptance criteria:**
- [x] Validates `title`, `description`, `category`, `priority`; invalid input returns `400` with per-field errors
- [x] `requester_id` comes from the auth context, **never** from the request body
- [x] The policy is looked up from `sla_policies` by priority and snapshotted into `sla_policy_id`
- [x] `sla_clock_started_at` and `sla_due_at` computed by `internal/sla` — no arithmetic in the handler
- [x] The ticket row and the `NULL → open` history row are written in **one transaction**; a failure writes neither

**Verification:**
- [x] Valid request returns `201` with the created ticket, a `Location` header and a non-NULL `sla_due_at`
- [x] `sla_due_at` equals `sla_clock_started_at + budget` **exactly** — not within a tolerance, because both instants are the same transaction timestamp
- [x] Each priority resolves to its seeded budget
- [x] An invalid actor role fails the history insert and leaves **zero** rows in both tables
- [x] A body containing `requester_id` is ignored; the requester is the authenticated caller
- [x] **The clock rebuilt from history equals the cached columns exactly** — the §9 property, at creation
- [x] 10 of 11 mutations turn the matching test red; the eleventh is recorded below

**Dependencies:** T5, T6, T7
**Files:** `internal/api/tickets.go`, `internal/api/dto.go`, `internal/api/problem.go`,
`internal/store/ticket_repo.go`, `db/queries/clock.sql`, plus tests
**Scope:** M

**Decisions taken during T9:**

- **Time comes from Postgres, read once per transaction** (`TransactionTime`), and the same
  instant is written to `sla_clock_started_at`, used to compute `sla_due_at`, and passed as the
  history row's explicit `created_at`. `InsertTicketStatusHistory` gained a `created_at`
  parameter for this. Left to the column default, the history row would carry the instant
  Postgres stamped it and the cache the instant the deadline was computed from, and §9's
  consistency test could never hold.
- **Errors are RFC 9457 problem documents** (`application/problem+json`). Validation reports
  every rejected field at once. A 500 carries no detail at all — Go error text accumulates
  driver messages, table names and connection strings, and an error response is the cheapest
  place to read them.
- **`CreateTicketRequest` has no `requester_id`, `status` or `sla_*` field.** A value with
  nowhere to land is dropped when the body is decoded, before any code can read it.
- **A missing policy for a supported priority is a 500, not a 400.** Validation has already
  established the priority is one of the four; no policy means our seed is wrong.
- **The repo owns the transaction, the handler owns HTTP.** `TicketRepo.Create` is the atomic
  unit: read the instant, resolve the policy, run it through `sla.Reconstruct`, write both rows.
  Even the trivial "budget from zero" case goes through `Reconstruct`, so §4.2's promise that
  deadline arithmetic exists in one place has no exception.

**Known gap, deliberate:** no test covers the *choice* of clock. Swapping `TransactionTime` for
`time.Now()` leaves the whole suite green, consistency test included, because the cache and the
history would move together. What the app clock breaks is invisible here: the breach worker
evaluates `sla_due_at < now()` on the database's clock, so a deadline from an API instance's
clock is shifted by that instance's drift — measured at 928µs against a database on the same
machine, unbounded across Cloud Run instances and a managed Postgres. The reasoning is recorded
at the call site.

---

### T10: `GET /api/tickets` and `GET /api/tickets/{id}`

**Description:** Read endpoints, scoped to the caller in SQL.

**Acceptance criteria:**
- [x] `GET /api/tickets` returns only the caller's tickets, newest first, paginated
- [x] `GET /api/tickets/{id}` returns `404` when the ticket belongs to someone else — **not `403`**, which would confirm the ticket exists
- [x] Scoping is in the SQL (`WHERE requester_id = $1`), not a post-query filter in Go
- [x] Responses expose `sla_due_at` and enough state for the UI to render a timer

**Verification:**
- [x] Customer B asking for A's ticket by id receives `404`
- [x] Customer B's list does not contain A's ticket
- [x] There is no handler ownership check to remove — the SQL is the only thing enforcing it, and removing the predicate turns the store tests red
- [x] Pagination is stable: a ticket inserted between two page reads neither repeats nor skips a row
- [x] 8 mutations of the handlers and the wiring each turn the matching test red

**Dependencies:** T9
**Files:** `internal/api/tickets.go`, `internal/api/router.go`, `db/queries/tickets.sql`,
`internal/config/config.go`, `cmd/api/main.go`, plus tests
**Scope:** M — larger than planned, see below

**Scope note — the plan had no task for wiring the router.** T8 and T9 built handlers that
nothing mounted; without doing it here we would have reached Checkpoint C with an API that still
served only `/health` in production. Done as part of this task: `NewRouter` takes a `Deps`
struct and returns an error, the Clerk webhook is mounted **outside** `RequireAuth`, the ticket
routes inside it, and `cmd/api/main.go` builds the store and the repo.

**Decisions taken during T10:**

- **Keyset pagination, not `OFFSET`.** The criterion asks for stable results across pages, and
  `OFFSET` cannot give them: a ticket created while someone is paging shifts every later row
  down and they see one twice. The cursor carries `(created_at, id)` — `id` because two tickets
  created in the same transaction share a timestamp, and a cursor on a non-unique key either
  skips rows or repeats them. The cursor is opaque so the sort order is not part of the API.
- **The page size is clamped, not rejected.** A caller asking for 1000 means "as many as I can
  have", and an error there is friction with no security value.
- **`CLERK_SECRET_KEY` and `CLERK_WEBHOOK_SECRET` are now required**; the API will not boot
  without them. `CLERK_AUTHORIZED_PARTY` falls back to `CORS_ALLOWED_ORIGIN`, because they are
  the same origin in practice and an operator who set one and forgot the other would silently
  lose the check that ties a token to our frontend. `CLERK_API_URL` is optional.
- **An empty page encodes as `[]`, never `null`**, so no client has to write a nil check.

**What mutation testing caught this time:** `TestTheTicketRoutesRequireASession` stayed green
with `RequireAuth` deleted from the router, because each handler also refuses a request with no
caller in its context. The system fails closed, which is the right direction — but the wiring
would have been broken with nothing to say so. The gap is now covered by
`TestASignedRequestReachesTheHandlerThroughTheRouter`, which signs a real token against a real
JWKS and requires the request to *succeed*, so the whole chain has to be present and in order.

---

### T11: SLA consistency test + architecture boundary test

**Description:** The two tests that protect the architecture. Neither adds a feature; both
prevent a class of bug.

**Reframed per [ADR 0001](../docs/adr/0001-single-calculation-path-for-the-sla-clock.md).**
This test does **not** assert the arithmetic is correct — the unit tests at the
`internal/sla` seams cover that. Asserting `Reconstruct(history) == cache` when the write
path *produces* the cache by calling `Reconstruct` would be tautological. It targets the
**write path** instead.

**Acceptance criteria:**
- [x] Consistency test, against a real database: the values stored in `tickets.sla_*` equal `Reconstruct` over the rows stored in `ticket_status_history` for that ticket
- [x] It is property-based over generated transition sequences, not a handful of examples — 15 sequences, up to 6 transitions each, checked after **every** step
- [x] It detects each of these write-path defects, verified by deliberately introducing them: cache update skipped; fact and cache written in separate transactions; history read *before* the new row was inserted (cache one event behind); partial write committed — **all four red**
- [x] Architecture test: `internal/sla` and `internal/ticket` import nothing from `internal/store`, `internal/api`, or `database/sql`
- [x] Architecture test: **`internal/ticket` never imports `internal/sla`** — the dependency runs one way only ([ADR 0002](../docs/adr/0002-validation-ownership-between-sla-and-ticket.md))
- [x] All wired into `make check` (architecture) and `make test-int` (consistency, needs a database)

**Verification:**
- [x] `make test-int` passes
- [x] Each of the four write-path defects makes the consistency test fail; so does deleting the call to the state machine, and so does making `closed` non-terminal
- [x] Adding an import of `database/sql` to `internal/sla` makes the architecture test fail — verified during T10 with `net/http` and `pgx`, and the failure propagates to `internal/sla` through `internal/ticket`

**Dependencies:** T9, T10
**Files:** `internal/ticket/transition.go`, `internal/ticket/transition_test.go`,
`internal/store/ticket_repo.go`, `internal/store/consistency_integration_test.go`,
`internal/ticket/architecture_test.go` (written earlier, before T7)
**Scope:** L — the state machine had to be built first, see below

**Scope note — T11 could not be met as written.** It asks for a property test over *generated
transition sequences*, and slice 1 had no transitions: `Create` was the only write path, so
every ticket's history was one row long and a "sequence" was a single element. Simulating
transitions inside the test would have produced exactly the tautology ADR 0001 warns about.

The state machine and its write path were therefore pulled forward from slice 3, with the
decision recorded in `docs/spec.md` §2. Two things fell out of that which slice 1 did not have
before: the SLA clock can now **pause and resume**, which is the headline behaviour of the whole
domain, and `TestPausingStopsTheClockAndResumingKeepsWhatWasSpent` proves that time spent
waiting on a customer does not consume budget.

**Also corrected: the §4.1 diagram contradicted its own rules.** It labelled an arrow "reopen
(customer or agent)" and drew it touching `closed`, while the prose states twice that `closed`
is terminal. The prose wins — the edge is `resolved → open`. The section now carries an
exhaustive edge table so the next reader does not have to choose.

**What the write path looks like**, and why it is a repo method rather than four handler calls
— the order is the whole point (ADR 0001):

```
1. insert the ticket_status_history row
2. read the ticket's full history      ← after the insert, never before
3. rebuild the clock from it
4. write the tickets.sla_* cache
```

`GetTicketForUpdate` takes `FOR UPDATE`: two transitions arriving at once would otherwise both
read the same current status and the second commit would overwrite the first with a cache that
never accounted for it.

> **Checkpoint C — review with human before Phase 3.**

---

## Phase 3: Frontend

### T12: Clerk wiring, customer layout, protected routes

**Description:** Clerk on the Next.js side, the `(customer)` route group, and the token
attached to API calls.

⚠️ **Revised after T2.** Next.js 16 renamed `middleware.ts` to **`proxy.ts`** and the
exported `middleware` function to **`proxy`**. The **edge runtime is not supported** in
`proxy`; it runs on Node.js and that is not configurable. Writing `middleware.ts` here
would produce a file Next 16 silently ignores — leaving every "protected" route open.

**Verified 2026-08-03, before writing any code. The risk is closed.** `@clerk/nextjs@7.6.4`
declares `next: ^16.1.0-0`, which `16.2.12` satisfies, and Clerk's documentation now shows
`clerkMiddleware` in `proxy.ts` outright: *"If you're using Next.js ≤15, name your file
`middleware.ts` instead of `proxy.ts`. The code itself remains the same."* No fallback needed.

**Two findings that changed the design, though.**

`clerkMiddleware()` on its own protects nothing — it attaches auth state and returns. Closing
a route needs `createRouteMatcher` plus `await auth.protect()` inside the callback. This is
the same shape as the `WithHeaderAuthorization` trap in spec §4.3: a helper that looks like
protection and is not.

And the pattern this task originally specified is the one both projects are moving away from.
Next's own documentation for 16.2.12 says Proxy *"should not be used as a full session
management or authorization solution"*, and Clerk publishes a guide titled
`migrate-from-create-route-matcher` whose stated goal is to *"move this protection to
individual resources"*.

**Decision: the redirect lives in the `(customer)` layout, via `auth.protect()`.** `proxy.ts`
still exists — Clerk's SDK needs it to populate auth state — but decides nothing. A layout
covers every route in the group including ones added later; a matcher is a list someone has
to remember to update, and a forgotten pattern fails silently.

This costs nothing in security, because the frontend holds no data of its own: every ticket
read goes through the Go API, which enforces `RequireAuth` and scopes by requester in SQL. The
frontend redirect is UX — it stops a signed-out visitor seeing an empty shell. The boundary is
on the other side of the network.

**Acceptance criteria:**
- [x] `<ClerkProvider>` mounted; sign-in and sign-up pages render (both `200`)
- [x] `web/proxy.ts` (**not** `middleware.ts`) mounts `clerkMiddleware`; `auth.protect()` in the `(customer)` layout redirects signed-out visitors
- [x] A test or manual check proves an unauthenticated request to a protected route is actually redirected — measured, see below
- [x] The API client attaches the Clerk session token to every request from one place — `lib/use-api.ts` is the only file in `web/` that mentions `Authorization` or calls `getToken`
- [x] TanStack Query provider configured with sane defaults
- [x] A `401` from the API is handled globally, not per call site — `QueryCache.onError`

**Verification:**
- [x] `pnpm build`, `pnpm lint` and `tsc --noEmit` clean
- [x] Signed out, the route surface is: `/` `200`, `/sign-in` `200`, `/sign-up` `200`, `/tickets` **`307` → `/sign-in?redirect_url=…`**
- [x] Manual: signed in, a request to the API carries the `Authorization` header — verified 2026-08-03.
  `/tickets` renders the success branch, which only runs when the query resolves; without a token
  `RequireAuth` would have answered `401` and the error branch would show instead. The decoded
  token carried `"azp": "http://localhost:3000"`, the exact claim `CLERK_AUTHORIZED_PARTY` matches.
- [ ] **Deferred:** a brand-new signup reaching the app *before the webhook lands* is still
  unproven in the wild. The signup on 2026-08-03 was provisioned by the **webhook**, not the
  fallback: `created_at` equals `updated_at` on that row, so exactly one path wrote it, and the
  relay was running. The lazy upsert is covered by unit tests but the race in spec §4.5 has never
  actually happened here.

  To force it: stop the relay CLI, create a user in the Clerk dashboard so the webhook cannot be
  delivered, then sign in and open `/tickets`. An empty list rather than a `500` means the
  fallback created the row. Verify with
  `SELECT clerk_user_id, created_at FROM users ORDER BY created_at DESC LIMIT 1;`

**What the measurement caught that a passing status code would not.**

The first run returned `307` and looked finished. The Location header said otherwise:

```
location: https://vital-seal-65.accounts.dev/sign-in?redirect_url=…
```

Clerk's **hosted** sign-in, not the route in this app — which was reachable but dead, since
nothing linked or redirected to it. `auth.protect()` was working; it was pointing somewhere
else.

The fix took two attempts, and the first was wrong in an instructive way. Setting `signInUrl`
on `<ClerkProvider>` changed nothing: `auth.protect()` runs on the **server**, and the
provider's props are React context the server never sees. The server side is configured on
`clerkMiddleware` in `proxy.ts`. Both are now set, and they cover different halves — the
provider governs client-side navigation, the middleware governs the server redirect.

The lesson is the same one T10 taught with `RequireAuth`: **a status code says something
happened, not that the right thing happened.** `307` was true in both cases.

**Also worth recording:** `x-middleware-rewrite: /tickets` in the response is what proves the
proxy passed the request through rather than short-circuiting it. Without that header the
`307` could equally have been Clerk's development-instance handshake, which produces the same
status.

**Dependencies:** T3, T7
**Files:** `web/app/layout.tsx`, `web/proxy.ts`, `web/app/(customer)/layout.tsx`, `web/lib/api.ts`, `web/lib/providers.tsx`
**Scope:** M

---

### T13: Create-ticket form ✅

**Description:** The form, with client validation matching the server's rules.

**Acceptance criteria:**
- [x] Fields: title, description, category, priority — validated with a schema shared in one place
- [x] Server-side `400` field errors render next to their fields
- [x] The submit button is disabled while in flight; double submission is impossible
- [x] On success, the ticket list cache is invalidated and the user lands on the new ticket
- [x] Error state is recoverable — a failure never loses what the user typed

**Verification:**
- [x] Component tests: invalid input blocks submit; server errors render
- [x] `pnpm build` and `pnpm lint` clean
- [ ] Manual: create a ticket end to end against the deployed API

**What "a schema shared in one place" turned out to mean.**

Not a Zod schema. The task was written expecting one, and a Zod schema restating
`maxTitleLength`, the category list and the priority list in TypeScript is precisely the copy
that drifts: the server tightens a bound, the form keeps accepting the old one, and a user
meets a `400` the form promised could not happen.

The forcing observation is that **the copy is not optional**. A form cannot render a category
select without knowing the categories. So the question was never whether to duplicate, only
whether the duplicate is generated or transcribed.

`cmd/gencontract` writes `web/lib/contract.ts` from the same declarations `internal/api`
validates against. `TestGeneratedContractIsUpToDate` fails while the file is stale, and
`TestContractBoundsAreTheOnesValidationEnforces` proves the published bounds by **calling
`Validate` at each boundary** rather than comparing a constant to itself — a contract that
said 100 while the server enforced 200 would fail there.

Client-side validation is then only `required` and `maxlength`, both native, both fed from the
generated file. Everything else — trimming, whitespace-only text, the exact count — is the
API's, and its sentences are rendered verbatim from the RFC 9457 `errors` member, which
`ApiError.fieldErrors` now carries.

**Why there is no optimistic update, despite §9 listing one.**

`docs/spec.md` §9 lists "optimistic update + rollback paths" under frontend tests. That row
belongs to **replies**, in slice 2 — the project brief said "UI optimista para las
respuestas". It does not fit creation.

Optimism pays when the client can predict the result and the user stays where they are.
Creating a ticket satisfies neither: the server assigns the id, the status, the timestamps and
the SLA deadline. An optimistic row would be mostly invented, would visibly change shape once
the real one arrived, and the id it lacks is the one thing needed to navigate to it.

What replaces it costs less and lies about nothing: the response is the real ticket, so it is
written straight into the detail cache and the page it lands on renders with no fetch.
Optimism's benefit, no rollback path to maintain. `invalidateQueries` targets
`ticketKeys.list()` and **not** `ticketKeys.all` — `all` is a prefix of `list`, so it looks
equivalent and passes the obvious test, while also marking the ticket seeded one line earlier
as stale.

**What the mutations caught.** Eight of eleven died first time. The three that survived were
each a test that looked stronger than it was:

| Mutation | Why it survived | Fix |
|---|---|---|
| Drop `required` from the title | The empty-form test was blocked by `description` and `category` anyway | One case per field, every other field filled |
| Neutralise the `ApiError` branch of the form-level message | The test only asserted an alert existed, not what it said | Assert the status appears; add the network-failure case |
| `ticketKeys.list()` → `ticketKeys.all` | `all` is a prefix, so the list is still invalidated | Also assert the seeded detail is **not** invalidated |

**Found while verifying, not fixed:** `internal/auth/middleware.go:52` and `:58` write a bare
`401` and `500` with no body, while every other error in the API is an RFC 9457 problem
document. Confirmed against the running API — `POST /api/tickets` with no token returns
`Content-Length: 0`. It cannot be fixed by calling `api.WriteProblem`, because `api` imports
`auth` and the reverse would be an import cycle; it needs the problem writer moved somewhere
both can import, or passed in. **Nothing in T13 is blocked by it** — a bodyless response gives
`ApiError.fieldErrors === {}`, which is correct, and the form has its own 401 message. Worth a
task of its own.

**Deliberately out of scope.** `/tickets/[id]` exists but is thin — T14 owns it. It was built
now because the form navigates to the ticket it created, and a success path that leads to a
404 is not a finished success path. `Ticket` and `TicketPage` are hand-mirrored in
`web/lib/tickets.ts`, which §8 permits; generating DTO types too would need a Go→TS type
mapping table that no drift test could check, unlike the vocabularies.

**Dependencies:** T9, T12
**Files:** `internal/api/contract.go`, `cmd/gencontract/main.go`, `web/lib/contract.ts`,
`web/lib/api.ts`, `web/lib/tickets.ts`, `web/lib/use-tickets.ts`,
`web/components/ticket-form.tsx`, `web/app/(customer)/tickets/new/page.tsx`,
`web/app/(customer)/tickets/[id]/`, `web/vitest.config.mts`
**Scope:** L — larger than planned, because it also brought up the frontend test harness

---

### T14: Ticket list and detail with SLA display

**Description:** The customer portal views, with a legible SLA timer.

**Acceptance criteria:**
- [ ] List shows title, status, priority, created date, and SLA remaining
- [ ] The SLA timer renders `due_at` **received from the API** — the frontend performs no deadline arithmetic (spec §4.2)
- [ ] A paused clock (`due_at` null) renders as paused, not as "expired" or blank
- [ ] Loading, empty, and error states on both views — no bare spinners, no silent failures
- [ ] Filters (status, priority) live in URL query params; a filtered view is a shareable link
- [ ] Detail view shows the status timeline from history

**Verification:**
- [ ] Component tests for the three states plus the paused-clock case
- [ ] Reloading a filtered URL restores the same filters
- [ ] Manual: a customer sees only their own tickets
- [ ] Manual with keyboard only: every interactive element is reachable and focus is visible

**Dependencies:** T10, T12
**Files:** `web/app/(customer)/tickets/page.tsx`, `web/app/(customer)/tickets/[id]/page.tsx`, `web/components/sla-timer.tsx`, `web/components/ticket-list.tsx`, `web/components/status-timeline.tsx`
**Scope:** L — split into list and detail if it grows past 5 files

> **Checkpoint D — the whole flow works in a browser.**

---

## Phase 4: Ship

### T15: CI pipeline

**Description:** Quality gates on every push. What is not enforced by CI does not hold.

**Acceptance criteria:**
- [ ] GitHub Actions runs `golangci-lint`, `go test -race`, `pnpm lint`, `pnpm build`
- [ ] Integration tests run against a Postgres service container with migrations applied
- [ ] The pipeline fails the build on any failure — no `continue-on-error`
- [ ] Playwright runs the one critical E2E path: sign up → create ticket → see it listed

**Verification:**
- [ ] A pull request with a deliberately failing test is blocked by CI
- [ ] A clean pull request goes green
- [ ] CI runtime under 5 minutes

**Dependencies:** T11, T14
**Files:** `.github/workflows/ci.yml`, `web/e2e/create-ticket.spec.ts`
**Scope:** M

---

### T16: Production deploy + README

**Description:** Ship the real application and write the document that makes it defensible.

**Acceptance criteria:**
- [ ] The full app is deployed; migrations run against production Neon
- [ ] Clerk production instance configured; the webhook endpoint points at the Cloud Run URL and delivers successfully
- [ ] The README explains: the SLA clock model, the fact-vs-cache decision and the consistency test that justifies it, the identity/authorization split, and the webhook race with its resolution
- [ ] The README states what is deliberately **not** built and why
- [ ] Every success criterion in spec §11 is checked

**Verification:**
- [ ] A stranger can sign up on the public URL and create a ticket
- [ ] The Clerk dashboard shows successful webhook deliveries
- [ ] `make check` clean on `main`
- [ ] Someone who has not seen the code can read the README and explain the SLA model back to you

**Dependencies:** T15
**Files:** `README.md`, `fly.toml`, deployment configuration
**Scope:** M

> **Checkpoint E — Slice 1 complete. Do not start Slice 2 until every box above is checked.**

---

## Refactor: one way to report an HTTP failure

Not a numbered task — it came out of T13's verification, and closes the bare-401 note
recorded there.

**What was wrong.** Three ways to report an error in a service that claimed to have one:
`WriteProblem` (problem+json, 7 calls), `http.Error` (text/plain, 6 calls in
`webhooks.go`), and `w.WriteHeader` with no body at all (2 calls in `auth/middleware.go`).
The third was forced: the writer lived in `internal/api`, which imports `internal/auth`, so
the middleware could not reach it.

**What was done.** `internal/httperr` now owns it, and every layer calls the same three
functions. The webhook's `http.Error` calls went with it. Verified live:

```
POST /api/tickets, no token
  before: 401, Content-Length: 0
  after:  401, application/problem+json, 94 bytes
```

**The name.** `internal/shared` was proposed and rejected — see
[ADR 0004](../docs/adr/0004-one-way-to-report-an-http-failure.md). A package named for
being shared has an admission rule that can never reject anything. `httperr` has one that
can: does this decide how an error appears in a response?

**Also from the same investigation.** `api.contains[T ~string]` deleted — `slices.Contains`
already did it, and `internal/ticket/transition.go` was already using it. `uuidString`,
`join`, `encodeCursor`, `decodeCursor` and `pageSize` all checked and left alone: one
consumer each.

**The test that carries the weight** is `TestHTTPErrDependsOnNothingInThisModule`, which
walks the import graph. Adding a single `internal/ticket` import to `httperr` kills it —
verified by mutation. Without that test the package can drift back above a layer that needs
it, and nothing else would notice.

**Mutations:** 5 of 5 dead. Reverting either `auth` call site, reverting one webhook call,
changing the content type, and importing `internal/ticket` into `httperr`.
