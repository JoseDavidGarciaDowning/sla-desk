package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// AgentHandlers are the module's agent-group endpoints, already built.
//
// Already built for the reason the ticket module's are: if this package
// constructed them it would import every feature, and the features import this
// package for the middleware's context accessor. Taking them as values keeps
// the arrow pointing one way.
//
// The webhook is deliberately not here. It is mounted outside the authenticated
// group — it carries a Svix signature, not a session JWT — and putting it in
// the same struct as two routes that sit behind a role check would invite
// somebody to mount all three together.
type AgentHandlers struct {
	Me         http.Handler
	Assignable http.Handler
}

// AgentRoutes mounts the endpoints only an agent may reach.
//
// It is given a chi.Router that already carries authentication and a role
// check, so an endpoint cannot be added to the wrong group by forgetting to.
// See internal/app.
func AgentRoutes(r chi.Router, h AgentHandlers) {
	r.Method(http.MethodGet, MePath, h.Me)
	r.Method(http.MethodGet, AssignablePath, h.Assignable)
}
