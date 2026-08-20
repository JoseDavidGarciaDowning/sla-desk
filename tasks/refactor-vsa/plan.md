# Implementation Plan: Vertical Slices — where a use case lives

Status: **Approved**
Architecture: [`docs/architecture.md`](../../docs/architecture.md)
Last updated: 2026-08-20

---

## Overview

The backend already is a modular monolith with three modules that do not know each other
exists, a pure domain in each, module-owned sqlc configs, a composition root, and seven
boundary rules enforced on every CI build. None of that changes here.

What changes is the layer between a route and a query. Today it is organised by technical
kind — `application/` holds every use case of a module in one `Service`, `transport/http/`
holds every handler and every DTO. After this it is organised by use case: one directory
per thing the system does, holding the handler, its contract, its HTTP adapter and its
tests.

**This refactor moves code and changes no behavior.** Every route, every query, every
status code and every response body is the same on the other side. The tests that prove
that are the ones already in the repository, and they move with the code they cover.

---

## What reading the code changed

The brief this started from assumed a layered `controllers/services/repositories` backend.
That is not what is here, and three of its sections were already true and machine-verified
before a line was written. Reading first is what turned a rewrite into a move.

### 1. Most of the target already exists

Modules, hexagonal dependency direction, module-owned `sqlc.yaml`, `queries/` next to the
module that owns them, `platform/` for cross-cutting infrastructure, `internal/app` as the
only package allowed to know two modules exist — all present, all tested by
`internal/architecture`. Sections 1, 3, 7, 8, 9, 10 and 11 of the brief describe the repo
as it already stood.

### 2. The pain is not in `application/`, it is in `transport/http/`

`application/service.go` is 132 lines for nine use cases, and seven of its methods are
three-line delegations. Splitting *that* into nine packages is the ceremony the progressive
complexity rule exists to refuse.

The file that cannot be navigated is `transport/http/handlers.go`: 346 lines holding four
unrelated endpoints. `queue.go` holds three more in 237. `dto.go` holds all nine contracts
in 263. `repository.go` holds all ten queries in 480. Nothing is named after the thing you
are looking for, and that is the whole complaint.

| Endpoint | Handler in | DTO in | Repo method in |
|---|---|---|---|
| `POST /tickets` | `handlers.go` | `dto.go` | `repository.go` |
| `GET /tickets` | `handlers.go` | `dto.go` | `repository.go` |
| `GET /tickets/{id}` | `handlers.go` | `dto.go` | `repository.go` |
| `GET /tickets/{id}/history` | `handlers.go` | `dto.go` | `repository.go` |
| `GET /agent/queue` | `queue.go` | `dto.go` | `repository.go` |
| `GET /agent/queue/{id}` | `queue.go` | `dto.go` | `repository.go` |
| `GET /agent/queue/{id}/history` | `queue.go` | `dto.go` | `repository.go` |
| `PATCH /agent/queue/{id}/assignee` | `assign.go` | — | `repository.go` |
| `POST /agent/queue/{id}/transitions` | `transition.go` | — | `repository.go` |

### 3. Go refuses the obvious way to write a feature-owned port

A feature declaring its own persistence interface and a shared adapter satisfying it
sounds like it should work by structural typing. It does not, and the compiler says so:

```
have Create(postgres.NewTicket) error
want Create(create.NewTicket) error
```

Structural typing applies to method *sets*, not to *parameter types* — a defined type is
identical only to itself. The adapter must import the feature to name its command type.
That is the correct hexagonal direction and it is the subject of ADR 0012.

---

## Architecture Decisions

### A. A feature owns its handler, its contract and its HTTP adapter. It does not own its route

Each feature directory holds `handler.go`, and `dto.go` and `http.go` when it needs them.
The module keeps one `transport/http/routes.go` with the two lists — `Routes` and
`AgentRoutes` — and mounts each feature's adapter from there.

Features must never self-mount. Those two lists are not bookkeeping: rule 7 says a query
with no requester predicate is mounted under `/api/agent` and nowhere else, and the two
separate lists are what make that visible to a reviewer on one screen. Nine files each
mounting themselves would dissolve the only place that guarantee can be read.

Rejected: per-feature `Routes(r)`. It buys nothing and costs the rule ADR 0011 exists for.

### B. One repository adapter, split across cohesive files

`postgres.Repository` stays one type with one constructor. `repository.go` keeps the type,
its dependencies and its constructor; the ten methods move to `create.go`, `list.go`,
`get.go`, `history.go`, `queue.go`, `assign.go` and `transition.go`.

