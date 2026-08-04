# Clerk: how it is wired, and what bites

Last updated: 2026-08-03

Reference for the Clerk side of this project. It covers the two request paths, why one
environment variable does two different jobs, the failure modes that produce a silent
`401`, and the behaviours of `clerk-sdk-go/v2` that are not what its documentation implies.

Written because every one of these cost time to work out, and none of them are obvious from
the code alone.

---

## 1. Two paths that never touch

The single most useful thing to hold in your head: **Clerk reaches this system in two
completely separate ways, and they share nothing.**

```
  ┌─────────────────────┐
  │  Clerk's servers    │
  └──────────┬──────────┘
             │  POST /api/webhooks/clerk
             │  server to server. No browser exists.
             │  Authenticated by a Svix signature.
             ▼
      ┌─────────────┐
      │  Go API     │      CORS is not involved. There is no origin.
      │  :8080      │
      └─────────────┘
             ▲
             │  GET /api/tickets
             │  cross-origin: 3000 calling 8080.
             │  Authenticated by a Clerk session JWT.
             │
  ┌──────────┴──────────┐
  │  Browser on :3000   │
  └─────────────────────┘
```

Consequences worth stating plainly:

- **The webhook never passes through CORS.** No browser is involved, so there is no
  `Origin` header to allow or refuse. A CORS misconfiguration cannot break provisioning.
- **The webhook is not behind `RequireAuth`.** Clerk sends a signature, not a session
  token; behind auth every delivery would be answered `401`, Svix would retry each one
  until it gave up, and provisioning would be silently dead. This is enforced by
  `TestTheClerkWebhookIsNotBehindRequireAuth`.
- **Ports:** `8080` is the Go API, `3000` is the Next.js frontend. Two processes. The
  webhook relay forwards to **8080**, because that is where the endpoint lives.

---

## 2. One value, two jobs

`CORS_ALLOWED_ORIGIN` is the origin the frontend is served from. It is used twice, for
different reasons, and confusing them is where the trouble starts.

| | Decides | Enforced by |
|---|---|---|
| **CORS** | Which origin a *browser* may call this API from | `httpx.CORS`, `internal/platform/httpx/cors.go` |
| **`azp`** | Which origin a *token* was minted for | `clerkhttp.AuthorizedPartyMatches`, via `auth.Config` |

When the frontend asks Clerk for a session token, Clerk stamps the requesting origin into
the token's `azp` claim. Our API compares that claim against `CLERK_AUTHORIZED_PARTY`,
which **falls back to `CORS_ALLOWED_ORIGIN` when unset** — because in practice they are the
same origin, and an operator who set one and forgot the other would lose the check without
noticing.

### Why the `azp` check exists at all

`jwt.Verify` in the Clerk SDK validates the issuer by **shape**, not by identity:

```go
strings.HasPrefix(iss, "https://clerk.") || strings.Contains(iss, ".clerk.accounts")
```

Any Clerk instance's issuer passes that. What actually binds a token to *our* instance is
the JWKS — the signature has to verify against our public key. The authorized party adds
the second half: that the token was minted for *our frontend*, not for some other origin of
the same instance.

---

## 3. The failure mode: a silent 401 in development

This is the one that costs an afternoon.

```
.env  (local)
CORS_ALLOWED_ORIGIN=https://sla-desk-phi.vercel.app     ← left over from production

browser on http://localhost:3000
  └─ asks Clerk for a token  →  azp: "http://localhost:3000"
  └─ calls the API           →  azp ≠ expected  →  401
```

Nothing in the response says why. The token is valid, the signature verifies, the user
exists, the session is live — and every request is refused. The API log records
`rejected an unverified request`, which sounds like the token is bad. It is not; it was
minted for the wrong origin.

**In development:**

```
CORS_ALLOWED_ORIGIN=http://localhost:3000
```

**In production:** the Vercel URL. If the two ever need to differ, set
`CLERK_AUTHORIZED_PARTY` explicitly rather than bending the CORS origin to fit.

---

## 4. Development and production are two instances

Every Clerk application is provisioned with a **Development** instance and, separately, a
**Production** one. The dropdown at the top left of the dashboard switches between them.

They do not share anything that matters here:

| | Development | Production |
|---|---|---|
| Secret key | `sk_test_…` | `sk_live_…` |
| Publishable key | `pk_test_…` | `pk_live_…` |
| Webhook endpoint | its own | its own |
| Signing secret | its own `whsec_…` | a **different** `whsec_…` |

The signing secret belongs to the *endpoint*, not to the application. Two endpoints means
two secrets, and configuring only one is why "it worked locally" and production receives
nothing.

---

## 5. Local webhook delivery

Clerk ships a CLI that relays deliveries to a local port. It replaces the `svix listen`
approach the plan originally recorded.

```bash
npx clerk@latest webhooks listen \
  --token c_YOUR_RELAY_TOKEN \
  --forward-to http://localhost:8080/api/webhooks/clerk
```

It prints a relay URL:

