# ADR 0002: Validation ownership between `internal/sla` and `internal/ticket`

- **Status:** Accepted
- **Date:** 2026-08-01
- **Context:** Slice 1, task T5 (`internal/sla`)
- **Related:** [ADR 0001](./0001-single-calculation-path-for-the-sla-clock.md), `docs/spec.md` §4.1, §4.2

## Context

`Reconstruct` consumes a `[]StatusChange` read from `ticket_status_history`. A malformed
history would produce a `ClockState` that is silently wrong — worse than a loud failure,
because a wrong SLA deadline looks entirely plausible.

The question: **which malformed inputs does `internal/sla` reject, and which are somebody
else's responsibility?**

Two domain packages are in play, and the dependency runs one way only:

```
internal/sla  ──imports──▶  internal/ticket      (Status, Priority)
internal/ticket  ──✗──▶  internal/sla            (never; enforced by test)
```

## Decision

**`Reconstruct` returns `(ClockState, error)`.** Not a panic. A panic in a pure domain
function is unacceptable: it converts a data problem into a process crash and denies the
caller any chance to handle it.

`internal/sla` validates **its own preconditions** — the properties its arithmetic depends
on — and nothing else:

| Rejected input | Error | Why it belongs to `sla` |
|---|---|---|
| Empty history | `ErrEmptyHistory` | There is no starting instant; nothing can be computed |
| Entries not ascending by `At` | `ErrUnorderedHistory` | Interval arithmetic is meaningless out of order |
| First entry is not `open` | `ErrHistoryMustStartOpen` | Every ticket is created into `open`; a history that does not is not a ticket's history |

`internal/sla` does **not** validate transition legality — that `closed → open`, for
example, is not a permitted move.

## Rationale for the exclusion

Transition legality **is** the state machine (`docs/spec.md` §4.1), and the state machine
lives in `internal/ticket`. Encoding it a second time in `internal/sla` would mean:

- The same rule written in two places. The day a state is added, forgetting one of them
  makes `sla` reject histories that are perfectly valid.
- `sla` coupled to the transition table, which makes the later `BusinessHours` work harder
  for no benefit.

An illegal transition is blocked at **write time**, where it belongs. It can never reach
`Reconstruct`, because it could never enter the database: spec §4.1 requires every accepted
transition to write its history row in the same transaction as the ticket update, and
rejects anything else with `409 Conflict`.

**Defence in depth, when it exists.** The state machine is Slice 3. If a redundant check is
wanted then, `sla` will **delegate** to `ticket.IsValidTransition(from, to)` — one call, no
duplicated table — rather than re-implementing the rule. `sla` already imports `ticket`, so
this costs nothing structurally.

## Consequences

**Positive**

- Each rule has exactly one home. The state machine is authoritative about transitions;
  `sla` is authoritative about clock arithmetic.
- `Reconstruct` fails loudly and recoverably on input it genuinely cannot process.
- The three validated preconditions are cheap: one pass over a small slice.

**Negative**

- A hypothetical illegal transition written directly to the database by a future code path
  that bypasses the handler would be computed rather than rejected. Accepted: such a path
  would violate spec §4.1 and is a defect in its own right. T11's consistency test is the
  net that catches write-path defects.

**Enforced by test**

- `internal/ticket` must never import `internal/sla`. Added to the architecture boundary
  test in T11, alongside the existing rule that neither package may import `internal/store`,
  `internal/api`, or `database/sql`.
