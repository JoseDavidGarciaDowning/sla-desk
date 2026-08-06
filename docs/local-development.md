# Running this locally

Last updated: 2026-08-03

Everything needed to go from a fresh clone to a working local stack, plus the traps that
have actually caught someone here.

---

## 1. What has to be running

Four processes. The first two are always needed; the third only when working on the
frontend; the fourth only when testing Clerk webhooks.

| | Command | Port | |
|---|---|---|---|
| Infrastructure | `make up` | 5433, 6380 | Postgres and Redis in containers |
| API | `make api` | 8080 | The Go API |
| Web | `cd web && pnpm dev` | 3000 | Next.js |
| Webhook relay | see §4 | — | Only for Clerk webhooks |

**The ports are deliberately non-standard.** Postgres is on **5433** and Redis on **6380**
because this machine also runs another project on 5432 and 6379. Inside the containers
they are still 5432 and 6379; only the host mapping moved.

---

## 2. Environment

Two files, neither committed. `.env.example` is committed and holds placeholders.

### `.env` — the Go API

```
DATABASE_URL=postgres://sladesk:sladesk@localhost:5433/sladesk?sslmode=disable
PORT=8081                      # optional; 8080 is the default

CLERK_SECRET_KEY=sk_test_...   # required — the API will not start without it
CLERK_WEBHOOK_SECRET=whsec_... # required — same

CORS_ALLOWED_ORIGIN=http://localhost:3000
CLERK_AUTHORIZED_PARTY=        # optional; defaults to CORS_ALLOWED_ORIGIN
CLERK_API_URL=                 # optional; for a Clerk proxy
REDIS_URL=                     # unused until slice 5

AGENT_CLERK_USER_IDS=          # optional; comma-separated Clerk subjects
ADMIN_CLERK_USER_IDS=          # optional; same
```

### How you become an agent

`AGENT_CLERK_USER_IDS` and `ADMIN_CLERK_USER_IDS` are how a `users` row comes to
hold anything other than `customer`. Empty means nobody, which is how slice 1
ran and how CI runs.

To make yourself an agent locally:

1. Sign in to the app once, so the row exists.
2. Find your subject — it is the `clerk_user_id` column:
   ```sql
   SELECT clerk_user_id, email, role FROM users ORDER BY created_at DESC LIMIT 5;
   ```
   It looks like `user_2abc123XYZ`. The Clerk dashboard shows the same value as
   the user's ID.
3. Put it in `.env` and restart the API:
   ```
   AGENT_CLERK_USER_IDS=user_2abc123XYZ
   ```
4. Make one authenticated request. `RequireAuth` calls `EnsureUser` on every
   request, so the promotion lands on that one — there is nothing to run.

Three things worth knowing, all of them deliberate:

- **The subject differs between Clerk instances.** Your development id is not
  your production id, which is exactly why this is configuration and not a
  migration. See `tasks/slice-2/plan.md` decision A.
- **Removing an id does not demote you.** The SQL refuses to write `customer`,
  so a typo in the variable cannot strip an agent mid-shift. Taking a role away
  is slice 9's admin endpoint. To undo it locally, write the column by hand.
- **The same id in both lists stops the boot**, with an error naming the
  subject. There is no defensible winner, and picking one by map order would
  make the deployed role depend on nothing you can read.

Startup logs `role grants loaded agents=N admins=N` — counts only, never the
identifiers, because a log naming the agents is an inventory of privileged
accounts.

### `web/.env.local` — the Next.js app

```
NEXT_PUBLIC_API_URL=http://localhost:8080
NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY=pk_test_...
CLERK_SECRET_KEY=sk_test_...   # for auth() in the (customer) layout, which runs on the server
```

The two Clerk secrets are required on purpose. A process that booted without them could
only answer `/health`, and nobody would find out until a user failed to sign in.

