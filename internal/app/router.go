// Package app is the composition root.
//
// It is the only package that may import more than one business module, and it
// is what turns three modules that know nothing about each other into one
// running service: it builds them, mounts their routes, and supplies each with
// the contracts the others implement.
//
// Nothing here contains a business rule. If a decision has to be made, it
// belongs in a module — this package only connects ends. Nothing may import it
// back either, and an architecture test enforces that: a module reaching into
// the wiring would be depending on its own neighbours through the back door.
package app

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity"
	identitydomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/domain"
	identityhttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/identity/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/config"
)

// HealthPath is the health endpoint.
//
// It is NOT /healthz. Cloud Run intercepts that exact path before the request
// reaches the container — see docs/adr/0003. Renaming it back will silently
// break the deployed service while every local test keeps passing.
const HealthPath = "/health"

// AgentPathPrefix is the one place an agent-only endpoint may be mounted.
//
// Every route under it sits behind a role check, and nothing outside it does.
// That is what lets a test assert the boundary by walking paths rather than by
// reading each handler and hoping none was missed.
const AgentPathPrefix = "/api/agent"

// Deps are the collaborators the routes need. Passed in rather than built here
// so the router can be assembled in tests without a database or a Clerk
// instance.
type Deps struct {
	// Probes back the health endpoint.
	Probes map[string]Probe

	// Identity answers who is making a request, and owns the webhook Clerk
	// posts user events to. Passed as the built module rather than as its
	// collaborators, so this package never names what is inside it.
	Identity *identity.Module

	// Tickets owns the ticket endpoints. Like Identity it is the built module,
	// not its collaborators: this package supplies what the module declared it
	// needs and does not reach inside it.
	Tickets *ticket.Module
}

// NewRouter builds the HTTP handler. The config is held rather than read from
// the environment so the router can be built in tests without touching the
// process environment, and the collaborators are injected so the routes can be
// exercised without a live database.
//
// It returns an error because an unusable Clerk webhook secret has to stop the
// process at startup. A route that answers 500 to every delivery, in an
// endpoint nobody watches, is the kind of failure that is discovered weeks
// later by a user who never got provisioned.
func NewRouter(cfg config.Config, deps Deps) (http.Handler, error) {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// middleware.RealIP is deliberately absent. It is deprecated in chi and
	// vulnerable to spoofing (GHSA-3fxj-6jh8-hvhx): it overwrites RemoteAddr
	// from X-Forwarded-For whether or not the infrastructure sets that header.
	// Slice 9 keys rate limiting on the client address, and a limit that can be
	// bypassed with a forged header is worse than none — it looks like
	// protection. When a real client IP is needed, it must be derived from a
	// trusted proxy hop count, not from the leftmost header value.

	// Runs before routing so a preflight is answered rather than rejected with
	// 405 by the method router.
	r.Use(corsMiddleware(cfg.CORSAllowedOrigin))

	r.Get(HealthPath, healthHandler(deps.Probes))

	// Clerk's webhook is mounted outside the authenticated group on purpose. It
	// carries a Svix signature, not a session JWT, so RequireAuth would reject
	// every delivery — and Svix would retry each one until it gave up, silently
	// breaking the primary provisioning path in docs/spec.md §4.5.
	// A nil module is a wiring mistake, and without this it is a nil pointer
	// dereference on the first request to reach a protected route — in
	// production, at 3am, rather than here.
	if deps.Identity == nil {
		return nil, errors.New("app: Deps.Identity is required; every protected route sits behind it")
	}
	if deps.Tickets == nil {
		return nil, errors.New("app: Deps.Tickets is required")
	}

	webhookPath, webhook, err := deps.Identity.WebhookRoute()
	if err != nil {
		return nil, err
	}
	r.Method(http.MethodPost, webhookPath, webhook)

	r.Group(func(r chi.Router) {
		// One chain, from the module. Both middlewares are required and in a
		// fixed order, and returning them together is what stops a caller
		// mounting only the token check and leaving these routes open
		// (docs/spec.md §4.3).
		r.Use(deps.Identity.Authenticate)

		// The module mounts its own routes on a router that already carries
		// authentication. It cannot mount them anywhere else, which is what
		// stops an endpoint being added outside this group by accident.
		tickethttp.Routes(r, deps.Tickets.HTTPHandlers(callerFromContext))

		// Everything an agent may do lives under one prefix, behind one role
		// check. Slice 2's reads have no requester predicate in their SQL —
		// an agent reads tickets that are not theirs — so this group is what
		// carries the guarantee the predicate used to (tasks/slice-2/plan.md
		// decisions B and C).
		//
		// A prefix rather than a role branch inside the existing handlers, for
		// a reason this file can demonstrate: the boundary is visible here, in
		// the URL, and in a test that walks every path under it as a customer.
		// A new agent endpoint is added inside a group that already refuses
		// everyone else, so forgetting the check is not something a reviewer
		// has to notice.
		//
		// The roles are named here rather than inside the module because this
		// is the package allowed to know what both modules mean by a role —
		// the same reason actorRole lives next door in adapters.go.
		r.Route(AgentPathPrefix, func(r chi.Router) {
			r.Use(identityhttp.RequireRole(identitydomain.RoleAgent, identitydomain.RoleAdmin))

			r.Method(http.MethodGet, identityhttp.MePath, identityhttp.MeHandler())
			r.Method(http.MethodGet, identityhttp.AssignablePath,
				identityhttp.AssignableUsersHandler(deps.Identity.Service))
			tickethttp.AgentRoutes(r, deps.Tickets.Service, callerFromContext)
		})
	})

	return r, nil
}
