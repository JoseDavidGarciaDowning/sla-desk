# Architecture

The Go backend is a **modular monolith**: one deployable binary, three business modules
that do not know each other exists, and rules that fail the build when one of them starts
to.

This document is for the two questions that come up in practice — *where does this code go*
and *why did the linter just stop me* — and it is meant to be edited when the answers
change.

## The map

```
cmd/
  api/                    builds internal/app and serves it
  gencontract/            writes web/lib/contract.ts from the ticket module

internal/
  app/                    COMPOSITION ROOT. The only package that may import
                          two modules. Router, CORS, health, and the adapters

  modules/
    ticket/               owns tickets, ticket_status_history, the status machine
    sla/                  owns sla_policies. The only deadline arithmetic
    identity/             owns users. Clerk verification and provisioning

  platform/               cross-cutting infrastructure ONLY:
    config/               environment loading, no globals
    httperr/              what a failure looks like on the wire (RFC 9457)
    httpx/                writing a JSON body, CORS

  architecture/           the boundary rules, as a test

db/migrations/            goose. Global: one database, one sequence
```

Every module has the same shape:

```
internal/modules/<module>/
├── domain/               entities, value objects, rules, errors. Imports nothing
├── features/             ONE DIRECTORY PER USE CASE
│   └── <use case>/       handler.go, and dto.go / http.go when it needs them
├── ports/                contracts more than one feature shares  (identity has none)
├── infrastructure/
│   └── postgres/
│       ├── sqlc.yaml     this module's tables and nobody else's
│       ├── queries/*.sql SQL we write
│       └── generated/    SQLC output. Package <module>db. Never edited by hand
├── transport/http/       paths, the route table, shared wire shapes, middleware
│                         — and no endpoint  (sla has none: nothing calls it directly)
└── module.go             the front door. Builds the features, hands them to the routes
```

**`features/` is where a use case lives, and the answer does not depend on the module.**
`ticket` has seven, `identity` three, `sla` one. A module with a single feature is the
convention working, not ceremony: nobody has to decide per module whether it is big enough
to deserve the directory, and nobody has to answer "why is sla different" later.

What varies is how much a feature is split *inside*:

```
features/me/          http.go                          — calls no use case
features/get/         handler.go http.go agent.go      — two guarantees, side by side
features/create/      handler.go dto.go http.go        — plus a request contract
```

Add a file when the code in it stops fitting; never to complete a pattern.

## Where does this code go?

| If it… | it belongs in |
|---|---|
| is a rule about what a ticket *is* | `modules/ticket/domain` |
| is something the system **does** | `modules/ticket/features/<use case>/` |
| is the request or response shape of **one** endpoint | that endpoint's feature |
| is a contract **one** feature needs from outside | that feature, beside its handler |
| is a contract **more than one** feature needs | `modules/<m>/ports` |
| is a wire shape **more than one** feature returns | `modules/<m>/transport/http` |
| decides whether a request may proceed at all (middleware) | `modules/<m>/transport/http` |
| talks to Postgres, Clerk, or any other system | `modules/<m>/infrastructure` |
| connects two modules, or decides what a route sits behind | `internal/app` |
| decides how bytes get onto the wire without knowing what they mean | `internal/platform` |

There is no `application` layer any more, and no `services/`, `controllers/`,
`repositories/` or `dto/` directory anywhere. A use case is not a technical layer; it is a
thing the system does, and it lives in one directory named after it.

The `platform` row is the one that gets abused, so it has a sharper test. The admission
question is **not** "is it used more than once" — that is a fact about the call graph, not
about the concept. It is:

> Does this decide something no business module owns?

`httperr` passes: what an RFC 9457 document looks like is nobody's domain. A helper that
knew what a ticket is would fail, and belongs in the ticket module however many places call
it.

## The rules

Nine. Eight are enforced, and each was watched failing before it was trusted.

1. **A module never imports another module.** What it needs, it declares as a contract in
   its own `ports` (or in the one feature that needs it), and `internal/app` connects the
   two ends.
