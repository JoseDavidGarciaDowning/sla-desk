package list

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// UseCase is what the adapter needs: the one call that executes a page of the caller's tickets.
//
// An interface rather than *Handler, declared here by the consumer, for the
// reason docs/spec.md §8 gives generally and one specific to a transport: a
// test of the HTTP behaviour — the status codes, the shapes, what is read from
// the request — should not have to build the use case's own collaborators to
// get at it. *Handler satisfies this, and so does a fake.
type UseCase interface {
	Handle(ctx context.Context, f Filter) ([]domain.Ticket, error)
}

// HTTP adapts GET /api/tickets onto the use case. Mount behind RequireAuth.
func HTTP(h UseCase, resolve ports.CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := resolve(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		query := r.URL.Query()
		params := Filter{RequesterID: caller.ID}

		// Both filters are read before either is rejected, so a request with
		// two bad ones is told about two rather than about the first.
		filterErrs := make(map[string]string)
		var problem string
		var raw *string
		if raw, problem = tickethttp.FilterParam(query, "status", tickethttp.ValidStatuses); problem != "" {
			filterErrs["status"] = problem
		} else {
			params.Status = (*domain.Status)(raw)
		}
		if raw, problem = tickethttp.FilterParam(query, "priority", tickethttp.ValidPriorities); problem != "" {
			filterErrs["priority"] = problem
		} else {
			params.Priority = (*domain.Priority)(raw)
		}
		if len(filterErrs) > 0 {
			httperr.WriteValidation(w, filterErrs)
			return
		}

		if raw := query.Get("cursor"); raw != "" {
			createdAt, id, err := tickethttp.DecodeCursor(raw)
			if err != nil {
				httperr.Write(w, http.StatusBadRequest, "the cursor is not one this API issued")
				return
			}
			params.AfterCreatedAt = &createdAt
			params.AfterID = &id
		}

		// One more row than asked for. If it comes back, there is another page,
		// and that is cheaper to learn than by counting the table.
		limit := tickethttp.PageSize(query.Get("limit"))
		params.PageSize = limit + 1

		rows, err := h.Handle(r.Context(), params)
		if err != nil {
			slog.ErrorContext(r.Context(), "listing tickets failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		var next *string
		if int32(len(rows)) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			cursor := tickethttp.EncodeCursor(last.CreatedAt, last.ID)
			next = &cursor
		}

		// Built with make so an empty page encodes as [] rather than null, and
		// no client has to write a nil check for it.
		out := make([]tickethttp.TicketResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, tickethttp.NewTicketResponse(row))
		}

		httpx.WriteJSON(w, r, http.StatusOK, Response{Tickets: out, NextCursor: next})
	})
}
