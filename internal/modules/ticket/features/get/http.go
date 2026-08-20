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

// UseCase is what the adapter needs: the one call that executes one of the caller's tickets.
//
// An interface rather than *Handler, declared here by the consumer, for the
// reason docs/spec.md §8 gives generally and one specific to a transport: a
// test of the HTTP behaviour — the status codes, the shapes, what is read from
// the request — should not have to build the use case's own collaborators to
// get at it. *Handler satisfies this, and so does a fake.
type UseCase interface {
	Handle(ctx context.Context, id, requesterID uuid.UUID) (domain.Ticket, error)
}

// HTTP adapts GET /api/tickets/{id} onto the use case. Mount behind
// RequireAuth.
//
// There is no response type in this package: what comes back is the module's
// shared TicketResponse, which create and list also return. A wrapper declared
// here would be a second name for one shape.
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

		row, err := h.Handle(r.Context(), id, caller.ID)
		if err != nil {
			// The module reports "not yours" and "does not exist" as the same
			// error, on purpose: a 403 would confirm that an id names a real
			// ticket (docs/spec.md §11).
			if errors.Is(err, domain.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, tickethttp.NewTicketResponse(row))
	})
}