2. **A domain package imports nothing** — no database, no HTTP, no router, no SDK, not even
   our own `platform`. Everything it needs is an argument.
3. **`internal/app` is a sink.** Everything may be reached from it; nothing may reach it
   back except `cmd`.
4. **`internal/platform` never imports a business module.**
5. **`httperr` and `httpx` import nothing from this module** — they sit below every layer
   that answers a request, and stay there only while they depend on none of them.
6. **Each module owns its own sqlc config.** There is no root `sqlc.yaml`, and a new one
   fails the build.
7. **A feature never reaches another feature** — not directly, and not through a helper two
   hops away. This is the vertical slice invariant: a use case you cannot read without
   opening its neighbours is a use case in a layer again, whatever the directory is called.

   When two features genuinely need the same thing, it moves *below* them both — a rule into
   the module's `domain`, a contract into its `ports`, a wire shape into its
   `transport/http`. Never sideways.
8. **A module's `transport/http` holds no endpoint.** The test is "no function returns an
   `http.Handler`", not anything about names, because that is what an endpoint *is* here and
   a name can be chosen to slip past. Middleware is exempt by construction: it returns
   `func(http.Handler) http.Handler`.

   This is what stops the old arrangement growing back one handler at a time — which is how
   it would grow back, because adding a handler next to the route table is always the
   smaller diff.

Slice 2 added one more, and it is the only one this file cannot enforce with a test:

9. **A query with no requester predicate is mounted under `/api/agent` and nowhere else.**
   The customer's reads carry `WHERE requester_id = $1`; the agent's carry nothing, because
   an agent reads tickets that are not theirs. What stands in for the predicate is the route
   group, which sits behind `RequireRole(agent, admin)` — see
   [ADR 0011](adr/0011-authorization-moves-from-the-predicate-to-the-route.md).

   It is checked by a test that walks **every path under the prefix** as a customer and
   requires 403 on each, driven from a list in the test file so a route added later is
   covered without the test being edited. That is weaker than the eight above: those are
   graph properties a tool derives, this is a list a person maintains. The compensation is
   that chi routes before it runs a group's middleware, so an unmounted path answers 404
   while a mounted one answers 403 — the same test therefore proves each route is protected
   *and* that it exists.

Rule 9 is readable because each module keeps **one route table**, in
`transport/http/routes.go`, listing the customer's routes and the agent's separately. Those
two lists are the only place the answer to "what is behind the role check" exists. The table
takes handlers that are **already built** — the module's front door constructs the features
and hands them over — which is what lets it stay free of any feature import. A route table
that constructed its handlers would import every feature, and every feature imports
`transport/http` for the wire shapes they share; that is a cycle, and it is why the
direction is one-way rather than a matter of taste.

## How they are enforced, and why it takes two things

Both halves run on every CI build. They catch different failures, which is the only reason
to have both:

| | `depguard` (in `make lint`) | `internal/architecture` (in `make arch`) |
|---|---|---|
| Reads | one file's import block | the whole import graph, transitively |
| Direct illegal import | **catches** | **catches** |
| Reached through another package | misses | **catches** |
| Inside a `//go:build integration` file | **catches** (lint runs with the tag) | misses |
| Survives `//nolint` | no | **yes** |

Measured, not assumed, in both directions:

- Routing a `ticket → sla` import through one intermediate package produces **zero**
  depguard findings and four from the architecture test. The direct import is not the shape
  these violations take in practice — nobody writes the obvious one. It is a helper that
  innocently reaches somewhere, two hops away, and only the graph walk sees it.
- Enabling depguard found **seven** violations the architecture test had been passing over:
  the ticket module's integration tests were importing the SLA module to build a real
  clock. The graph walk uses the default build context, so files behind
  `//go:build integration` are invisible to it; `make lint` runs golangci-lint with the tag
  and sees them.

Those seven were not suppressed. The two tests that genuinely needed both modules were
tests of the *wired system*, not of the ticket module, and moved to `internal/app` — where
importing two modules is the whole point. The third only needed a foreign key and now reads
it with SQL.

## Why two modules can hold the same word

