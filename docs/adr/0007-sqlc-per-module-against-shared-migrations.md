# ADR 0007: One sqlc config per module, generated against the shared migrations

- **Status:** Accepted
- **Date:** 2026-08-04
- **Context:** Splitting `internal/store` into three modules
- **Related:** `docs/adr/0005` (module boundaries)

## Context

There was one `sqlc.yaml` generating one `internal/store` package containing every table in
the schema. Any package that imported it could name `store.User`, `store.Ticket` and
`store.SlaPolicy` — so "the ticket module must not read the users table" was a rule someone
had to remember, with a `User` struct sitting in scope offering to help them forget.

## Decision

**Each module that owns tables carries its own sqlc config, next to its own queries.**

```
internal/modules/ticket/infrastructure/postgres/
    sqlc.yaml
    queries/{tickets,ticket_status_history,clock}.sql
    ticketdb/          generated — Ticket, TicketStatusHistory, and nothing else
```

Two settings do the work:

```yaml
schema: "../../../../../db/migrations"   # the migrations that actually ship
omit_unused_structs: true                # only tables this module queries
```

`omit_unused_structs` is what turns ownership from a convention into a structural fact.
`tickets` carries foreign keys into both `users` and `sla_policies`, and the ticket
module's generated package still contains **no `User` and no `SlaPolicy`**. There is no
type to reach across the boundary with.

Verified by generation, not assumed:

| Module | Generated models |
|---|---|
| `ticket` | `Ticket`, `TicketStatusHistory` |
| `sla` | `SlaPolicy` |
| `identity` | `User` |

### Migrations stay global

`db/migrations` is not split, and each module's `schema:` points at it. This is a
deliberate departure from "each module carries its own `schema.sql`":

- There is **one database and one goose sequence**. `tickets` references `sla_policies` and
  `users`, so the tables cannot be created independently — a per-module DDL split would
  need an ordering between the modules' migrations, which is the coupling the split was
  meant to remove.
- A copied `schema.sql` is a **second copy of the DDL that drifts**. The original config
  called this out and it still holds: sqlc compiles against the schema that actually
  ships, so there is nothing to keep in sync.

What is per-module is the **queries**, which is where ownership actually lives. A module
owns a table if it holds the statements that read and write it.

## Enforcement

- `make sqlc` discovers configs with `find internal/modules -name sqlc.yaml`. Adding a
  module means adding a config next to that module's queries, never editing a shared file.
- `TestEachModuleOwnsItsOwnSQLCConfig` fails if a root `sqlc.yaml` reappears, and if a
  module has an `infrastructure/postgres` directory without a config of its own.

## Consequences

- Three configs share the same override block for timestamps and UUIDs. Real duplication,
  and the alternative — one shared config — is the thing being removed. Each module's
  domain-type overrides are genuinely its own, which is most of the block.
- Generation takes three sqlc invocations instead of one. Under a second.
- A module that needs another module's data asks that module for it, because the query and
  the generated type it would need do not exist on its side.

## A gotcha worth recording

sqlc v1.31.1 emits an import twice — once plain, once aliased — if the short and long
`go_type` spellings are mixed for the same package:

```go
import (
    "github.com/google/uuid"
    uuid "github.com/google/uuid"   // redeclared; does not compile
)
```

The nullable variant forces the long form because it needs `pointer: true`, so the mistake
is easy: short for the non-null column, long for the nullable one. **Use the long form for
both.** All three configs do.
