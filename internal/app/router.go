package app

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/health"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// Router builds the HTTP handler.
//
// It returns an error because an unusable Clerk webhook secret has to stop the
// process at startup. A route that answers 500 to every delivery, in an
// endpoint nobody watches, is the kind of failure that is discovered weeks
// later by a user who never got provisioned.
func (a *App) Router() (http.Handler, error) {
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
	r.Use(httpx.CORS(a.cfg.CORSAllowedOrigin))

	r.Get(health.Path, health.Handler(a.probes))

	// Clerk's webhook is mounted outside the authenticated group on purpose. It
	// carries a Svix signature, not a session JWT, so authentication would
	// reject every delivery — and Svix would retry each one until it gave up,
	// silently breaking the primary provisioning path in docs/spec.md §4.5.
	webhookPath, webhook, err := a.Identity.WebhookRoute()
	if err != nil {
		return nil, err
	}
	r.Method(http.MethodPost, webhookPath, webhook)

	r.Group(func(r chi.Router) {
		// One chain, handed over whole by the identity module: the first half
		// verifies the token and rejects nothing, the second turns an
		// unauthenticated request into a 401 and resolves the caller. Taking
		// them separately is what would let someone mount the half that
		// verifies without the half that rejects, leaving these routes open
		// (docs/spec.md §4.3).
		r.Use(a.Identity.Authenticate)

		// Every module that has authenticated routes mounts them here. A module
		// never mounts itself on the root router, so "is this endpoint behind
		// authentication" is answered by which group it is in rather than by
		// reading the module.
		a.Ticket.Routes(r)
	})

	return r, nil
}
