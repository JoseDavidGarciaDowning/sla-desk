# Working in this repository

This file is for AI coding agents. It holds what an agent needs and the rest of the
documentation does not already say. **It deliberately does not restate the architecture** —
a fact written twice is a fact that will eventually disagree with itself, and the copy that
rots is the one no test reads.

| You need | Read |
|---|---|
| Where does this code go? | [`docs/architecture.md`](docs/architecture.md) — the map, the placement table, the nine rules |
| Why is it like this? | [`docs/adr/`](docs/adr/) — twelve decisions, each with the option it rejected |
| What is it supposed to do? | [`docs/spec.md`](docs/spec.md) |
| How do PRs work here? | [`sla-desk-pr-workflow-cheatsheet.md`](sla-desk-pr-workflow-cheatsheet.md) |

The frontend has its own: [`web/AGENTS.md`](web/AGENTS.md). This one is about the Go API.

---

## Read the module before you add a file to it

Not the whole repo — the module you are changing. Its `module.go` says what it offers, its
`features/` listing says what it does, and its `ports/` says what it needs from outside.
Three files, and they answer most placement questions before you have to reason about one.

The most common failure is not writing wrong code. It is writing correct code in a
directory that makes it unfindable.

---

## Where new code goes

The architecture is **modular DDD, sliced by use case, with hexagonal dependency
direction**. Three ideas, and they answer different questions:

- **Modules** (`ticket`, `sla`, `identity`) are the business boundaries. A module never
  imports another.
- **Features** are the use cases inside a module. One directory each, named for what the
  system *does*.
- **Ports and adapters** decide which way dependencies point: application and domain code
  depends on abstractions, infrastructure implements them.

Work down this list and stop at the first match.

### Is it a business rule, or a concept the business would recognise?
→ `internal/modules/<module>/domain/`

No HTTP, no SQL, no framework, no SDK — enforced by a test. Everything it needs is an
argument. An error raised by more than one feature is a domain concept and lives here too
(`domain/errors.go`).

### Is it something the system *does*?
→ `internal/modules/<module>/features/<use case>/`

Named for the action: `create`, `assign`, `change_status`. Not `ticket_service`, not
`handlers`. If you cannot name it as a verb, you may be describing a layer rather than a use
case.

**A feature may not import another feature** — not directly, and not through a helper two
hops away. That is rule 7, and it is enforced twice: by the architecture test on the
transitive import graph, and by depguard, which sees the `//go:build integration` files the
graph walk cannot.

When two features need the same thing, move it *below* both — a rule into `domain`, a
contract into `ports`, a wire shape into `transport/http`. Never sideways.

### Does the feature need an input or output contract of its own?
→ `dto.go` inside that feature

Only that feature's. There is no global `dto/` directory and there will not be one.

### Does the feature need an HTTP adapter?
→ `http.go` inside that feature

Then add the built handler to the module's `transport/http/routes.go`. **A feature never
mounts its own routes.** The two lists in that file — the customer's and the agent's — are
the only place the answer to "what is behind the role check" exists (rule 9).

### Is the input to the use case big enough to read badly inside `handler.go`?
→ `command.go` inside that feature

Only when it is. Most are three fields and belong beside the handler that takes them.

### Does the feature need something from outside the module?
→ Declare the interface **in the feature**, beside the handler that uses it

The consumer owns the interface (`docs/spec.md` §8). Name it for what you need, in your own
vocabulary — the ticket module asks `CanHoldTickets`, not `IsAgent`.

### Do **several** features in the module need the same outside thing?
→ `internal/modules/<module>/ports/`

Only then. `identity` has no `ports` package because nothing there crosses features, and
that is correct rather than an oversight.

### Is it the implementation of one of those contracts?
→ `internal/modules/<module>/infrastructure/`

Postgres, Clerk, anything external. A module gets only the infrastructure it actually needs.

### Is it SQL you wrote?
→ `internal/modules/<module>/infrastructure/postgres/queries/`

### Is it SQLC output?
→ `internal/modules/<module>/infrastructure/postgres/generated/`

**Never edit a file in `generated/`.** Change the `.sql` and run `make sqlc`. The folder is
named so that there is no reading under which editing it is correct.

### Does it decide something *no business module owns*?
→ `internal/platform/`

The admission question is **not** "is it used more than once" — that is a fact about the
call graph, not about the concept. A helper that knows what a ticket is fails it, however
many places call it.

### Does it connect two modules, or decide what a route sits behind?
→ `internal/app/`

The only package that may import more than one module. It contains no business rule.

---

## Progressive complexity

**Start with the smallest structure that expresses the code, and add a file when the code in
it stops fitting. Never to complete a pattern.**

```
features/me/          http.go                       ← calls no use case; one file is right
features/list/        handler.go dto.go http.go
features/get/         handler.go http.go agent.go   ← two guarantees, side by side
```

A feature with one file is not unfinished. A feature with six files it did not need is
harder to read than the layer it replaced, and it is the failure this rule exists to
prevent — the temptation is always to add the file now "so the structure is consistent".
Consistency is in *where things go*, not in how many files each one has.

The same applies at module scale. `sla` has one feature. `ticket` has seven. Both are
correct.

---

## Never create these

`services/` · `controllers/` · `repositories/` · `dto/` · `models/` · `utils/` · `common/` ·
a new `application/` layer

They organise by technical kind, which is the arrangement this codebase was reorganised
away from. If you believe you need one, you are looking for either a feature or something
that belongs below several of them — say which in the PR rather than creating the directory.

---

## Before you open a PR

```bash
make fmt          # gofumpt + gci
make lint         # golangci-lint, incl. the integration and smoke build tags, and eslint
make arch         # the boundary rules, as a test
go test ./...     # unit
make test-int     # integration; needs a database
```

Two that are easy to skip and should not be:

- **`go vet -tags=integration ./...`** after any type rename. The architecture test's graph
  walk uses the default build context and **cannot see files behind `//go:build
  integration`**. `make lint` covers them; a plain `go build` does not.
- **Count the tests before and after a move.** Deleting a package deletes its tests, and the
  diff will not tell you.

  ```bash
  before=$(git grep -h "^func Test" main -- 'internal/*' | sed 's/(.*//' | sort)
  after=$(grep -rh "^func Test" internal --include="*_test.go" | sed 's/(.*//' | sort)
  comm -23 <(echo "$before") <(echo "$after")   # must be empty
  ```

Every PR targets `main` and must leave it deployable on its own. Do not stack branches —
open the second when the first has merged. The cheatsheet explains why.
