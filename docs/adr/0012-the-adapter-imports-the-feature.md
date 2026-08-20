# ADR 0012: The postgres adapter imports the features

- **Status:** Accepted
- **Date:** 2026-08-20
- **Context:** The vertical slice refactor — `tasks/refactor-vsa/plan.md`
- **Related:** [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md),
  [ADR 0008](./0008-no-io-inside-another-modules-transaction.md),
  `docs/architecture.md`, `docs/spec.md` §8

## Context

Open `internal/modules/ticket/infrastructure/postgres/create.go` and the first thing in the
import block is a feature:

```go
import (
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/create"
)

func (r *Repository) Create(ctx context.Context, in create.Command, clock ports.SLAClock) (domain.Ticket, error)
```

Infrastructure importing an application package looks like the dependency rule pointing the
wrong way, and it is the single most likely thing in this refactor for somebody to "fix"
six months from now. This ADR exists so that the fix does not compile and the reason is
one file away.

## Why it cannot be otherwise

Each feature declares the persistence it needs, in its own words, holding its own command
type (spec §8, consumer-declared interfaces):

```go
package create

type Command struct { /* ... */ }

type Tickets interface {
	Create(ctx context.Context, in Command, clock ports.SLAClock) (domain.Ticket, error)
}
```

The obvious way to satisfy that from a shared adapter is to declare an identical struct in
the adapter and let structural typing do the rest. **It does not work, and the compiler is
unambiguous about why:**

```
cannot use &postgres.Repository{} as create.Tickets value:
	*postgres.Repository does not implement create.Tickets (wrong type for method Create)
		have Create(postgres.NewTicket) error
		want Create(create.NewTicket) error
```

Go's structural typing applies to **method sets**, not to **parameter types**. A defined
type is identical only to itself (`spec: Type identity`). Two structs with the same fields
and different names are different types, and an interface naming one is satisfied only by a
method naming that same one.

So an interface that mentions a feature-owned type can only be implemented by code that
imports that feature. There is no third option.

## Decision

**The adapter imports the feature whose port it implements, and that is the dependency rule
being obeyed rather than broken.**

Hexagonal architecture says dependencies point *inward*: infrastructure depends on the
application, never the reverse. `postgres` importing `features/create` is exactly that
arrow. What makes it look wrong is only that "infrastructure imports application" is
usually invisible — the port is normally declared in one shared package, so the import
lands on something with a neutral name.

The file split keeps it honest. `postgres/create.go` imports `features/create` and nothing
else from the features tree; `postgres/assign.go` imports `features/assign`. One file, one
feature, one arrow. `assertions.go` lists all nine in one place:

```go
var (
	_ create.Tickets  = (*Repository)(nil)
	_ list.Tickets    = (*Repository)(nil)
	/* ... */
)
```

Nothing else would force the type to keep satisfying a port whose only other mention is in
a package that imports it.

## The rule does not apply everywhere, and knowing where is the useful part

The identity module's middleware declares its own port and is satisfied **without** any
import:

```go
// internal/modules/identity/transport/http/middleware.go
type UserSource interface {
	EnsureUser(ctx context.Context, subject string) (domain.User, error)
}
```

`*provision.Handler` satisfies that, and `transport` never names `provision`. The
difference is not the layer — it is the **types in the signature**. This one mentions
`string` and `domain.User`, both of which live below both packages. The ticket ports mention
`create.Command`, which does not.

> **The test:** a port whose signature names only types from the domain, the standard
> library, or a shared package can be declared anywhere and satisfied from anywhere. A port
> that names a feature-owned type can only be implemented by an importer of that feature.

That is worth knowing before choosing where a command type lives, because it is the whole
cost of putting one inside its feature.

## Consequences

**A cycle becomes possible and is refused by the compiler.** If a feature imported
`infrastructure/postgres` the pair would not build. Nothing does — features name ports, and
the composition root supplies the adapter — but the guard is compilation rather than
vigilance.

**`internal/app` never sees a command type.** The composition root builds
`postgres.NewRepository(pool)` and hands it to `ticket.NewWith`, which distributes it to the
handlers. Only the module's own front door and its own adapter name the feature packages.

**One shared adapter, not one per feature.** The alternative — a `create` adapter, an
`assign` adapter — would remove the multi-feature import block at the cost of nine
constructors in the composition root and nine copies of the transaction handling ADR 0008
depends on. The import block is the cheaper thing to read.

**What would falsify this.** If a feature's command type stops being feature-specific —
if `create.Command` and `transition.Command` converge into one shape used by both — then it
was never feature-owned and belongs in `ports`, and this ADR stops applying to it. That is a
judgement about the domain, not about the language rule, and the language rule will keep
being true either way.
