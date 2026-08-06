package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// AssigneeSuffix is appended to a ticket's path in the agent group.
const AssigneeSuffix = "/assignee"

// TicketAssigner is the slice of the module this handler needs.
type TicketAssigner interface {
	Assign(ctx context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error)
}

// assignField is the key the body carries. Decoding into a map rather than a
// struct is what makes "absent" and "null" different requests.
//
// Measured rather than assumed, because the obvious two attempts both fail:
// a *uuid.UUID field leaves nil for both, and so does a *json.RawMessage —
// encoding/json sets a pointer to nil when it reads null, whatever it points
// at. A map preserves the difference, because a key that never appeared is
// simply not in it while a null one holds the four bytes.
//
// This matters more than it looks: "take the assignee off" and "I forgot to
// send anything" would otherwise be the same request, and one of them is a
// write. A client sending {} by accident would silently unassign a ticket
// somebody is working on.
const assignField = "assignee_id"

// AssignTicketHandler serves PATCH /api/agent/tickets/{id}/assignee.
//
// PATCH rather than PUT: it changes one field and leaves the rest of the ticket
// alone, which is what PATCH means. The body carries the field explicitly
// rather than the path carrying the assignee, so unassigning is the same
// request shape as assigning.
func AssignTicketHandler(tickets TicketAssigner, resolve CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolve(r.Context()); !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		var body map[string]json.RawMessage
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTicketBody))
		if err := decoder.Decode(&body); err != nil {
			httperr.Write(w, http.StatusBadRequest, "the request body is not valid JSON")
			return
		}

		// Absent is a client mistake, not an unassignment.
		raw, present := body[assignField]
		if !present {
			httperr.WriteValidation(w, map[string]string{
				assignField: "is required — send null to unassign",
			})
			return
		}

		var assignee *uuid.UUID
		if string(raw) != "null" {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				httperr.WriteValidation(w, map[string]string{
					assignField: "must be a user id or null",
				})
				return
			}
			id, err := uuid.Parse(text)
			if err != nil {
				httperr.WriteValidation(w, map[string]string{
					assignField: "must be a user id or null",
				})
				return
			}
			assignee = &id
		}

		updated, err := tickets.Assign(r.Context(), ticketID, assignee)
		switch {
		case errors.Is(err, application.ErrNotAssignable):
			// A field error rather than a 404, because the request is well
			// formed and the id is a real uuid — it just names somebody who
			// cannot hold tickets. The same answer is given for an id that
			// names nobody at all, so this endpoint cannot be used to learn
			// which uuids are users.
			//
			// The planning card said 422. It is 400, like every other
			// validation failure in this API: httperr.WriteValidation is what
			// the frontend's ApiError.fieldErrors already reads (T13), and a
			// second status for the same shape of answer would buy a semantic
			// distinction nobody consumes at the cost of a second code path in
			// the client.
			httperr.WriteValidation(w, map[string]string{
				assignField: "that user may not hold tickets",
			})
			return
		case errors.Is(err, application.ErrTicketNotFound):
			httperr.Write(w, http.StatusNotFound, "no such ticket")
			return
		case err != nil:
			slog.ErrorContext(r.Context(), "assigning a ticket failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, NewTicketResponse(updated))
	})
}
