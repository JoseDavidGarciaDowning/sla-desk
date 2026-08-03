# ADR 0004: One way to report an HTTP failure

- **Status:** Accepted
- **Date:** 2026-08-03
- **Context:** Slice 1, after T13. Found while verifying the create-ticket form against the running API
- **Related:** `docs/spec.md` §7, §11

## Context

This service claimed to report errors as RFC 9457 problem documents. Measured against the
running API, it reported them **three different ways**:

| Form | Where | Calls |
|---|---|---|
| `WriteProblem` — `application/problem+json` | `internal/api/tickets.go` | 7 |
| `http.Error` — `text/plain` | `internal/api/webhooks.go` | 6 |
| `w.WriteHeader` — **no body at all** | `internal/auth/middleware.go` | 2 |

```
$ curl -i -X POST localhost:8080/api/tickets -d '{}'
HTTP/1.1 401 Unauthorized
Content-Length: 0
```

The third form was not a stylistic lapse. The problem writer lived in `internal/api`, and
the dependency graph runs one way:

```
ticket (leaf) ◀── sla ◀── store ◀── auth ◀── api
```

`internal/api` imports `internal/auth`, so `auth` importing `api` would be a cycle. The
middleware that rejects every unauthenticated request **could not** reach the writer, and
answered with a bare status line because that was the only thing available to it.

A 401 with no body cannot be told apart from a 401 invented by a proxy in front of the
service, and the frontend reads the document to decide what to show.

## Decision

**`internal/httperr` owns what a failure looks like on the wire, and every layer calls it.**

```go
httperr.Write(w, http.StatusUnauthorized, "authentication required")
httperr.WriteValidation(w, fields)   // per-field 400
httperr.WriteInternal(w)             // 500, never echoing the cause
```

It sits below every layer that reports an error, and **imports nothing from this module**.
`TestHTTPErrDependsOnNothingInThisModule` walks the import graph and fails on any such
import.

That test is the load-bearing part. Without it, one import of `internal/ticket` — to name a
role in a message, say — puts `httperr` above `ticket`, and the next package that needs to
report an error finds it out of reach. That is precisely the position it was extracted to
escape, and nothing else would notice.

The webhook handler's six `http.Error` calls were converted at the same time. Svix only
reads the status, so the body arguably does not matter there — but that is an argument for
the body being unimportant, not for it being *different*. Clerk's delivery log shows it,
and consistency costs nothing.

## Rejected: `internal/shared`

Proposed, and it would have worked. The objection is to the name, not the structure.

A package named for the fact that several callers use it has an admission rule of "more
than one thing imports this" — a fact about the call graph, not about the concept. **That
rule can never reject anything.** Everything eventually has two callers, so the package only
grows, and its name tells a reader nothing about what is inside. This is the `util` /
`common` / `misc` failure, and the Go standard library has no such package.

`httperr` has a rule that can say no: *does this decide how an error appears in a response?*
Problem documents do. A UUID formatter does not, however many packages want one.

The investigation that preceded this found **exactly one** genuinely shared concern, so
there was nothing else a `shared` package would have held anyway. If a second, unrelated one
appears, it gets its own named package. Two cohesive packages cost less to read than one
drawer.

## Also settled by the same investigation

- **`internal/api`'s `contains[T ~string]` was deleted.** `internal/ticket/transition.go`
  already used `slices.Contains` for the same operation. It was not a shared helper needing
  a home; it was the standard library, reimplemented.
- **`uuidString` stays in `internal/api`.** Only the transport layer renders a UUID as text.
  `auth.User.ID` is deliberately a `pgtype.UUID` (`internal/auth/user.go`), so there is no
  second caller.
- **`join`, `encodeCursor`, `decodeCursor`, `pageSize` stay where they are.** One consumer
  each.

## Consequences

- Every error path in the service answers `application/problem+json` with a body whose
  `status` agrees with the HTTP status.
- `ApiError.fieldErrors` on the frontend now has a document to read on every failure, not
  only on a 400 from a ticket handler.
- `internal/httperr` is a new place a change can be made, and the architecture test is what
  keeps it from becoming a place anything can be put.
- The rule generalises: a package is named for what it provides. If a future extraction is
  proposed as "shared", that is a signal the concern has not been identified yet.
