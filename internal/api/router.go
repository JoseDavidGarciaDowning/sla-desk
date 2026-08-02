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

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/config"
)

// HealthPath is the health endpoint.
//
// It is NOT /healthz. Cloud Run intercepts that exact path before the request
// reaches the container — see docs/adr/0003. Renaming it back will silently
// break the deployed service while every local test keeps passing.
const HealthPath = "/health"

// NewRouter builds the HTTP handler. The config is held rather than read from
// the environment so the router can be built in tests without touching the
// process environment, and the probes are injected so health checking can be
// exercised without a live database.
func NewRouter(cfg config.Config, probes map[string]Probe) http.Handler {
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

	r.Get(HealthPath, healthHandler(probes))

	return r
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
