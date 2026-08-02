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
- [ ] goose migration creates `users` (`id`, `clerk_user_id` UNIQUE NOT NULL, `email`, `name`, `role`, timestamps) and `sla_policies` (`id`, `name`, `priority`, `budget_minutes`, `schedule_mode`, `active`, `created_at`)
- [ ] `role` and `priority` are constrained by the database (enum or CHECK) — not only by Go
- [ ] Seed migration inserts the four default policies from spec §4.2 (urgent 60, high 240, normal 1440, low 4320)
- [ ] `sqlc.yaml` configured; `make sqlc` generates compiling Go
- [ ] `down` migration reverses cleanly

**Verification:**
- [ ] `make migrate-up && make migrate-down && make migrate-up` runs clean
- [ ] `make sqlc && go build ./...` succeeds
- [ ] `SELECT * FROM sla_policies` returns exactly 4 active rows

**Dependencies:** T1
**Files:** `db/migrations/001_*.sql`, `db/migrations/002_seed_sla_policies.sql`, `db/queries/users.sql`, `sqlc.yaml`
**Scope:** M

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
- [ ] `tickets` includes all `sla_*` columns from spec §4.2
- [ ] `ticket_status_history` created with `from_status` nullable (creation is `NULL → open`)
- [ ] All four indexes from spec §5 exist, including the partial index `tickets (sla_due_at) WHERE sla_breached_at IS NULL`
- [ ] `status` and `priority` constrained by the database
- [ ] sqlc queries: create ticket, insert history row, list by requester, get by id **scoped by requester**, read history by ticket

**Verification:**
- [ ] `make migrate-up && make migrate-down && make migrate-up` clean
- [ ] `make sqlc && go build ./...` succeeds
- [ ] `EXPLAIN` on the breach worker query from spec §4.2 shows an index scan, not a sequential scan

**Dependencies:** T4
**Files:** `db/migrations/003_*.sql`, `db/queries/tickets.sql`, `db/queries/ticket_status_history.sql`
**Scope:** M

> **Checkpoint B — migrations reversible, sqlc compiles, `internal/sla` green.**

---

## Phase 2: Auth and the tickets API

### T7: Clerk JWT verification, `RequireAuth`, lazy upsert ⚠️ HIGH RISK

**Description:** Verify the Clerk session JWT against the JWKS, resolve it to a local
user, and reject everything else.

**Re-read the `clerk-sdk-go/v2` docs before writing code. Do not code from memory.**

**Acceptance criteria:**
- [ ] JWT verified against Clerk's JWKS; the JWK is cached, not fetched per request
- [ ] `RequireAuth` **rejects** unauthenticated requests with `401` — `WithHeaderAuthorization` alone does not do this (spec §4.3)
- [ ] The authenticated user, including the role **read from our database**, is placed in the request context
- [ ] A verified token with no matching `users` row triggers the idempotent upsert (spec §4.5) and the request succeeds
- [ ] New users are always seeded `role = 'customer'`; the role is never read from the token or any client input

**Verification:**
- [ ] Integration tests return `401` for: no header, malformed header, expired token, token signed by the wrong key
- [ ] A test with a valid token for an unknown subject returns `200` and creates exactly one `users` row
- [ ] Calling the upsert twice concurrently creates exactly one row
- [ ] `go test ./internal/auth/... -race` passes

**Dependencies:** T4
**Files:** `internal/auth/clerk.go`, `internal/auth/middleware.go`, `internal/auth/provision.go`, `internal/auth/middleware_test.go`
**Scope:** M

---

### T8: Clerk webhook with Svix verification

**Description:** `POST /api/webhooks/clerk`, handling `user.created` and `user.updated`.

**Read the `svix-webhooks/go` docs first** — only the library name and the header names are
confirmed so far.

**Acceptance criteria:**
- [ ] Signature verified with `github.com/svix/svix-webhooks/go` using `svix-id`, `svix-timestamp`, `svix-signature`
- [ ] An invalid or missing signature returns `400` and writes nothing
- [ ] The route is exempt from `RequireAuth` — Clerk does not send a session JWT
- [ ] The handler calls the **same** upsert function as T7
- [ ] Replaying an identical event produces no duplicate row and still returns `2xx` (Svix retries on non-2xx)
- [ ] Unknown event types return `200` and are ignored, not treated as errors

**Verification:**
- [ ] Integration test with a correctly signed fixture payload creates the user
- [ ] Test with a tampered body returns `400`
- [ ] Delivering the same event twice leaves exactly one row
- [ ] Manual: `svix listen` forwards a real Clerk signup to localhost and the row appears

**Dependencies:** T7
**Files:** `internal/api/webhooks.go`, `internal/api/webhooks_test.go`, `internal/api/testdata/user_created.json`
**Scope:** M

---

### T9: `POST /api/tickets`

**Description:** Create a ticket: resolve the SLA policy from priority, start the clock,
and write the initial history row — all in one transaction.

**Acceptance criteria:**
- [ ] Validates `title`, `description`, `category`, `priority`; invalid input returns `400` with per-field errors
- [ ] `requester_id` comes from the auth context, **never** from the request body
- [ ] The policy is looked up from `sla_policies` by priority and snapshotted into `sla_policy_id`
- [ ] `sla_clock_started_at = now()` and `sla_due_at` computed by `internal/sla` — no arithmetic in the handler
- [ ] The ticket row and the `NULL → open` history row are written in **one transaction**; a failure writes neither

