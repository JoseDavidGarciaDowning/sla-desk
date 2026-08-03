package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/auth"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
)

// TicketsPath is the collection endpoint.
const TicketsPath = "/api/tickets"

// maxTicketBody caps the request body. The description is bounded at 10000
// characters, so anything approaching this is not a ticket.
const maxTicketBody = 64 << 10 // 64 KiB

// TicketCreator is the slice of the store this handler needs. Declared by the
// consumer, per docs/spec.md §8; *store.TicketRepo satisfies it.
type TicketCreator interface {
	Create(ctx context.Context, in store.NewTicket) (store.Ticket, error)
}

// CreateTicketHandler serves POST /api/tickets.
//
// It must be mounted behind RequireAuth. The requester is read from the request
// context and never from the body — docs/spec.md §4.3 — and CreateTicketRequest
// has no field one could arrive in anyway.
func CreateTicketHandler(tickets TicketCreator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.UserFromContext(r.Context())
		if !ok {
			// Only reachable if this route is mounted without RequireAuth in
			// front of it, which is a wiring mistake rather than a bad request.
			slog.ErrorContext(r.Context(), "ticket creation reached without an authenticated caller",
				"path", r.URL.Path)
			WriteProblem(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req CreateTicketRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, maxTicketBody)).Decode(&req); err != nil {
			WriteProblem(w, http.StatusBadRequest, "the request body is not valid JSON")
			return
		}

		if errs := req.Validate(); len(errs) > 0 {
			WriteValidationProblem(w, errs)
			return
		}
		req = req.Normalised()

		created, err := tickets.Create(r.Context(), store.NewTicket{
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
			if errors.Is(err, store.ErrNoPolicyForPriority) {
				slog.ErrorContext(r.Context(), "no active SLA policy serves a supported priority",
					"priority", req.Priority, "error", err)
			} else {
				slog.ErrorContext(r.Context(), "creating a ticket failed", "error", err)
			}
			WriteInternalProblem(w)
			return
		}

		w.Header().Set("Location", TicketsPath+"/"+uuidString(created.ID))
		writeJSON(w, r, http.StatusCreated, NewTicketResponse(created))
	})
}
