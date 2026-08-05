# ADR 0006: A module owns its generated queries

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Refactor to a modular monolith, PR 3 of 6
- **Related:** [ADR 0005](./0005-modules-do-not-share-a-vocabulary.md)

## Context

ADR 0005 cut `internal/sla`'s dependency on `internal/ticket` and deliberately left
`internal/auth` alone. The reason was a cycle that could not be broken where it stood:

```
sqlc.yaml:  users.role  ──▶  auth.Role      (the override we wanted)
generated store         ──▶  auth           (so the store imports auth)
auth                    ──▶  store          (auth already imported store)
```

`internal/auth` needed to own its `Role`, and the only place to name that type was a
config that generated a package `auth` itself depended on. There is no ordering of those
three arrows that works.

The tempting escape was to have sqlc emit a plain `string` for `users.role` and convert in
Go. That trades a compile-time guarantee for a convention, and it is churn: the next step
would have had to type it again.

## Decision

**The users table moves into an identity module, which generates its own queries against
its own domain.**

```
internal/modules/identity/
├── domain/                     Role, User, Identity. Imports nothing but uuid
├── application/                Service, and the ports it needs
├── infrastructure/
│   ├── clerk/                  everything that knows Clerk's SDK exists
│   └── postgres/
│       ├── sqlc.yaml           users, and nothing else
│       ├── queries/users.sql
│       └── identitydb/         generated
├── transport/http/             RequireAuth, the Clerk webhook
└── module.go                   Authenticate, WebhookRoute
```

The cycle does not form because the generated package now sits *inside* the module and
depends on the module's **domain**, which imports nothing. The arrow that used to close the
loop runs into a leaf.

`omit_unused_structs: true` is what makes ownership structural rather than a convention.
`tickets` carries a foreign key into `users`, and the identity module's generated package
still contains **no Ticket** — there is no type to reach across the boundary with. The
same setting was added to the root config for the same reason, and `store.User` is gone
from it.

### Migrations stay shared

Each config's `schema:` points at `db/migrations`. There is one database and one goose
sequence, `tickets` references `users`, and a per-module DDL split would need an ordering
between modules' migrations — which is the coupling the split exists to remove. A copied
`schema.sql` is a second copy that drifts.

What is per-module is the **queries**. A module owns a table if it holds the statements
that read and write it.

### The domain carries `uuid.UUID`, not `pgtype.UUID`

`internal/auth.User.ID` was a `pgtype.UUID`, with a comment saying a friendlier type was a
decision for "the task that first serialises one". A domain package that must not import a
database driver is that moment, and `TestModuleDomainsArePure` is what makes it binding
rather than a preference.

`github.com/google/uuid` was already in `go.mod` as an indirect dependency and is now a
direct one. The cost is one conversion, in `internal/api/identity.go`, which is the
adapter that moves to `internal/app` in PR 5.

## Consequences

**`internal/auth` is deleted.** Its 1,028 lines are distributed across the module's layers,
and every test moved with the code it covers. Test count went from 162 to 163.

**The role conversion is now explicit and exhaustive.** `internal/api/identity.go` maps
`identity.Role` onto `ticket.Role` with a `switch` rather than a string conversion. The two
answer different questions — what someone *is* versus what they *were when they acted* —
and a role added to identity and not accounted for would otherwise reach
`ticket_status_history` as a string its CHECK constraint rejects. Falling through to the
least privileged role makes that a permission error rather than a failed write.

**`api.Deps.Identity` is required.** `NewRouter` returns an error when it is nil. This was
found by a test: `Deps{}` used to build a router, and after the change it was a nil pointer
dereference on the first request to a protected route. A startup error is the right shape
for a wiring mistake.

That same discovery exposed a test that would have passed for the wrong reason —
`TestRouterRefusesAnUnusableWebhookSecret` would have been satisfied by the missing-module
error instead of the bad-secret one. It now supplies the module and asserts on which
failure it got.

**Tests exercise the repository, not the generated queries.** The user integration tests
moved from `internal/store` and now call `postgres.UserRepository`, which is what
production calls — including the translation of `pgx.ErrNoRows` into
`application.ErrNoSuchUser` that `Resolve` branches on to decide whether to provision.

**The NULL-name assertion got stronger.** The domain carries a plain `string`, so the
repository is what turns an empty one into NULL. The test now reads the column back
(`SELECT name IS NULL`) rather than checking the returned value, because a returned `""` is
what a blank string would give too — which is precisely the distinction the column exists
to keep.

## Enforcement

Two rules in `internal/architecture`, each watched failing before it was trusted:

- `TestDomainsDoNotReachEachOther` now covers `identity -> ticket` and `identity -> sla`.
- `TestModuleDomainsArePure` refuses a database, HTTP, a router or an SDK in any module's
  domain package.

`make sqlc` discovers configs with `find` rather than listing them, so a module added with
its own queries and left off a hand-written list cannot silently stop being regenerated.
