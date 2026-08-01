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

// NewRouter builds the HTTP handler. The config is held rather than read from
// the environment so the router can be built in tests without touching the
// process environment.
func NewRouter(cfg config.Config) http.Handler {
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

	r.Get("/healthz", healthz)

	return r
}

type healthResponse struct {
	Status string `json:"status"`
}

// healthz reports liveness only.
//
// T3 extends it to verify real connectivity to Postgres before returning ok: a
// health check that proves only that the process is running proves nothing about
// whether the deployment actually works.
func healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, healthResponse{Status: "ok"})
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
