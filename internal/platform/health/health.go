// Package health reports whether this instance can serve traffic.
//
// It is platform rather than module code: what it knows is that a dependency
// either answers or does not. Which dependencies exist is the composition
// root's business, and arrives as a map of probes.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// Path is the health endpoint.
//
// It is NOT /healthz. Cloud Run intercepts that exact path before the request
// reaches the container — see docs/adr/0003. Renaming it back will silently
// break the deployed service while every local test keeps passing.
const Path = "/health"

// Probe reports whether one dependency is reachable.
//
// It is a function rather than an interface because every implementation is a
// single call, and tests can then supply one inline without a stub type.
type Probe func(ctx context.Context) error

// probeTimeout bounds a single dependency check. Without it an unreachable
// database would hold the health request open until the server timeout, and the
// platform would read a hang rather than a failure.
const probeTimeout = 2 * time.Second

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Handler runs every probe and fails the request if any of them fails.
//
// Liveness alone is not worth reporting: a process that is running but cannot
// reach its database is not healthy, and the platform needs to know so it stops
// routing traffic to this instance.
func Handler(probes map[string]Probe) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checks := make(map[string]string, len(probes))
		healthy := true

		for name, probe := range probes {
			ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
			err := probe(ctx)
			cancel()

			if err != nil {
				healthy = false
				checks[name] = "failed"

				// This endpoint is public. Driver errors carry hostnames and
				// sometimes credentials, so the detail goes to the logs and the
				// client learns only that the check failed.
				slog.ErrorContext(r.Context(), "health probe failed",
					"probe", name, "error", err)

				continue
			}

			checks[name] = "ok"
		}

		body := healthResponse{Status: "ok", Checks: checks}
		status := http.StatusOK

		if !healthy {
			body.Status = "degraded"
			status = http.StatusServiceUnavailable
		}

		httpx.WriteJSON(w, r, status, body)
	}
}