> **`CORS_ALLOWED_ORIGIN` must be `http://localhost:3000` in development.** It is compared
> against the token's `azp` claim, which carries the origin the browser asked from. Left
> pointing at the Vercel URL, every request is refused with a `401` while the token is
> perfectly valid. Explained in [clerk-integration.md](clerk-integration.md) §3.

> **`NEXT_PUBLIC_*` is inlined at build time, not read at runtime.** `pnpm dev` re-reads
> it; a `pnpm build` does not. Change one and rebuild, or the old value stays in the
> bundle.

---

## 3. Commands

```bash
make up              # Postgres + Redis, waits for healthy
make down            # stop, keeping volumes

make migrate-up      # apply migrations
make migrate-down    # roll back one
make migrate-reset   # all the way down, then up again
make migrate-status
make migrate-new name=add_comments

make sqlc            # regenerate every module's queries (finds the configs itself)
make contract        # regenerate web/lib/contract.ts from the API's own bounds
make api             # run the API

make lint            # golangci-lint, twice: plain and with the integration tag
make fmt             # gofumpt, through golangci-lint
make check           # lint + tests. Must pass before every commit
make test-go         # unit tests, no database
make test-int        # integration tests — needs `make up` first
make web-test        # Vitest — needs `make web-install` first
make e2e             # Playwright, end to end — see below
make smoke           # check the deployed application. Reads only
```

### The end-to-end suite

`make e2e` drives a real browser through sign-up, creating a ticket, and finding it in the
list. It needs the whole stack up — `make up`, `make api`, and a Clerk **development**
instance, whose keys it reads from `web/.env.local`.

**It must run on the port the API's `CORS_ALLOWED_ORIGIN` names**, which is `3000` by
default. Run it anywhere else and every request from the browser is refused; the test says
so rather than reporting a missing element, but the fix is the port and not the test.

`make smoke` checks `sla-desk.josegd.me` instead — health, the sign-out redirect, the CORS
origin, and Clerk's certificate. Every check is a read, so it is safe to run at any time.

`goose`, `sqlc` and `golangci-lint` are pinned as tool dependencies in `go.mod` and run
through `go tool`. A fresh clone needs nothing installed beyond Go, and CI lints with the
same version you do — an installed-separately linter drifts, and the first anyone hears of
it is a pull request that is red for nobody's mistake.

```bash
cd web
pnpm dev             # 3000
pnpm build           # also type-checks
pnpm lint
pnpm test            # Vitest, one run
pnpm test:watch      # Vitest, watching
```

### The generated contract

`web/lib/contract.ts` is written by `cmd/gencontract` and must never be edited by hand.
It carries the category and priority vocabularies and the field length limits, read from
the same declarations the ticket module validates against — the form needs them to render
its selects at all, so the only question was whether that copy is generated or
transcribed.

Change a bound or add a category in `internal/modules/ticket/transport/http/dto.go`, then run `make contract`.
Forgetting fails `TestGeneratedContractIsUpToDate`, which `make check` runs.

---

## 4. Clerk webhooks locally

```bash
npx clerk@latest webhooks listen \
  --token c_YOUR_RELAY_TOKEN \
  --forward-to http://localhost:8080/api/webhooks/clerk
```

The token is printed by the CLI the first time it runs, and it is on the endpoint's page
in the Clerk dashboard. It identifies **your** relay, so it is not committed — the value
above is a placeholder.

**Always pass `--token`.** Without it the relay URL can change across machines or a
cleared config, and the dashboard endpoint — with its signing secret — is tied to that
URL. When it changes, Clerk keeps delivering to a URL that forwards nowhere: no error,
just events that never arrive.

Register the relay URL in the dashboard **exactly as printed**, without appending the
path. Full walkthrough and the reasoning in [clerk-integration.md](clerk-integration.md).

The CLI only listens. Nothing happens until a user is created — from the dashboard, or by
signing up once there is a frontend.

---

## 5. Traps that have caught someone here