```
URL:             https://webhooks.clerk.com/in/c_YOUR_RELAY_TOKEN/
Forwarding to:   http://localhost:8080/api/webhooks/clerk
```

### Register that URL exactly as printed

**Do not append `/api/webhooks/clerk` to it.** The relay is not a generic tunnel to your
host — the local path was supplied by `--forward-to` and is already baked into the relay.
Appending it produces a URL that resolves to nothing.

This differs from the ngrok flow in Clerk's own documentation, where the path *is*
appended because ngrok exposes the whole host and preserves paths. The two mechanisms look
alike and are not.

### Always pass `--token`

Without it the CLI generates a relay token that **can change** across machines, a cleared
config, or a collision. The dashboard endpoint — and its signing secret — is tied to that
URL. When the token changes, Clerk keeps delivering to a URL that no longer forwards
anywhere: no error, no failed delivery, just events that never arrive.

Pin it, and keep the command somewhere you will find it again.

### Setting the endpoint up

1. Start the CLI first — the signing secret does not exist until an endpoint does.
2. Dashboard → **Webhooks** → **Add Endpoint**.
3. Paste the relay URL, unchanged.
4. Under **Message Filtering**, select **`user.created`** and **`user.updated`** only.
   Everything else is answered `200` and ignored by our handler, so subscribing to more is
   noise.
5. **Create**. The endpoint's settings page shows the **Signing Secret**.

> The CLI reports `Verification: off`. That is about the *relay*, which only forwards.
> Clerk still signs the delivery and our handler still verifies it with the endpoint's
> signing secret. Nothing is unverified.

---

## 6. Environment variables

```
CLERK_SECRET_KEY=sk_test_...        required — the API will not start without it
CLERK_WEBHOOK_SECRET=whsec_...      required — same
CORS_ALLOWED_ORIGIN=http://localhost:3000
CLERK_AUTHORIZED_PARTY=             optional — defaults to CORS_ALLOWED_ORIGIN
CLERK_API_URL=                      optional — for a Clerk proxy, and for tests

NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY=pk_test_...   frontend only, from T12 onward
```

The two required ones are required **on purpose**: a process that booted without them
could only answer `/health`, and nobody would find out until a user failed to sign in.

**Naming:** ours is `CLERK_WEBHOOK_SECRET`. Clerk's documentation calls it
`CLERK_WEBHOOK_SIGNING_SECRET`, because that is what their Next.js example uses. Same
value, different name.

---

## 7. What `clerk-sdk-go/v2` actually does

Measured against v2.7.0 by reading the source, not inferred from the documentation. Several
of these contradict what the docs imply.

**`WithHeaderAuthorization` rejects nothing.** Known — but the detail matters: a request
with **no token and one with a token it cannot even decode both pass straight through** to
the next handler. Only a token that decodes and then fails verification reaches the failure
handler. Mounting it alone leaves a route open.

**`RequireHeaderAuthorization` exists and does reject — with `403`.** The failure handler
answers `401`. That is a mixed status surface which tells an attacker which of their
guesses was closer, so we do not use it. `auth.Middleware` plus our own `auth.RequireAuth`
answers `401` to all nine failure modes we test.

**`jwt.Verify` does not cache the JWK.** Caching moved to the caller in v2. The *HTTP
middleware* does cache, by key id, for an hour — so mounting `WithHeaderAuthorization`
satisfies the requirement and no cache of ours is needed.

**That cache is process-global and keyed by key id alone**, with no scoping by instance or
issuer. Harmless with one Clerk instance. It made our tests leak public keys into each
other until each got a unique key id.

**The session token carries `sub` and not the email or name.** `clerk.RegisteredClaims` has
no such field. Provisioning fetches them from the Backend API — once per user, on the first
request we see from them — and picks the address Clerk marks primary rather than the first
in the list.

**`clerk.SetKey` is package-global state** and is not used here. `&clerk.ClientConfig{...}`
passes the key explicitly, which spec §8 requires and which is also what lets tests point
the SDK at an `httptest.Server`.

---

## 8. Testing without a Clerk instance

The whole verification path is exercised with no network and no Clerk account: generate an
RSA key, serve a standard JWKS document from `httptest`, hand-sign RS256 tokens, and point
`CLERK_API_URL` at the stub. That covers expired, wrong-key, tampered, unknown-key-id,
wrong-`azp` and non-Clerk-issuer tokens.

Webhook fixtures are signed with Svix's own exported `Sign`, so the tests use the same
implementation that verifies them rather than a reimplementation that would agree with
itself whatever it did.

- `internal/modules/identity/transport/http/authentication_test.go` — the JWT path
- `internal/modules/identity/transport/http/webhook_test.go` — the Svix path
- `internal/app/router_test.go` — the assembled chain, end to end

---

## Related

- [spec §4.3](spec.md#43-authentication-and-authorization) — the identity/authorisation split
- [spec §4.5](spec.md#45-user-provisioning-clerk--our-database) — provisioning and the signup race
- [checkpoint-c.md](checkpoint-c.md) — what the backend delivers and what is proven
