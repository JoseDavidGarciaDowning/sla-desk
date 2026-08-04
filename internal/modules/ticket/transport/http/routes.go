package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
)

// Routes mounts every ticket endpoint.
//
// All of them must be mounted behind authentication — every handler reads the
// caller from the request context and answers 401 without one. The router does
// not decide that here: it is given a chi.Router that already carries the
// authentication middleware, so a route cannot be added to the wrong group by
// forgetting something. See internal/app.
func Routes(r chi.Router, svc *application.Service, caller CallerResolver) {
	r.Method(http.MethodPost, TicketsPath, CreateTicketHandler(svc, caller))
	r.Method(http.MethodGet, TicketsPath, ListTicketsHandler(svc, caller))
	r.Method(http.MethodGet, TicketsPath+"/{id}", GetTicketHandler(svc, caller))
	r.Method(http.MethodGet, TicketsPath+"/{id}"+TicketHistorySuffix, GetTicketHistoryHandler(svc, caller))
}
