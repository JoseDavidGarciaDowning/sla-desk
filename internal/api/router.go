// Package api wires HTTP routes to handlers. It owns transport concerns only:
// status codes, serialisation and middleware. Business rules live in the domain
// packages and are never decided here.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/auth"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/config"
)

// HealthPath is the health endpoint.
//
// It is NOT /healthz. Cloud Run intercepts that exact path before the request
// reaches the container — see docs/adr/0003. Renaming it back will silently
// break the deployed service while every local test keeps passing.
const HealthPath = "/health"

// Deps are the collaborators the routes need. Passed in rather than built here
// so the router can be assembled in tests without a database or a Clerk
// instance.
type Deps struct {
	// Probes back the health endpoint.
	Probes map[string]Probe

	// Users is the provisioning side of the store, used by both the webhook
	// and the lazy fallback in RequireAuth.
	Users auth.Provisioner

	// Tickets writes; Reader reads. Two interfaces rather than one because the
	// write path needs a transaction and the read path does not.
	Tickets TicketCreator
	Reader  TicketReader
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
	webhook, err := ClerkWebhookHandler(cfg.ClerkWebhookSecret, deps.Users)
	if err != nil {
		return nil, err
	}
	r.Method(http.MethodPost, ClerkWebhookPath, webhook)

	clerkCfg := auth.Config{
		SecretKey:       cfg.ClerkSecretKey,
		AuthorizedParty: cfg.ClerkAuthorizedParty,
		APIURL:          cfg.ClerkAPIURL,
	}

	r.Group(func(r chi.Router) {
		// Two middlewares, in this order and both required. The first verifies
		// the token and attaches claims but rejects nothing; the second is what
		// turns an unauthenticated request into a 401 and resolves the caller
		// into one of our users. Mounting only the first leaves these routes
		// open (docs/spec.md §4.3).
		r.Use(auth.Middleware(clerkCfg))
		r.Use(auth.RequireAuth(deps.Users, auth.NewIdentityFetcher(clerkCfg)))

		r.Method(http.MethodPost, TicketsPath, CreateTicketHandler(deps.Tickets))
		r.Method(http.MethodGet, TicketsPath, ListTicketsHandler(deps.Reader))
		r.Method(http.MethodGet, TicketsPath+"/{id}", GetTicketHandler(deps.Reader))
	})

	return r, nil
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already on the wire, so the client cannot be told.
		// Log it rather than swallowing it silently.
		slog.ErrorContext(r.Context(), "encoding response failed",
			"error", err, "path", r.URL.Path)
	}
}