`ticket.Priority` and `sla.Priority` carry the same four strings. `ticket.Role` and
`identity.Role` carry the same three. That duplication is deliberate, and it is the part of
this design most likely to look like a mistake.

They answer different questions:

| | |
|---|---|
| `ticket.Priority` | how urgent the requester says it is |
| `sla.Priority` | which row of the policy table applies |
| `identity.Role` | what someone **is** |
| `ticket.Role` | what someone **was when they acted**, on the audit trail |

That last pair is the clearest: promoting an agent must not rewrite history. If they were
one type, they could not disagree — and they have to.

Sharing a type would mean one module owning a vocabulary another module's schema
constrains. The boundary would exist in the folder layout and nowhere else. The cost is a
mapping in `internal/app`, and it is exhaustive rather than a string conversion: a role
added to identity and unaccounted for there fails to the least privileged role instead of
reaching a CHECK constraint at write time.

## The one rule that is about time, not structure

**No I/O belonging to another module happens inside our transaction.**

The ticket module's `create` and `transition` handlers resolve an SLA clock *before* the
repository opens its transaction, and hand the repository a value whose `Compute` is pure. The repository
cannot reach the SLA module even by accident, because it is never given anything that
could.

The reason is arithmetic: a read on a second connection, held for the transaction's
duration, against a `pgxpool` default of `max(4, NumCPU)`. Enough concurrent writes each
holding two connections wait on each other.

`Transition` reads `sla_policy_id` unlocked to resolve the clock, then — once the row is
locked — checks that it still agrees and refuses with `ErrPolicyChangedUnderUs` if not.
Nothing updates that column, so the check should be unreachable. It exists because a
deadline computed from the wrong budget looks entirely plausible.

## Adding a module

1. `internal/modules/<name>/` with the four layers and a `module.go`.
2. Its own `infrastructure/postgres/sqlc.yaml` with `omit_unused_structs: true`, pointing
   `schema:` at `db/migrations`. That setting is what makes ownership structural: a table
   your queries do not touch generates no struct, so there is nothing to reach across the
   boundary with.
3. Add the name to `modules` in `internal/architecture/architecture_test.go` and a
   `<name>-module` block in `.golangci.yml`. **Both.**
4. Declare what you need from other modules as contracts in your own layers. Wire them in
   `internal/app`.
5. `make sqlc` finds the new config on its own — it discovers rather than lists, so a
   module left off a hand-written list cannot silently stop being regenerated.

## When the linter stops you

It will name the rule and say what to do instead. Before reaching for `//nolint`, note that
it will not help: the architecture test walks the graph regardless of what golangci-lint is
told to skip.

Almost always the answer is one of:

- **Declare a contract.** You need something another module has: say what you need, in your
  own vocabulary, in your own `ports` or in the feature that needs it. `internal/app` supplies
  it.
- **Move the code.** It is in the wrong layer, or `platform` has grown something that knows
  what a ticket is.
- **Change the rule.** Sometimes the right answer. Change it in *both* places, write down
  why, and make sure you have seen the new one fail.

## Related decisions

| | |
|---|---|
| [ADR 0001](adr/0001-single-calculation-path-for-the-sla-clock.md) | one calculation path for the SLA clock |
| [ADR 0002](adr/0002-validation-ownership-between-sla-and-ticket.md) | validation ownership (partly superseded by 0005) |
| [ADR 0004](adr/0004-one-way-to-report-an-http-failure.md) | one way to report an HTTP failure |
| [ADR 0005](adr/0005-modules-do-not-share-a-vocabulary.md) | modules do not share a vocabulary |
| [ADR 0006](adr/0006-a-module-owns-its-generated-queries.md) | a module owns its generated queries |
| [ADR 0007](adr/0007-resolving-a-policy-is-io-computing-with-one-is-not.md) | resolving is I/O, computing is not |
| [ADR 0008](adr/0008-no-io-inside-another-modules-transaction.md) | no I/O inside another module's transaction |
| [ADR 0009](adr/0009-the-composition-root.md) | the composition root |