File boundaries do not imply type boundaries. Findability is a file-naming problem, and
splitting the file solves it; splitting the *type* would give nine adapters, nine
constructor call sites, and nine copies of the transaction handling that ADR 0008 depends
on.

Prefer one shared adapter wherever features share a persistence boundary, transaction
handling and infrastructure dependencies. Introduce a second only when a feature genuinely
needs an independent one.

### C. Ports are declared by the feature that needs them. `ports/` is for what crosses features

The feature declares the persistence interface it needs, in its own words, holding its own
command type. `ports/` holds only what more than one feature genuinely shares:
`SLAPolicies`, `SLAClock`, `ClockState`, `AssigneeDirectory`, and the module's own `Caller`
and `CallerResolver`.

There is no central `ports.go` catch-all for commands, filters, errors and shared types. A
type lives with the feature or the domain concept that owns it. This is also what avoids
the import cycle a central ports package would create the moment it named a feature's
command.

**Consequence, and it looks wrong until you know why:** `postgres/create.go` imports
`features/create` to name `create.NewTicket` in its signature. That is the adapter
depending inward on the port it implements — textbook hexagonal, and the only arrangement
Go's type identity rule permits. See ADR 0012.

### D. `Service` dissolves

`application.Service` was one object with nine methods and three dependencies, most of
whose methods only forwarded. Each feature's `Handler` now holds exactly what it needs:
`create.Handler` takes a store and an SLA resolver, `list.Handler` takes a store.

`module.go` grows a constructor block wiring the seven handlers from the shared repository
and the module's two contracts. That is the right place for it — ADR 0009 already makes
the module's front door the place where a module is assembled.

Rejected: keeping `Service` as a facade over the features. It would leave the nine-method
object that is the thing nobody could navigate, purely to avoid editing `router.go`.

### E. Every module gets `features/`, including the small ones

`sla` has two operations and no HTTP surface. `identity` has three use cases and two
middlewares. Neither decomposes the way `ticket` does, and a case-by-case rule would have
been defensible.

It is still `features/` everywhere. The convention is worth more than the two directories
it saves: an agent that has to decide *whether* a module gets `features/` will decide
wrongly, and "why is sla different" is a question with no good answer in a file the next
person reads. What varies is how much each feature is split internally, not whether the
directory exists.

Middleware stays in `transport/http`. `RequireAuth` and `RequireRole` are HTTP behavior,
not use cases, and rule 9 is phrased so the distinction is mechanical rather than a
judgement call.

### F. One directory per concept, not per method or per audience

Seven ticket features, not nine. `GET /tickets/{id}` and `GET /agent/queue/{id}` read the
same ticket under different guarantees — one scoped by requester in SQL, one not — so they
share `features/get/` as `handler.go` and `agent.go`. Same for history.

Two files side by side make the scoped/unscoped distinction *more* visible than two
directories twelve lines apart in a listing, and the distinction is exactly what a reader
must not miss.

Rejected: `features/customer/` and `features/agent/`. Splitting the domain by permission is
the mistake ADR 0011 already avoided once.

### G. Two new rules, enforced twice

```
8. No feature reaches another feature, even indirectly.
9. transport/http contains no function returning http.Handler.
```

Rule 8 is the vertical-slice invariant, and without it the slices grow into each other
through an innocent-looking helper — which `architecture_test.go` already documents as the
shape these violations actually take. It goes in **both** the architecture test and
depguard: the graph walk is transitive but blind to `//go:build integration` files, and a
feature's integration test borrowing another feature's fixture is precisely the case that
gap lets through. `docs/architecture.md` already measured this in both directions.

Rule 9 is mechanically precise because middleware returns `func(http.Handler) http.Handler`
rather than `http.Handler`, so it is exempt by construction rather than by exception.

A third assertion checks every module has a `features/` directory, which turns decision E
from a convention into a build failure.

### H. Delivery: six sequential PRs, none stacked

The project rule is that every PR targets `main` and leaves it deployable alone; if work
must chain, the second opens when the first merges
(`sla-desk-pr-workflow-cheatsheet.md`). Slice 2 shipped four PRs that way.

| PR | Content | Deployed means |
|---|---|---|
| 1 | `decodeBody` → `httpx`; `<module>db/` → `generated/` ×3 | Nothing changed — mechanical |
| 2 | ticket customer features: create, list, get, history | The customer path runs on slices |
| 3 | ticket agent features: queue, assign, transition. `Service` deleted, `ports/` final | The whole ticket module runs on slices |
| 4 | identity: provision, assignable, me | |
| 5 | sla: resolve | All three modules converted |
| 6 | Rules 8–9, depguard, `architecture.md`, `AGENTS.md`/`CLAUDE.md`, ADR 0012 | The convention is enforced by CI |

