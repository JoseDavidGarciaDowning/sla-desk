# ADR 0010: A name that lies is a bug

- **Status:** Accepted
- **Date:** 2026-08-05
- **Context:** Naming audit of `internal/modules` after the modular monolith landed
- **Amends in part:** [ADR 0007](./0007-resolving-a-policy-is-io-computing-with-one-is-not.md) — the
  type it calls `Calculator` is now `Policies`. The decision that ADR records is unchanged.
- **Related:** [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md),
  [ADR 0009](./0009-the-composition-root.md)

## Context

Reading the modules from the outside, four kinds of unfamiliar name are easy to confuse.
Three of them are worth learning and one is worth changing, and telling them apart is the
whole problem:

1. **A Go convention.** `ByClerkID` rather than `GetUserByClerkID`, because *Effective Go*
   says a getter for `owner` is `Owner()`. `CallerResolver` as a `func` type rather than an
   interface, with `http.HandlerFunc` as the precedent. The `, bool` second result, which is
   the comma-ok idiom.
2. **Hexagonal vocabulary.** `Caller` is an anti-corruption layer: the ticket module states
   who it needs in its own words so it never imports the identity module. `SLAPolicies` and
   `SLAClock` are consumer-declared ports.
3. **Domain vocabulary.** `Provision` is the term SCIM and every identity provider uses for
   creating the local account from an upstream IdP. `Upsert` is SQL.
4. **A name that is simply wrong.**

The first three are learned once and pay for themselves, because they connect this codebase
to literature written about it. Renaming them would cut that link. Only the fourth is a
defect, and it needs a test sharper than "I would have called it something else".

## Decision

**A name is a defect when it lies about what the thing does. There are exactly two ways to
lie: promising less than it does, and promising what it does not do.**

Anything else — short, unfamiliar, abstract, not how another language would spell it — is
not grounds for a rename. The test is applied by reading the body, not by comparing taste,
which is what makes it settleable in review.

Four names failed it.

### `Service.Resolve` → `Service.EnsureUser`

Promised less than it does. On a miss it calls Clerk over the network and inserts a row,
and `RequireAuth` runs it on every authenticated request. "Resolve" is a real word —
GraphQL resolvers, DNS, dependency resolvers — and every one of those is a **read**.

The middleware's one-method interface could no longer be `Resolver`, and `UserEnsurer` is
worse than what it names. It is `UserSource`, named for the role rather than the verb, with
`math/rand.Source` as the precedent for dropping `-er` when the verb is awkward.

### `Calculator` → `Policies`

Promised what it does not do. It reads policies and returns them; no method computes
anything. ADR 0007 says so in as many words — "`Calculator` resolves policies and does not
wrap the arithmetic" — and treats it as a virtue, which it is. The design is right; the
name pointed away from it. Anyone looking for the deadline maths opened `calculator.go` and
did not find it, because it lives on `domain.Policy`.

The file is `policies.go` for the same reason.

### `Calculator.ForPolicy` → `Policies.ByID`

Took a policy id and returned a policy, naming neither end of the trip. `PolicyRepository`
underneath had `ByID` all along; the layer above had taken the worse name.

**`SLAPolicies.ForPolicy` on the ticket side keeps its name.** It takes a policy id and
returns an `SLAClock` — different thing in, different thing out — so it says something. Two
methods spelled alike were not equally wrong, and only the circular one moved.

### `ListByRequester` / `GetForRequester` → `ListForRequester` / `OneForRequester`

Two prepositions for one relationship, and a `Get` prefix on the one method that had it
while its two siblings did not. Nothing here lied, but which spelling belonged to which
method had to be remembered rather than worked out. The three now differ only in what they
return.

## Consequences

No behaviour changes. The renames are mechanical, the existing suites cover them, and the
boundaries in `internal/architecture` are untouched because no import moved.

`Provision`, `Upsert`, `Caller`, `CallerResolver`, `SLAPolicies`, `SLAClock`, `module.go`,
`New`/`NewWith` and the `By*` repository methods were audited and deliberately kept. So was
the module name `identity` over `auth`: Clerk performs authentication, and this module
answers who someone is in our system and what role they hold, which is the ambiguity `auth`
is famous for. It is not airtight — the module also exposes `RequireAuth` — but it is the
most accurate of the available names, and `auth` would be a step backwards.

The cost of the rule is that it licenses a rename whenever someone can show the body
disagrees with the name, which is a small and bounded kind of churn. The cost of not having
it is the other thing: a reviewer with no way to tell "I dislike this name" apart from
"this name is wrong", and both arguments carrying equal weight.
