package history

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

// The agent's read of the same timeline, beside the customer's for the reason
// given in get/agent.go: the scoped query carries a requester in its JOIN and
// this one carries nothing, and the two must be readable side by side.

// AgentTickets is the unscoped read of a ticket's timeline.
//
// It reuses ListTicketStatusHistory, which has never had a requester predicate:
// it is the input to the SLA reconstruction, running inside a transaction that
// has already established which ticket it is working on.
type AgentTickets interface {
	Timeline(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error)
}

// AgentHandler executes the unscoped read.
type AgentHandler struct {
	tickets AgentTickets
}

func NewAgent(tickets AgentTickets) *AgentHandler { return &AgentHandler{tickets: tickets} }

// Handle returns any ticket's full history.
func (h *AgentHandler) Handle(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error) {
	return h.tickets.Timeline(ctx, ticketID)
}

// AgentUseCase is what the agent adapter needs.
type AgentUseCase interface {
	Handle(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error)
}

// AgentHTTP adapts GET /api/agent/tickets/{id}/history onto the unscoped read.
//
// It returns the DTO the customer's timeline uses, so both views of a ticket's
// history say the same thing about it — including the omission that matters:
// the actor's id never travels. It is another user's primary key, and putting
// it on the wire hands out an identifier to enumerate (T14b). An agent gains no
// reason to see one.
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

		rows, err := h.Handle(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket history for an agent failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, tickethttp.NewTicketHistoryResponse(rows))
	})
}
