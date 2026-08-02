# ADR 0003: The health endpoint is `/health`, not `/healthz`

- **Status:** Accepted
- **Date:** 2026-08-02
- **Context:** Slice 1, task T3 (deploy the walking skeleton)
- **Related:** `tasks/todo.md` T3

## Context

`/healthz` is the conventional name for a health endpoint — it is what Kubernetes,
Knative and most Go services use, and it was the obvious first choice here.

**On Cloud Run it does not work.** Requests to that exact path never reach the container.
They are answered by the Google Frontend with a branded HTML 404 while the service is
perfectly healthy.

This cost several hours to find, because every plausible explanation was wrong: the URL
was correct, ingress was `all`, IAM was fine, the service was `Ready`, traffic was 100% on
a ready revision, and the container was running and logging.

## The measurement

A differential test against the deployed service. The `content-type` identifies who
answered: `text/html` is the Google Frontend, `text/plain` is chi's 404 from inside the
container.

```
/healthz        404  text/html    ← intercepted, never reached the container
/healthz?x=1    404  text/html    ← intercepted; the query string does not help
/healthz/       404  text/plain   ← reached the container
/healthzz       404  text/plain   ← reached the container
/health         404  text/plain   ← reached the container
/foo            404  text/plain   ← reached the container
/favicon.ico    404  text/plain   ← reached the container
```

Corroborated from the other side: Cloud Run's request logs
(`run.googleapis.com/requests`) contain entries for `/healthz/`, `/favicon.ico` and `/`,
and **never** for `/healthz`. Temporary instrumentation inside the router confirmed the
same — the handler chain saw every path except that one.

**Exactly one path string is affected.** One extra character, or a trailing slash, and the
request arrives normally.

## Decision

The health endpoint is **`/health`**, declared once as `api.HealthPath` so the string is
not repeated across the router, the tests and the runbook.

## Rationale

`/health` is measured to reach the container. `/healthz` is measured not to. Convention
loses to the platform.

**On the mechanism:** Cloud Run runs a `queue-proxy` sidecar inherited from Knative, which
uses `/healthz` for its own probing. That is the most likely explanation and is supported
by secondary sources, **but it is not confirmed by Google's documentation.** The Cloud Run
container runtime contract does not mention reserved paths at all. The behaviour above is
measured; the mechanism is inference, and this ADR does not claim otherwise.

## Consequences

- **Renaming this back to `/healthz` breaks production while every local test passes.**
  That is the trap: the failure only exists on Cloud Run, and it looks like a missing
  route rather than a platform behaviour. `api.HealthPath` carries a comment saying so.
- Anything checking service health — uptime monitors, Cloud Run probes, the runbook, CI —
  must use `/health`.
- A move to another platform does not require reverting this. `/health` works everywhere;
  `/healthz` does not.
- **Reserved paths are undocumented, so others may exist.** Any future endpoint that
  suddenly 404s in production while working locally should be checked with the same
  differential test before the application is suspected.

## The diagnostic technique, which generalises

The finding came from comparing **response headers**, not bodies. A 404 looks like a 404
until you look at who sent it:

| | Application (chi) | Google Frontend |
|---|---|---|
| `content-type` | `text/plain; charset=utf-8` | `text/html; charset=UTF-8` |
| `content-length` | 19 | ~1568 |
| `x-content-type-options` | `nosniff` | absent |
| `referrer-policy` | absent | `no-referrer` |

Capturing the local 404's fingerprint *first*, then comparing the deployed one against it,
is what turned an unfalsifiable "it 404s" into a decidable question. Use `curl -i`, never
plain `curl`: the body says what was returned, the headers say **who returned it**.