PR 2 leaves `Service` half-dissolved, holding only the methods PR 3 removes. That is stated
in the PR description rather than hidden: an intermediate state a reviewer can see is
cheaper than one they discover.

Rules 8–9 land last because they cannot go green until PR 5 merges. A red rule in `main`
would break `make arch`, and a PR that leaves CI red is the one thing the project rule
forbids outright.

---

## File Disposition

Every file in the blast radius, and what happens to it.

### `internal/modules/ticket`

| File | Action |
|---|---|
| `domain/*.go` | **kept** — plus a new `errors.go` for `ErrTicketNotFound` and `ErrNoSLAPolicy` |
| `application/service.go` | **deleted** — dissolves into seven handlers |
| `application/ports.go` | **split four ways** — commands to their feature, contracts to `ports/`, two errors to `domain/errors.go`, two to their feature |
| `application/service_queue_test.go` | **moved** → `features/queue` |
| `application/assign_test.go` | **moved** → `features/assign` |
| `transport/http/handlers.go` | **split** → `create`, `list`, `get`, `history` |
| `transport/http/queue.go` | **split** → `queue`, `get/agent.go`, `history/agent.go` |
| `transport/http/dto.go` | **split** → per-feature `dto.go` |
| `transport/http/assign.go` | **moved** → `features/assign` |
| `transport/http/transition.go` | **moved** → `features/transition` |
| `transport/http/caller.go` | **split** → `ports/caller.go` + `transport/http/routes.go` |
| `transport/http/body.go` | **moved** → `internal/platform/httpx` |
| `transport/http/contract.go` | **kept** — module-wide vocabulary, spans every feature |
| `transport/http/*_test.go` | **split, following the code they cover** |
| `infrastructure/postgres/repository.go` | **split by file** — one type preserved |
| `infrastructure/postgres/ticketdb/` | **moved** → `generated/`, package name unchanged |
| `infrastructure/postgres/sqlc.yaml` | **edited** — `out:` path only |
| `infrastructure/postgres/queries/*.sql` | **kept** |
| `infrastructure/postgres/*_integration_test.go` | **kept** |
| `module.go` | **rewritten** — constructs seven handlers |

### `internal/modules/identity`

| File | Action |
|---|---|
| `application/service.go` | **split** → `features/provision`, `features/assignable` |
| `transport/http/webhook.go` | **moved** → `features/provision` |
| `transport/http/assignable.go` | **moved** → `features/assignable` |
| `transport/http/me.go` | **moved** → `features/me` (one file; it calls no service) |
| `transport/http/middleware.go`, `require_role.go` | **kept** — middleware is not a use case |
| `infrastructure/postgres/identitydb/` | **moved** → `generated/` |
| `authentication_test.go`, `domain/*`, `infrastructure/clerk/*` | **kept** |

### `internal/modules/sla`

| File | Action |
|---|---|
| `application/policies.go` | **moved** → `features/resolve/` |
| `infrastructure/postgres/sladb/` | **moved** → `generated/` |
| `domain/*`, `infrastructure/postgres/repository.go`, `queries/` | **kept** |

### Outside the modules

| File | Action |
|---|---|
| `internal/platform/httpx/` | **gains** `body.go` |
| `internal/architecture/architecture_test.go` | **gains** rules 8, 9 and the `features/` assertion |
| `.golangci.yml` | **gains** a depguard entry for rule 8 |
| `internal/app/router.go` | **edited** — passes `*ticket.Module` rather than `.Service` |
| `cmd/gencontract/main.go` | **unchanged** — `contract.go` does not move |
| `db/migrations/`, `docs/spec.md`, `web/` | **untouched** |

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| A behavior change slips in under a move | Every existing test moves with its code and stays green at every commit. No test is rewritten; a test that needs editing to pass is a behavior change and stops the commit |
| `postgres/create.go` importing `features/create` gets "fixed" later | ADR 0012, written in PR 6, with the compiler output that forces it |
| Import path `…/postgres/generated` with package `ticketdb` confuses tooling | Explicit alias at every import site. Legal Go; the alias is for the reader |
| Rule 9 accidentally forbids middleware | Phrased as "returns `http.Handler`". Middleware returns `func(http.Handler) http.Handler` and is exempt by construction |
| PR 2's half-dissolved `Service` is mistaken for the end state | Stated in the PR description and in a comment on the type itself |
| The 789-line `handlers_test.go` split loses a case | Case count asserted before and after; `go test -run` over the old names before deletion |

---

## Open Questions

None. Nineteen decisions were settled before this file was written.
