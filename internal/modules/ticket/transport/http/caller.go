package http

import (
	"context"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Caller is who is making the request, as this module needs to know them.
//
// Deliberately not the identity module's User. This module does not know that
// module exists: it says what it needs — an id to scope reads by, and a role to
// check transitions against — and the composition root is responsible for
// producing one. That contract is what keeps the two independently evolvable
// (docs/adr/0005).
//
// The role is the ticket module's own, not identity's. They hold the same three
// strings and answer different questions: what someone *is*, versus what they
// *were when they acted*. The conversion happens once, in internal/app.
type Caller struct {
	ID   uuid.UUID
	Role domain.Role
}

// CallerResolver reads the authenticated caller out of a request context.
//
// A function rather than an interface: it has one method, and a test can supply
// one inline without declaring a stub type. It returns false when the request
// did not pass through authentication, which is a wiring mistake rather than a
// bad request — and every handler answers 401 on it, so a route mounted outside
// the authenticated group fails closed.
type CallerResolver func(ctx context.Context) (Caller, bool)

// Routes mounts every ticket endpoint.
//
// All of them must sit behind authentication — every handler reads the caller
// from the request context and answers 401 without one. This function does not
// decide that: it is given a chi.Router that already carries the authentication
// middleware, so an endpoint cannot be added to the wrong group by forgetting
// to. See internal/app.
// AgentRoutes mounts the endpoints only an agent may reach.
//
// Separate from Routes because the two carry different guarantees and must be
// mounted in different places: everything here is backed by a query with no
// requester predicate, so the router hands it a chi.Router that already carries
// a role check (tasks/slice-2/plan.md decision C). A handler added to the wrong
// function is a handler behind the wrong guard, and the two lists being
// separate is what makes that visible.
func AgentRoutes(r chi.Router, svc *application.Service, resolve CallerResolver) {
	r.Method(nethttp.MethodGet, QueuePath, QueueTicketsHandler(svc, resolve))
	r.Method(nethttp.MethodGet, QueuePath+"/{id}", AgentTicketHandler(svc, resolve))
	r.Method(nethttp.MethodGet, QueuePath+"/{id}"+TicketHistorySuffix, AgentTicketHistoryHandler(svc, resolve))
}

func Routes(r chi.Router, svc *application.Service, resolve CallerResolver) {
	r.Method(nethttp.MethodPost, TicketsPath, CreateTicketHandler(svc, resolve))
	r.Method(nethttp.MethodGet, TicketsPath, ListTicketsHandler(svc, resolve))
	r.Method(nethttp.MethodGet, TicketsPath+"/{id}", GetTicketHandler(svc, resolve))
	r.Method(nethttp.MethodGet, TicketsPath+"/{id}/history", GetTicketHistoryHandler(svc, resolve))
}
