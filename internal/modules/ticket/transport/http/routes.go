package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
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

// AgentHandlers are the module's agent endpoints, already built, for the same
// reason.
type AgentHandlers struct {
	Queue      http.Handler
	Get        http.Handler
	History    http.Handler
	Assign     http.Handler
	Transition http.Handler
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
// The two lists are also the whole reason this file exists. Every endpoint's
// code lives in its own feature; what cannot live there is the answer to "which
// of these is behind the role check", because that is a fact about the set.
func AgentRoutes(r chi.Router, h AgentHandlers) {
	r.Method(http.MethodGet, QueuePath, h.Queue)
	r.Method(http.MethodGet, QueuePath+"/{id}", h.Get)
	r.Method(http.MethodGet, QueuePath+"/{id}"+TicketHistorySuffix, h.History)
	r.Method(http.MethodPatch, QueuePath+"/{id}"+AssigneeSuffix, h.Assign)
	r.Method(http.MethodPost, QueuePath+"/{id}"+TransitionsSuffix, h.Transition)
}
