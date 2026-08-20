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

// UseCase is what the adapter needs: the one call that executes one of the caller's timelines.
//
// An interface rather than *Handler, declared here by the consumer, for the
// reason docs/spec.md §8 gives generally and one specific to a transport: a
// test of the HTTP behaviour — the status codes, the shapes, what is read from
// the request — should not have to build the use case's own collaborators to
// get at it. *Handler satisfies this, and so does a fake.
type UseCase interface {
	Handle(ctx context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error)
}

// HTTP adapts GET /api/tickets/{id}/history onto the use case. Mount behind
// RequireAuth.
func HTTP(h UseCase, resolve ports.CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := resolve(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		rows, err := h.Handle(r.Context(), id, caller.ID)
		if err != nil {
			if errors.Is(err, domain.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket history failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		if len(rows) == 0 {
			httperr.Write(w, http.StatusNotFound, "no such ticket")
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, tickethttp.NewTicketHistoryResponse(rows))
	})
}
