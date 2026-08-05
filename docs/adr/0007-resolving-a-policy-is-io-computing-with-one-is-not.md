# ADR 0007: Resolving a policy is I/O; computing with one is not

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Refactor to a modular monolith, PR 4 of 6
- **Related:** [ADR 0001](./0001-single-calculation-path-for-the-sla-clock.md),
  [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md),
  [ADR 0006](./0006-a-module-owns-its-generated-queries.md)

## Context

`internal/sla` was a pure domain package: `Reconstruct`, `Policy`, `Schedule`, and no I/O
at all. Whoever wanted a policy read it themselves — `internal/store` held `policyFrom`,
`policyOf`, `ErrUnsupportedSchedule` and the `sla_policies` queries.

That is a module split down the middle. The arithmetic lived in one package and the data
it operates on lived in another, and the piece that interprets a stored row — turning
`schedule_mode = '24x7'` into an `Always24x7` — sat on the wrong side of the line
entirely. `internal/store` had to know that a `Schedule` is a thing and which strings name
which one.

## Decision

**The SLA module owns `sla_policies`, its queries, and the interpretation of a stored row.**

```
internal/modules/sla/
├── domain/                     Priority, Policy, Phase, Schedule, Reconstruct
├── application/                PolicyRepository (the port), Calculator
├── infrastructure/postgres/
│   ├── sqlc.yaml               sla_policies, and nothing else
│   ├── queries/sla_policies.sql
│   ├── sladb/                  generated
│   └── repository.go           policyFrom, and the schedule interpretation
└── module.go
```

`omit_unused_structs` again does the structural work: `tickets` carries a foreign key into
`sla_policies`, and the generated package holds **no `Ticket`**.

### The application layer is thin on purpose

`Calculator` resolves policies and does not wrap the arithmetic. That is not an oversight —
it is the property the module is designed around:

> **Resolving a policy costs I/O. Computing a clock from one does not.**

A caller resolves first and computes second. If the arithmetic were behind a service
method, every computation would look like it might do a database read, and the caller
would have no way to know when it was safe to run one inside a transaction it already
owned.

### `New(db sladb.DBTX)`, not `New(pool)`

The module is built on a handle, not a pool, and `internal/store` builds it on **its own
transaction**:

```go
policy, err := sla.New(tx).Calculator.ForPriority(ctx, priority)
```

The policy read therefore runs on the connection the transaction already holds. Building
it on the pool instead would take a **second** connection for the transaction's duration,
and `pgxpool` defaults to `max(4, NumCPU)` — enough concurrent creates each holding two
would wait on each other.

The better shape is to resolve *before* opening the transaction, and that is what the
ticket module will do in PR 5 once it owns this write. Today `internal/store` is both the
composition point and the transaction owner, and the handle keeps the current structure
correct without pretending otherwise.

## Consequences

**`ErrUnsupportedSchedule` moved and got a better name.** It is now
`postgres.ErrUnsupportedScheduleMode`, next to the code that produces it. It is not a
validation failure — the CHECK constraint already restricts the column — it is the case
where the database has been migrated ahead of the binary. Failing loudly beats computing a
deadline with the wrong schedule, which would look entirely plausible.

**`internal/store` translates the module's sentinel into its own.**
`internal/api` matches on `store.ErrNoPolicyForPriority` to answer 422 rather than 500.
Letting the SLA module's error through would make `internal/api` import that module to name
an error, which is exactly the coupling the contract exists to avoid. The original is
wrapped with `errors.Join`, so the cause still reaches the logs. This translation is the
adapter that moves to `internal/app` in PR 5.

**The policy tests moved and two of them got stronger.** They now exercise the repository
rather than the generated queries, so what is under test includes the budget arriving as a
`time.Duration` and the schedule being interpreted at all — neither of which a generated
query does. A new one asserts that an unserved priority is reported as
`application.ErrNoPolicyForPriority` and *not* as `pgx.ErrNoRows`, which is the branch
ticket creation depends on.

**`internal/ticket/architecture_test.go` shrank.** `internal/sla` is no longer in its list
of domain packages, because `TestModuleDomainsArePure` in `internal/architecture` now
covers every module's domain rather than a list somebody has to remember to extend.

## Enforcement

`TestDomainsDoNotReachEachOther` gains `sla -> ticket` and `sla -> identity`. Both were
watched failing before they were trusted: reintroducing the import into
`internal/modules/sla/domain` turns them red, directly and through an intermediate package.
