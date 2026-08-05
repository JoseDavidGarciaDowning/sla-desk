# ADR 0008: No I/O belonging to another module inside our transaction

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Refactor to a modular monolith, PR 5 of 7
- **Related:** [ADR 0001](./0001-single-calculation-path-for-the-sla-clock.md),
  [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md),
  [ADR 0007](./0007-resolving-a-policy-is-io-computing-with-one-is-not.md)

## Context

ADR 0007 left a deferred decision. `internal/store` owned the ticket write paths *and*
was the composition point, and it read the SLA policy **inside** its transaction, on the
transaction's own handle:

```go
policy, err := sla.New(tx).Calculator.ForPriority(ctx, priority)
```

That kept it to one pooled connection, which is correct. But it was correct *by
arrangement*, not by construction: it only worked because the SLA module happened to accept
a `DBTX`, and nothing in either module's signature said so. Passing the pool instead would
have been a one-word change with no compile error and a deadlock under load — `pgxpool`
defaults to `max(4, NumCPU)`, and enough concurrent writes each holding two connections
wait on each other.

## Decision

**The SLA clock is resolved before the transaction opens, and the repository is handed a
value that cannot do I/O.**

```go
type SLAClock interface {
	PolicyID() int64
	Compute(timeline []domain.Phase) (ClockState, error)   // pure
}
```

`application.Service` resolves; `postgres.Repository` writes. The repository's signature is
`Create(ctx, in, clock)` — there is no way for it to reach the SLA module even by accident,
because it is never given anything that could.

That is the difference from ADR 0007's arrangement: the property used to depend on a
convention nobody could see, and now it is in the type.

### Transition reads the policy id first, unlocked

```go
policyID, _ := repo.PolicyIDOf(ctx, id)   // no transaction, no lock
clock, _    := sla.ForPolicy(ctx, policyID)
repo.Transition(ctx, in, clock)           // opens the transaction
```

The unlocked read is safe because `sla_policy_id` is written once by `Create` and no query
updates it. **The repository does not take that on trust**: once the row is locked it
compares the two and refuses with `ErrPolicyChangedUnderUs` if they disagree.

That check should be unreachable, which is exactly why it exists. A deadline computed from
the wrong budget is the kind of wrong that looks entirely right.

## Consequences

**`internal/store` is gone.** Its 856 lines of production code became the ticket module's
`domain`, `application` and `infrastructure/postgres`. There is no root `sqlc.yaml` any
more either, and `TestEachModuleOwnsItsOwnSQLCConfig` asserts that a new one cannot appear.

**The domain has its own `Ticket`.** Handlers used to receive the generated row type, which
put the shape of the `tickets` table into every layer and made adding a column a change to
three of them. The mapping now happens once, in the repository.

**`SLAConsumed` is a `time.Duration`, not an `int64` of microseconds.** The column is still
microseconds — see docs/spec.md §4.2 for why minutes were not enough — and the repository is
what converts. This immediately found a bug in a moved test that multiplied the value by
`time.Microsecond` a second time; it had been correct against an `int64` and was silently
wrong against a `Duration` until the type stopped allowing it.

**Ids are `uuid.UUID` everywhere.** The last `pgtype.UUID` left the API surface, which
removed `uuidString` and the conversion in `internal/api/identity.go` along with it. That
conversion existed only because the generated code had chosen the driver's type.

**Two architecture tests were absorbed rather than deleted.**
`TestDomainPackagesDependOnNothingImpure` and `TestTicketDoesNotImportSLA` lived in
`internal/ticket` and are now covered by `TestModuleDomainsArePure` and
`TestDomainsDoNotReachEachOther` — over every module rather than over a list somebody has
to extend. The old one also forbade `internal/api` and `internal/config`, which the new one
did not until this was checked; the test count would have looked fine either way, which is
how a rule quietly weakens.

## What is still not a module

`internal/api` holds the ticket handlers, the router, CORS, health, and both cross-module
adapters (`sla.go`, `identity.go`). It is the composition point, and PR 6 splits it: the
handlers into the ticket module's `transport/http`, everything else into `internal/app`.

The adapters move unchanged in substance, which is the test of whether the translation was
real work or ceremony.
