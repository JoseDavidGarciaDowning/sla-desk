# ADR 0005: Modules do not share a vocabulary

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Refactor to a modular monolith, PR 2 of 6
- **Supersedes in part:** [ADR 0002](./0002-validation-ownership-between-sla-and-ticket.md) — the
  dependency direction and `ErrHistoryMustStartOpen`. The validation *ownership* it decided
  still stands and is restated below.

## Context

The backend is being reorganised around business domains. The blocker was not where the
files sat — it was that `internal/ticket` had quietly become a **shared kernel**:

```
internal/auth   ──imports──▶  internal/ticket    (Role)
internal/sla    ──imports──▶  internal/ticket    (Status, Priority)
internal/store  ──imports──▶  internal/ticket, internal/sla
```

Neither `auth` nor `sla` can move into a module of its own while pointing at tickets for
vocabulary. So the arrows have to go before any file does.

Measured before deciding, the coupling was smaller than it looked. **Three lines of
production code**, against 45 uses in tests:

| | |
|---|---|
| `internal/sla/clock.go:31` | `Priority ticket.Priority` |
| `internal/sla/clock.go:38` | `StatusChange.To ticket.Status` |
| `internal/sla/clock.go:101` | `if first != ticket.StatusOpen` |

## Decision

**A module declares the vocabulary it needs, in its own terms, even when the strings
match.** `internal/sla` gets its own `Priority`, and stops naming ticket statuses at all.

### `StatusChange` becomes `Phase`

```go
type Phase struct {
	At      time.Time
	Running bool
}
```

`internal/sla` never needed to know that tickets exist, that they have statuses, or which
of those statuses the clock runs in. It needed a boolean. The division of labour is now
exact:

> **The ticket package decides which statuses burn budget. The SLA package decides how
> much time that is.**

A fifth status is a change in `internal/ticket` and no change at all here. `Status.RunsClock()`
already existed and already carried that decision — this only stops the SLA package from
looking past it.

### `ErrHistoryMustStartOpen` becomes `ErrTimelineMustStartRunning`

This is the substantive change, and it is not a rename.

ADR 0002 justified the old rule as *"every ticket is created into open; a history that does
not is not a ticket's history"* — a statement about **tickets**, enforced in the SLA
package. Restating it in this package's own terms turns out to split it in two:

| Rule | Owner |
|---|---|
| A ticket starts in `open` | `internal/ticket` |
| A timeline starts with the clock running | `internal/sla` |

Both are true. They agree because `StatusOpen.RunsClock()` is true. **Neither needs the
other to exist.** What looked like one rule written in the wrong package was two rules,
each already at home.

### What ADR 0002 decided that still holds

`Reconstruct` returns `(ClockState, error)` rather than panicking, validates its own
preconditions and nothing else, and does **not** check transition legality — that is the
state machine's job. Only the direction and the third sentinel are superseded.

## Consequences

**The conversion moves to the caller.** `internal/store` builds `sla.Phase` from history
rows via `row.ToStatus.RunsClock()`, and converts `ticket.Priority` to `sla.Priority`.
Today `store` is the composition point; once the modules split, this moves to
`internal/app` and nowhere else.

**Two test cases collapsed.** "Resolved stops the clock like pending does" and "closing a
resolved ticket adds nothing" were distinguishing ticket statuses, which this package no
longer sees. They were kept, renamed to describe the arithmetic they actually exercise —
their numbers still differ, so they still cover different paths. The claim they were
really making, *which statuses run the clock*, is asserted exhaustively over all four by
`TestOnlyOpenRunsTheClock` in `internal/ticket`, which already existed and whose comment
already said it was there so `internal/sla` would not have to know.

**The property test got stronger.** It used to generate random ticket statuses and let
`StatusOpen` stand in for "running". It now generates the boolean directly, which explores
every running/paused sequence rather than only the ones four particular statuses produce.
Which sequences a real ticket can reach is the state machine's property, and
`internal/ticket` asserts it.

**`internal/auth` is not cut yet, on purpose.** sqlc maps `users.role` onto whichever Go
type `sqlc.yaml` names, so pointing it at an `auth`-owned type would make the generated
`store` package import `auth` — which already imports `store`. The cycle closes. The way
out is not a temporary string column; it is `auth` owning its own generated queries, which
is the identity module's job in PR 3. The rule lands in the step that makes it true.

## Enforcement

`internal/architecture` asserts the boundary transitively, walking the import graph rather
than reading one file's import block — a package reached through an innocent-looking
helper has broken the boundary just as thoroughly, and that is the shape these violations
actually take. The rule was watched failing before it was trusted: reintroducing
`import "internal/ticket"` into `internal/sla` turns it red.

Each remaining PR adds its own rule to that package, red first.
