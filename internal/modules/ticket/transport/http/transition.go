package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// maxReasonLength bounds the note an agent may attach. It is not a comment —
// comments are slice 4 — so it is short on purpose.
const maxReasonLength = 500

// TicketTransitioner is the slice of the module this handler needs.
type TicketTransitioner interface {
	Transition(ctx context.Context, in application.StatusChange) (domain.Ticket, error)
}

type transitionRequest struct {
	To     string  `json:"to"`
	Reason *string `json:"reason"`
}

// TransitionTicketHandler serves POST /api/agent/tickets/{id}/transitions.
//
// It adds no write logic. Service.Transition has resolved the clock before the
// transaction, taken FOR UPDATE, written the history row, rebuilt the clock
// from it and updated the cache since T11 — all of it mutation-tested, and all
// of it reachable only from integration tests until now. This is the surface
// that was missing.
//
// It is where the SLA clock can be paused and resumed over HTTP for the first
// time, which is the headline behaviour of the whole domain.
func TransitionTicketHandler(tickets TicketTransitioner, resolve CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := resolve(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		var body transitionRequest
		if err := httpx.DecodeJSON(w, r, &body, MaxBodyBytes); err != nil {
			httperr.Write(w, http.StatusBadRequest, "the request body is not valid JSON")
			return
		}

		fieldErrs := make(map[string]string)

		target := domain.Status(body.To)
		if body.To == "" {
			fieldErrs["to"] = "is required"
		} else if !slices.Contains(ValidStatuses, target) {
			// Rejected here rather than passed down, so an unknown word never
			// reaches the state machine. The domain would refuse it too, but as
			// "you cannot go from open to blorp", which reads like an edge that
			// might exist elsewhere.
			fieldErrs["to"] = "must be one of " + statusList()
		}

		if body.Reason != nil {
			if trimmed := strings.TrimSpace(*body.Reason); len(trimmed) > maxReasonLength {
				fieldErrs["reason"] = "is too long"
			} else if trimmed == "" {
				// A reason of spaces is not a reason. Storing it would put a
				// blank line in a timeline an agent reads.
				body.Reason = nil
			} else {
				body.Reason = &trimmed
			}
		}

		if len(fieldErrs) > 0 {
			httperr.WriteValidation(w, fieldErrs)
			return
		}

		updated, err := tickets.Transition(r.Context(), application.StatusChange{
			TicketID:  ticketID,
			Target:    target,
			ActorID:   caller.ID,
			ActorRole: caller.Role,
			Reason:    body.Reason,
		})
		switch {
		case errors.Is(err, domain.ErrForbidden):
			// 403, and deliberately not the same answer as an impossible edge.
			// The domain already tells them apart — "this move does not exist"
			// versus "it exists and is not yours to make" — and flattening them
			// would tell an agent their client is broken when the truth is that
			// somebody else has to do it.
			httperr.Write(w, http.StatusForbidden, "your role may not make that transition")
			return
		case errors.Is(err, domain.ErrInvalidTransition):
			// A field error rather than the 422 the planning card named, for
			// the same reason assignment is a 400: every validation failure in
			// this API carries an errors member the frontend already reads.
			//
			// A closed ticket lands here. It is terminal (docs/spec.md §4.1),
			// so no edge leaves it for any role, and it needs no special case.
			httperr.WriteValidation(w, map[string]string{
				"to": "that transition is not possible from this ticket's status",
			})
			return
		case errors.Is(err, domain.ErrTicketNotFound):
			httperr.Write(w, http.StatusNotFound, "no such ticket")
			return
		case err != nil:
			slog.ErrorContext(r.Context(), "transitioning a ticket failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		// The updated ticket, so a client needs no follow-up read to see the
		// new deadline — which is the whole point of the request for a pause.
		httpx.WriteJSON(w, r, http.StatusOK, NewAgentTicketResponse(updated))
	})
}

func statusList() string {
	out := make([]string, len(ValidStatuses))
	for i, s := range ValidStatuses {
		out[i] = string(s)
	}
	return strings.Join(out, ", ")
}
