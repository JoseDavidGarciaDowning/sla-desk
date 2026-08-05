# ADR 0009: The composition root

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Refactor to a modular monolith, PR 6 of 7
- **Related:** [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md),
  [ADR 0008](./0008-no-io-inside-another-modules-transaction.md)

## Context

Three modules now own their own data, vocabulary and use cases, and none of them imports
another. What was left was the package holding them together: `internal/api` had the ticket
handlers, the router, CORS, health, and both cross-module adapters.

That is two jobs in one package. The handlers are the ticket module's HTTP surface — they
belong to it. The wiring is not any module's, and belongs above all of them.

## Decision

**`internal/api` is split. The handlers go into the module; everything else becomes
`internal/app`.**

```
internal/modules/ticket/transport/http/    handlers, DTOs, the frontend contract
internal/app/                              router, CORS, health, and the adapters
```

`internal/app` is the only package that may import more than one business module, and
`TestNothingReachesTheCompositionRoot` refuses the reverse: nothing may import it back
except `cmd`. A module reaching into the wiring would be depending on its own neighbours
through the back door, which is the arrangement this refactor removed.

### The ticket module declares who a caller is

```go
type Caller struct {
	ID   uuid.UUID
	Role domain.Role
}

type CallerResolver func(ctx context.Context) (Caller, bool)
```

Not the identity module's `User`. The ticket module says what it needs — an id to scope
reads by, and a role to check transitions against — and `internal/app` supplies a function
that produces one. Neither module learns anything about the other: identity never learns
what a caller is used for, and ticket never learns where one comes from.

A function rather than an interface, because it has one method and a test can supply one
inline. That turned out to matter more than expected: the module's transport tests used to
import the identity package to build an authenticated request, which was a boundary
violation sitting inside the module. They now build a `Caller` directly and pass a
one-line resolver.

### The module mounts its own routes

```go
tickethttp.Routes(r, deps.Tickets.Service, callerFromContext)
```

`Routes` is handed a `chi.Router` that already carries the authentication middleware, so an
endpoint cannot be added outside the authenticated group by forgetting to. The composition
root decides what a route sits behind; the module decides what its routes are.

## Consequences

**The adapters moved unchanged in substance.** That was the test set for them when they were
written in PR 3 and PR 5 — a translation that had to be rewritten when the composition point
moved would have been ceremony. `actorRole` and `translateSLAError` are byte-identical;
`callerFromContext` changed its return type from a local struct to the contract the ticket
module declared, which is the same values under a name the module owns.

**`internal/httpx` was extracted.** `writeJSON` was needed by both the module's transport
and the health endpoint. It joins `internal/httperr` under the same admission rule — does
this decide how bytes get onto the wire, without knowing what the bytes mean? — and under
the same architecture test, which refuses either of them an import from this module.

**`Deps.Tickets` is required, like `Deps.Identity`.** The same guard, for the same reason,
found by the same failure: tests that built `Deps{}` and got a nil pointer at request time.
`TestRouterRefusesAnUnusableWebhookSecret` now asserts it failed on *neither* module being
missing, because either would have satisfied it while proving nothing.

**A relative path became a resolved one.** The contract test read
`../../web/lib/contract.ts`, which broke silently when the package moved three directories
deeper — and the failure read like a missing file rather than like a moved test. It now
walks up to `go.mod`, the way the architecture test already did.

## What is left

`internal/config`, `internal/httperr` and `internal/httpx` still sit at the top of
`internal/`. PR 7 moves them under `internal/platform`, adds the named CI step for the
architecture rules, and writes `docs/architecture.md`.