**Verification:**
- [ ] Integration test: valid request returns `201` with the created ticket and a non-NULL `sla_due_at`
- [ ] `sla_due_at` equals `now + policy budget` for an unpaused ticket, within tolerance
- [ ] Each priority resolves to its seeded budget
- [ ] A forced failure after the ticket insert leaves **zero** rows in both tables
- [ ] A body containing `requester_id` for another user is ignored

**Dependencies:** T5, T6, T7
**Files:** `internal/api/tickets.go`, `internal/api/dto.go`, `internal/store/ticket_repo.go`, `internal/api/tickets_test.go`
**Scope:** M

---

### T10: `GET /api/tickets` and `GET /api/tickets/{id}`

**Description:** Read endpoints, scoped to the caller in SQL.

**Acceptance criteria:**
- [ ] `GET /api/tickets` returns only the caller's tickets, newest first, paginated
- [ ] `GET /api/tickets/{id}` returns `404` when the ticket belongs to someone else — **not `403`**, which would confirm the ticket exists
- [ ] Scoping is in the SQL (`WHERE requester_id = $1`), not a post-query filter in Go
- [ ] Responses expose `sla_due_at` and enough state for the UI to render a timer

**Verification:**
- [ ] Integration test: customer A creates a ticket; customer B requests it by id and receives `404`
- [ ] Integration test: customer B's list does not contain A's ticket
- [ ] Removing the handler's ownership check still yields `404` — proving the SQL enforces it
- [ ] Pagination returns stable results across pages

**Dependencies:** T9
**Files:** `internal/api/tickets.go`, `db/queries/tickets.sql`, `internal/api/tickets_test.go`
**Scope:** S

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
- [ ] Consistency test, against a real database: the values stored in `tickets.sla_*` equal `Reconstruct` over the rows stored in `ticket_status_history` for that ticket
- [ ] It is property-based over generated transition sequences, not a handful of examples
- [ ] It detects each of these write-path defects, verified by deliberately introducing them: cache update skipped; fact and cache written in separate transactions; history read *before* the new row was inserted (cache one event behind); partial write committed
- [ ] Architecture test: `internal/sla` and `internal/ticket` import nothing from `internal/store`, `internal/api`, or `database/sql`
- [ ] Architecture test: **`internal/ticket` never imports `internal/sla`** — the dependency runs one way only ([ADR 0002](../docs/adr/0002-validation-ownership-between-sla-and-ticket.md))
- [ ] All wired into `make check`

**Verification:**
- [ ] `make test-int` passes
- [ ] Deliberately corrupting `sla_consumed_minutes` in a fixture makes the consistency test **fail** — a test that cannot fail proves nothing
- [ ] Adding an import of `database/sql` to `internal/sla` makes the architecture test fail

**Dependencies:** T9, T10
**Files:** `internal/sla/consistency_int_test.go`, `internal/architecture_test.go`
**Scope:** S

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

**Verify before writing any code:** does the installed Clerk version support Next 16's
`proxy`? `clerkMiddleware` has historically targeted the edge runtime. If it does not yet,
route protection has to be enforced another way, and the fallback must be decided before
implementation rather than discovered during it.

**Acceptance criteria:**
- [ ] `<ClerkProvider>` mounted; sign-in and sign-up pages render
- [ ] `web/proxy.ts` (**not** `middleware.ts`) protects `/(customer)/*`; signed-out visitors are redirected
- [ ] A test or manual check proves an unauthenticated request to a protected route is actually redirected — not merely that the file exists
- [ ] The API client attaches the Clerk session token to every request from one place — never per component
- [ ] TanStack Query provider configured with sane defaults
- [ ] A `401` from the API is handled globally, not per call site

**Verification:**
- [ ] `pnpm build` succeeds
- [ ] Manual: signed out, `/tickets` redirects to sign-in
- [ ] Manual: signed in, a request to the API carries the `Authorization` header
- [ ] Manual: a brand-new signup can reach the app immediately — the T7 lazy upsert covers the webhook race

**Dependencies:** T3, T7
**Files:** `web/app/layout.tsx`, `web/proxy.ts`, `web/app/(customer)/layout.tsx`, `web/lib/api.ts`, `web/lib/providers.tsx`
**Scope:** M

---

### T13: Create-ticket form

**Description:** The form, with client validation matching the server's rules.

**Acceptance criteria:**
- [ ] Fields: title, description, category, priority — validated with a schema shared in one place
- [ ] Server-side `400` field errors render next to their fields
- [ ] The submit button is disabled while in flight; double submission is impossible
- [ ] On success, the ticket list cache is invalidated and the user lands on the new ticket
- [ ] Error state is recoverable — a failure never loses what the user typed

**Verification:**
- [ ] Component tests: invalid input blocks submit; server errors render
- [ ] `pnpm build` and `pnpm lint` clean
- [ ] Manual: create a ticket end to end against the deployed API

**Dependencies:** T9, T12
**Files:** `web/app/(customer)/tickets/new/page.tsx`, `web/components/ticket-form.tsx`, `web/lib/schemas.ts`
**Scope:** M

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
