package get

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// The agent's read of the same ticket, in the same package as the customer's
// and deliberately not in one of its own.
//
// They answer one question — "show me this ticket" — under two different
// guarantees, and the difference is the whole point: handler.go's query carries
// WHERE requester_id = $1, and this one carries nothing. Two directories would
// put those twelve lines apart in a listing; two files put them next to each
// other, where a reader comparing them cannot miss which is which.
//
// What is *not* shared is a method with an optional requester. One call that
// took a nil requester to mean "unscoped" would let a caller ask the wrong
// question by leaving a field unset, which is exactly the failure the two
// separate reads exist to make impossible.

// AgentTickets is the unscoped read.
//
// It takes no requester — not an optional one, none — and that absence is the
// contract. Authorization for it lives in where the route is mounted
// (docs/adr/0011), because there is no predicate here to carry it.
type AgentTickets interface {
	OneByID(ctx context.Context, id uuid.UUID) (domain.Ticket, error)
}

// AgentHandler executes the unscoped read.
type AgentHandler struct {
	tickets AgentTickets
}

func NewAgent(tickets AgentTickets) *AgentHandler { return &AgentHandler{tickets: tickets} }

// Handle returns any ticket, or domain.ErrTicketNotFound.
//
// It takes no caller: who is asking changes nothing about the answer. That is
// what makes this the unscoped read, and why the route it hangs off is the one
// carrying the role check.
func (h *AgentHandler) Handle(ctx context.Context, id uuid.UUID) (domain.Ticket, error) {
	return h.tickets.OneByID(ctx, id)
}

// AgentUseCase is what the agent adapter needs.
type AgentUseCase interface {
	Handle(ctx context.Context, id uuid.UUID) (domain.Ticket, error)
}

// AgentHTTP adapts GET /api/agent/tickets/{id} onto the unscoped read.
//
// 404 for an id that names no ticket, unchanged from the customer's endpoint.
// The agent group answers 403 to a customer because its path carries no id to
// confirm; an id that names nothing is a different question, and docs/spec.md
// §11's rule applies to it exactly as before.
func AgentHTTP(h AgentUseCase, resolve ports.CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolve(r.Context()); !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		row, err := h.Handle(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket for an agent failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, tickethttp.NewAgentTicketResponse(row))
	})
}
