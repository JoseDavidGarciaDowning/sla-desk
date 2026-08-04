# ADR 0005: Modules are bounded by contracts, and the boundary costs a duplicated vocabulary

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** After slice 1. The backend had grown to five technical packages and the seams were in the wrong places
- **Related:** `docs/adr/0002` (validation ownership), `docs/adr/0004` (one way to report a failure), `docs/adr/0006` (no I/O inside another module's transaction), `docs/spec.md` §7, §8

## Context

The backend was organised by technical layer: `internal/api`, `internal/store`,
`internal/auth`, plus two domain packages. That arrangement answers "where do HTTP handlers
live" and answers nothing about ownership. Measured on the code as it stood:

| Package | Lines | Tables it wrote | Concepts it knew |
|---|---:|---|---|
| `internal/store` | 291 + 4 generated | `tickets`, `ticket_status_history`, `users`, `sla_policies` | all four |
| `internal/api` | 740 | — | tickets, users, SLA deadlines |
| `internal/auth` | 320 | `users` | users, ticket roles |

One package wrote every table in the schema. `store.TicketRepo.Create` resolved an SLA
policy, computed a deadline, inserted a ticket and inserted a history row — four concerns,
three of the four tables, one method. Adding an agent dashboard would have meant editing
`internal/api`, `internal/store` and `internal/auth` for a change that is entirely about
tickets.

The coupling was real rather than notional:

```
ticket (leaf) ◀── sla ◀── store ◀── auth ◀── api
```

`internal/sla` imported `internal/ticket` for one method call — `Status.RunsClock()` — and
that single import made the deadline arithmetic unable to move without the state machine.

## Decision

**Three modules, each owning its tables, and none importing another.**

```
internal/modules/<module>/
    domain/           entities, value objects, rules, errors — imports nothing
    application/      use cases, and the contracts this module needs from outside
    infrastructure/   repositories, generated queries, external SDKs
    transport/        HTTP handlers, DTOs, routes
    module.go         the front door
```

| Module | Owns | Has HTTP |
|---|---|---|
| `ticket` | `tickets`, `ticket_status_history` | yes |
| `sla` | `sla_policies` | no |
| `identity` | `users` | yes — Clerk webhook and authentication |

A module states what it needs as an interface **in its own vocabulary**, and
`internal/app` — the composition root, and the only package permitted to import more than
one module — supplies something that satisfies it.

```go
// ticket/application: what the ticket module needs, said in ticket words.
type SLAPolicies interface {
    ForPriority(ctx context.Context, p domain.Priority) (SLAClock, error)
    ForPolicy(ctx context.Context, id int64) (SLAClock, error)
}
```

### The part that costs something

Interfaces decouple **behaviour**. They do not decouple **types**. `ticket.Priority` and
`sla.Priority` hold the same four strings; `ticket.ActorRole` and `identity.Role` hold the
same three. Both pairs are now declared twice and mapped in `internal/app/adapters.go`.

This is the price, and it is worth naming rather than hiding. Two things make it the right
trade here:

1. **They are not the same concept.** A ticket's priority is how urgent the requester says
   it is; an SLA priority is the key a budget is filed under. `identity.Role` is what
   someone *is* and changes on promotion; `ticket.ActorRole` is what someone *was when they
   acted*, denormalised onto `ticket_status_history` precisely so promoting an agent does
   **not** rewrite the audit trail. One shared type would make the second property
   impossible to state.

2. **The alternative is a shared kernel**, and a package holding "the types everyone needs"
   has no admission rule. It grows until it is the application.

The mapping is 30 lines, exhaustive, and tested — including that an unmapped role falls to
the least privileged rather than silently becoming an admin.

### What disappeared rather than moved

`internal/sla` no longer knows what a ticket is. `Reconstruct` used to take
`[]StatusChange` and call `Status.RunsClock()`; it now takes `[]Phase{At, Running}`.

> **The ticket module decides which statuses burn budget. The SLA module decides how much
> time that is.**

Adding a fifth status is now a change in one module and no change at all in the other. The
import was not redirected through an interface — it stopped existing.

### Shared code

`internal/platform` holds logging, configuration, HTTP mechanics, the problem-document
writer and the test database handle. It is named for what it provides, not for the fact
that several things use it — the same rule ADR 0004 states for `httperr`. It may not
import a business module, and an architecture test enforces that.

## Enforcement

Two mechanisms, and neither subsumes the other:

| | Catches | Misses | Runs in |
|---|---|---|---|
| **depguard** (`.golangci.yml`) | a direct illegal import, per file, in seconds | anything reached through an intermediate package | `make lint`, CI |
| **`internal/architecture`** | transitive reaches, layer direction, platform purity | nothing, but takes a second | `make arch`, `make test`, CI |

Verified by injecting each violation and confirming the failure — including the two-hop
case (`ticket → platform/httpx → sla`), which depguard cannot see and the architecture test
reports by name. The architecture test also survives `//nolint:depguard`.

Both are wired into `make check` and into separate CI steps, so an illegal dependency
fails the build rather than being noticed in review.

## Consequences

- Adding a module means creating four directories and adding one string to `modules` in
  `internal/architecture/architecture_test.go`. Every rule is expressed over that list.
- A cross-module feature costs an interface and an adapter. That is the intended friction:
  it is the moment to ask whether the boundary is in the right place.
- Two vocabularies are duplicated and must be kept mapped. `TestEveryIdentityRoleMapsToATicketRole`
  fails if a role is added without deciding what it means on a ticket.
- Tests that span two modules live in `internal/app`, because that is the only place the
  two exist together. The SLA consistency test (`docs/spec.md` §9) and the whole ticket
  lifecycle suite moved there for this reason.
- Splitting a module into a service later means replacing one adapter in `internal/app`.
  Nothing inside a module changes.

## Alternatives rejected

**A shared kernel package for common types.** Removes the duplication and removes the
boundary with it: `ticket` and `sla` would both depend on a package neither owns, and the
first business type someone adds to it is unopposable.

**Keeping the layered structure and adding lint rules.** Rules over `internal/api` and
`internal/store` can only say "api may not import store", which is not the problem. The
problem was that one package wrote four tables, and no import rule expresses that.

**One `sqlc.yaml` with three outputs.** Simpler to run, and it puts every module's
generation in a file every module has to agree about. See ADR 0007.
