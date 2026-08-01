# ADR 0001: A single calculation path for the SLA clock

- **Status:** Accepted
- **Date:** 2026-08-01
- **Context:** Slice 1, task T5 (`internal/sla`)
- **Related:** `docs/spec.md` §4.2, `tasks/todo.md` T5 and T11

## Context

`docs/spec.md` §4.2 establishes that `ticket_status_history` is the **fact** and the
`tickets.sla_*` columns are a **derived cache**. The cache exists for one reason: the
breach checker needs a single indexed predicate (`WHERE sla_due_at < now()`), and
aggregating full history for every open ticket on every run neither scales nor indexes.

Keeping a derived cache creates an obligation to prove the cache agrees with the fact.
`tasks/todo.md` T11 was written to discharge that obligation with a consistency test:
*reconstruct the clock from history and assert it equals the cached value*.

While defining the seams for T5, that test was found to be **tautological** under the
obvious implementation. If the write path produces the cache by calling `Reconstruct` over
the history, then a test that compares `Reconstruct(history)` against the cache is
comparing `Reconstruct` to itself. It passes by construction and can never disagree with
the code — precisely the anti-pattern the project's TDD guidance names.

The question this ADR answers: **how many implementations of the clock arithmetic exist?**

## Decision

**One.** `Reconstruct(policy, history, now) (ClockState, error)` is the only function that
computes clock state. There is no second, incremental implementation.

Consequences that follow, and are binding:

### 1. `Advance` is not a public surface

Any incremental stepping over transitions is a **private helper inside `Reconstruct`'s
loop**. It is not exported. Two exported functions that both compute clock state would be
alternative B under a different name.

### 2. The cache is pure memoization

`tickets.sla_*` holds nothing that cannot be recomputed from `ticket_status_history` plus
the ticket's `sla_policy_id`. It is an index-friendly projection, never a source of truth.

### 3. The handler write order is fixed

Within **one transaction**, in this order:

```
1. INSERT the ticket_status_history row
2. SELECT the ticket's full history          ← after the insert, never before
3. Reconstruct(policy, history, now)
4. UPDATE the tickets.sla_* cache columns
```

Reading history **before** the insert leaves the cache exactly one event behind. It is a
silent, plausible-looking corruption, and it is one of the specific bugs T11 exists to
catch.

### 4. T11 is reframed

T11 no longer asserts that the arithmetic is correct — the unit tests at the `internal/sla`
seams cover that, with expected values drawn from worked examples rather than recomputation.

T11 asserts, against a real database:

> The cached values **stored in `tickets`** equal `Reconstruct` over the history
> **stored in `ticket_status_history`**.

This is not tautological, because it targets a different failure class: a handler that
skipped the cache update, wrote fact and cache in separate transactions, read history
before inserting, or committed a partial write. Those bugs live in the write path, not in
the arithmetic, and no amount of unit testing on `internal/sla` would find them.

## Consequences

**Positive**

- One implementation of the arithmetic means one place for an arithmetic bug to live.
- Cache/fact divergence becomes structurally impossible except through a write-path defect,
  which T11 detects.
- Recomputing from full history is cheap: a ticket accumulates a handful of history rows,
  not thousands.

**Negative**

- Every transition reads the ticket's full history rather than stepping incrementally from
  the previous state. Accepted: the row count per ticket is small and bounded in practice.
- If a ticket ever accumulated pathological history (thousands of transitions), this would
  need revisiting. No such path exists in the current design.

**Neutral**

- T11 remains mandatory. Its justification changed; its necessity did not.

## Alternatives considered

### B — two paths: incremental `Advance` for writes, `Reconstruct` for audit

Rejected. It makes the consistency test meaningful by **creating the divergence the test
looks for**. Two independent implementations of the same rule will eventually disagree;
building them deliberately in order to have something worth testing inverts the purpose of
the test. The correct move is to make divergence impossible, not detectable.

Reconsider only if profiling shows full-history reconstruction is a real bottleneck — and
then the incremental path should be introduced *with* the audit comparison, not instead of
it.