**`make test-int` prints `ok` when every test skips.** The integration tests call
`t.Skip` without `DATABASE_URL`, and `go test` reports success for a run that asserted
nothing. The target now fails loudly instead. If it ever stops doing that, a green
integration run means nothing.

**A linter does not see build-tagged files.** `make lint` runs `golangci-lint` twice, the
second time with `--build-tags=integration`, or the integration tests rot unnoticed until
someone next runs them.

**`$path` is a reserved array in zsh, tied to `PATH`.** A loop written as
`for path in / /sign-in; do …` silently destroys `PATH` for the rest of the shell, and
every subsequent command reports "not found". Use any other variable name.

**Hydration warnings mentioning `cz-shortcut-listen`** come from the ColorZilla browser
extension, not from this code. React's own message lists extensions as a cause. It
disappears in a private window.

**`pnpm start` logs no requests.** Only `pnpm dev` does. A route that appears not to be
hit in production mode is probably just not being logged.

**A form test that submits an entirely empty form proves nothing.** jsdom does enforce
`required`, so the submission is blocked — by whichever field still has the attribute.
Dropping `required` from the title left the test green. Every required-field test fills
the other fields, so only the field under test can be what blocks it.

**`make help` printed "Makefile" as the name of every target.** `-include .env` puts a second
entry in `MAKEFILE_LIST`, and grep prefixes each line with the filename once it has more than
one file — so `awk` split on `Makefile:` instead of on the target. It worked on a fresh clone
and nowhere else. `grep -h` fixes it. The same target's pattern also lacked `[0-9]`, so `e2e`
never appeared at all.

**Vitest runs `*.spec.ts`, which is also what Playwright calls its files.** Until `e2e/**`
was excluded in `vitest.config.mts`, `make check` tried to run the browser suite inside jsdom
and failed on the `@playwright/test` import.

**Never write a Vitest hook with a concise arrow body that returns something.**

```ts
beforeEach(() => apiFetch.mockReset());   // hangs for 10s, then every test fails
beforeEach(() => { apiFetch.mockReset(); });  // correct
```

`mockReset()` returns the mock, the arrow returns it, and Vitest awaits whatever a hook
returns. The hook times out after 10 seconds and **every test in the file fails**, each with
an error pointing at the hook rather than at the arrow — so the symptom looks like the
component or the mock being broken. It cost a while to find. The same applies to
`afterEach(() => vi.clearAllMocks())`.

---

## 6. Checking things by hand

```bash
# health, including the database probe
curl -s localhost:8080/health | python3 -m json.tool

# who is provisioned
docker compose exec -T postgres psql -U sladesk -d sladesk \
  -c "SELECT clerk_user_id, email, role, created_at FROM users ORDER BY created_at;"

# the seeded SLA policies
docker compose exec -T postgres psql -U sladesk -d sladesk \
  -c "SELECT name, priority, budget_minutes FROM sla_policies WHERE active ORDER BY budget_minutes;"

# a ticket's history, which the SLA clock is rebuilt from
docker compose exec -T postgres psql -U sladesk -d sladesk \
  -c "SELECT from_status, to_status, actor_role, created_at FROM ticket_status_history ORDER BY id;"
```

Route surface without a session, which is what T12 asserts:

```bash
cd web && pnpm build && PORT=3100 pnpm start
# then
curl -s -o /dev/null -w "%{http_code} -> %{redirect_url}\n" http://localhost:3100/tickets
# 307 -> http://localhost:3100/sign-in?redirect_url=...
```

A `307` alone is not the answer — Clerk's development-instance handshake produces one too.
The response also has to carry `x-middleware-rewrite: /tickets`, which is what proves the
proxy passed the request through instead of short-circuiting it.

---

## Related

- [spec.md](spec.md) — what is being built and why
- [clerk-integration.md](clerk-integration.md) — the two request paths, `azp`, the silent 401
- [checkpoint-c.md](checkpoint-c.md) — the backend as it stands
- [adr/](adr/) — the decisions that needed a record
