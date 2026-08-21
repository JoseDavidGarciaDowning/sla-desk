package create

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// UseCase is what the adapter needs: the one call that executes Command, and returns the ticket it opened.
//
// An interface rather than *Handler, declared here by the consumer, for the
// reason docs/spec.md §8 gives generally and one specific to a transport: a
// test of the HTTP behaviour — the status codes, the shapes, what is read from
// the request — should not have to build the use case's own collaborators to
// get at it. *Handler satisfies this, and so does a fake.
type UseCase interface {
	Handle(ctx context.Context, cmd Command) (domain.Ticket, error)
}

// HTTP adapts POST /api/tickets onto the use case.
//
// It must be mounted behind RequireAuth. The requester is read from the request
// context and never from the body — docs/spec.md §4.3 — and Request has no
// field one could arrive in anyway.
func HTTP(h UseCase, resolve ports.CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := resolve(r.Context())
		if !ok {
			// Only reachable if this route is mounted without RequireAuth in
			// front of it, which is a wiring mistake rather than a bad request.
			slog.ErrorContext(r.Context(), "ticket creation reached without an authenticated caller",
				"path", r.URL.Path)
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req Request
		if err := httpx.DecodeJSON(w, r, &req, tickethttp.MaxBodyBytes); err != nil {
			httperr.Write(w, http.StatusBadRequest, "the request body is not valid JSON")
			return
		}

		if errs := req.Validate(); len(errs) > 0 {
			httperr.WriteValidation(w, errs)
			return
		}
		req = req.Normalised()

		created, err := h.Handle(r.Context(), Command{
			RequesterID: caller.ID,
			ActorRole:   caller.Role,
			Title:       req.Title,
			Description: req.Description,
			Category:    req.Category,
			Priority:    req.Priority,
		})
		if err != nil {
			// A priority with no policy is our seed being wrong, not the
			// caller's request: validation has already established that the
			// priority is one of the four the system supports.
			if errors.Is(err, domain.ErrNoSLAPolicy) {
				slog.ErrorContext(r.Context(), "no active SLA policy serves a supported priority",
					"priority", req.Priority, "error", err)
			} else {
				slog.ErrorContext(r.Context(), "creating a ticket failed", "error", err)
			}
			httperr.WriteInternal(w)
			return
		}

		w.Header().Set("Location", tickethttp.TicketsPath+"/"+created.ID.String())
		httpx.WriteJSON(w, r, http.StatusCreated, tickethttp.NewTicketResponse(created))
	})
}
