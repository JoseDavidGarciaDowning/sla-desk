# ADR 0006: A module resolves what it needs before opening a transaction, never inside one

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Found while splitting `store.TicketRepo` into the ticket module
- **Related:** `docs/adr/0001` (single calculation path), `docs/adr/0005` (module boundaries)

## Context

Creating a ticket needs an SLA deadline. Before the split, one method did everything in one
transaction:

```go
tx := pool.Begin(ctx)
now       := q.TransactionTime(ctx)              // the DB's clock
policyRow := q.GetActiveSLAPolicyByPriority(...)  // sla_policies
state     := sla.Reconstruct(policy, ...)         // arithmetic
q.CreateTicket(...)                               // tickets
q.InsertTicketStatusHistory(...)                  // ticket_status_history
tx.Commit(ctx)
```

Under ADR 0005 the policy read belongs to the SLA module, reached through a contract. The
obvious translation keeps the shape and calls the contract from inside the transaction.

**That deadlocks.** The SLA module has its own handle on the pool, so the call takes a
*second* connection while the ticket transaction is holding the first. `pgxpool` defaults
`MaxConns` to `max(4, NumCPU)`. On a one-CPU Cloud Run instance that is 4:

| Concurrent creates | Connections held | Pool | Result |
|---|---|---|---|
| 3 | 3 + 3 wanted | 4 | slow |
| 4 | 4 held, 4 wanted, 0 free | 4 | **deadlock until the context deadline** |

Every transaction waits for a connection that only another transaction can release. It
does not surface in a unit test, it does not surface at low traffic, and it surfaces under
load as request timeouts with no error in the logs.

## Decision

**No module performs I/O inside another module's transaction.** The contract is split so
resolution and computation are separate:

```go
type SLAPolicies interface {                       // I/O — called before the transaction
    ForPriority(ctx context.Context, p domain.Priority) (SLAClock, error)
    ForPolicy(ctx context.Context, id int64) (SLAClock, error)
}

type SLAClock interface {                          // pure — safe inside one
    PolicyID() int64
    Compute(timeline []domain.Phase) (ClockState, error)
}
```

The use case owns the ordering; the repository owns the transaction:

```go
func (s *Service) Create(ctx context.Context, in NewTicket) (domain.Ticket, error) {
    clock, err := s.sla.ForPriority(ctx, in.Priority)   // one connection, released
    if err != nil { ... }
    return s.repo.Create(ctx, in, clock)                // one connection, its own
}
```

`Transition` needs the policy the ticket was created under, which means knowing the ticket
first. It reads **only the policy id**, unlocked, before beginning:

```sql
-- name: GetTicketSLAPolicyID :one
SELECT sla_policy_id FROM tickets WHERE id = $1;
```

That read is safe because `sla_policy_id` is written once by `CreateTicket` and no query
updates it. The locked read inside the transaction then *asserts* it rather than trusting
it, and refuses with `ErrPolicyChangedUnderfoot` if it ever differs — unreachable today,
which is the point of writing it down.

## Consequences

- One extra indexed primary-key lookup per transition. Measured at well under a
  millisecond, against a deadlock that has no upper bound.
- The transaction is shorter: the ticket row is locked for two inserts and an update, not
  for another module's query as well.
- `Compute` is pure, so the deadline arithmetic is exercised in unit tests with nothing
  running — the property `docs/adr/0001` depends on.
- This is what a service split would force anyway. If the SLA module ever becomes a
  separate service, `ForPriority` becomes an RPC and nothing else moves.
- **One test had to change to keep meaning what it said.**
  `TestAFailedCacheUpdateLeavesNoHistoryRow` used to provoke its failure with an
  uninterpretable `schedule_mode`, which failed at step 3 of the ordering in ADR 0001 —
  after the history row was inserted. With resolution moved ahead of the transaction, that
  case now fails *before anything is written*. The test would still have passed while
  proving the early return rather than the rollback. It now provokes the failure with a
  trigger that refuses every `UPDATE tickets`, which fires at step 4 where the old case
  used to, and a second test covers the early refusal explicitly.

## Alternatives rejected

**Share the transaction across modules.** Pass `pgx.Tx` through the contract so the SLA
read joins the ticket transaction. It removes the deadlock and replaces the boundary with
a shared driver type: the ticket module's contract would name `pgx.Tx`, every
implementation would be a Postgres implementation, and the interface would decouple
nothing.

**Raise `MaxConns`.** Moves the number at which it deadlocks; does not remove it.

**A unit-of-work abstraction owned by `internal/app`.** Real, and much more machinery than
this needs. Worth revisiting only if a write genuinely has to span two modules' tables
atomically — which none does today, because each module owns its tables outright.
