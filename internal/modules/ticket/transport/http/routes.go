package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// Handlers are the module's customer endpoints, already built.
//
// Already built, and that is the design rather than an inconvenience. If this
// package constructed them it would have to import every feature — and every
// feature imports this package for the wire shapes they share, which is a
// cycle. Taking them as values keeps the arrow pointing one way: features
// depend on the module's shared HTTP vocabulary, and the module's front door
// depends on the features.
//
// It also leaves this file as nothing but the route table, which is what makes
// the table worth having.
type Handlers struct {
	Create  http.Handler
	List    http.Handler
	Get     http.Handler
	History http.Handler
}

// Routes mounts every customer ticket endpoint.
//
// All of them must sit behind authentication — every handler reads the caller
// from the request context and answers 401 without one. This function does not
// decide that: it is given a chi.Router that already carries the authentication
// middleware, so an endpoint cannot be added to the wrong group by forgetting
// to. See internal/app.
func Routes(r chi.Router, h Handlers) {
	r.Method(http.MethodPost, TicketsPath, h.Create)
	r.Method(http.MethodGet, TicketsPath, h.List)
	r.Method(http.MethodGet, TicketsPath+"/{id}", h.Get)
	r.Method(http.MethodGet, TicketsPath+"/{id}"+TicketHistorySuffix, h.History)
}

// AgentRoutes mounts the endpoints only an agent may reach.
//
// Separate from Routes because the two carry different guarantees and must be
// mounted in different places: everything here is backed by a query with no
// requester predicate, so the router hands it a chi.Router that already carries
// a role check (docs/adr/0011). A handler added to the wrong function is a
// handler behind the wrong guard, and the two lists being separate is what
// makes that visible.
//
// It still builds its handlers from the module's Service, because the agent
// features have not moved yet. When they do it takes an AgentHandlers value and
// this file stops naming application entirely.
func AgentRoutes(r chi.Router, svc *application.Service, resolve CallerResolver) {
	r.Method(http.MethodGet, QueuePath, QueueTicketsHandler(svc, resolve))
	r.Method(http.MethodGet, QueuePath+"/{id}", AgentTicketHandler(svc, resolve))
	r.Method(http.MethodGet, QueuePath+"/{id}"+TicketHistorySuffix, AgentTicketHistoryHandler(svc, resolve))
	r.Method(http.MethodPatch, QueuePath+"/{id}"+AssigneeSuffix, AssignTicketHandler(svc, resolve))
	r.Method(http.MethodPost, QueuePath+"/{id}"+TransitionsSuffix, TransitionTicketHandler(svc, resolve))
}

// CallerResolver is an alias for the module's own contract, kept so the agent
// handlers that have not moved yet still compile against it.
//
// It disappears with them. The real declaration is in ports, where it belongs:
// what a caller is, is this module's question, and it names no HTTP type.
type CallerResolver = ports.CallerResolver

// Caller is an alias, for the same reason and with the same lifetime.
type Caller = ports.Caller
