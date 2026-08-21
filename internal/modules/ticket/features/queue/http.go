package queue

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// PausedPosition is where a ticket with no deadline sorts.
//
// A paused ticket cannot breach (docs/spec.md §4.2), so it belongs after every
// real deadline. The value is finite rather than 'infinity' because the cursor
// travels as a time.Time, which has no infinity — see db/migrations/004.
//
// It is exact in both directions: timestamptz resolves to one microsecond and
// RFC3339Nano writes six digits, so a cursor built from this value compares
// equal to the column expression it came from.
var PausedPosition = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)

// UseCase is what the adapter needs: the one call that reads a page of the
// queue. *Handler satisfies it, and so does a fake.
type UseCase interface {
	Handle(ctx context.Context, f Filter) ([]Entry, error)
}

// HTTP adapts GET /api/agent/tickets onto the use case.
//
// The caller is still resolved, for two reasons that are not authorization:
// assignee=me needs their id, and a request that reached here without one is a
// wiring mistake that must fail closed rather than serve the unscoped set.
func HTTP(h UseCase, resolve ports.CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := resolve(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		query := r.URL.Query()
		params := Filter{Assignee: Any}

		// Every filter is validated before any is rejected, so a request with
		// three bad ones is told about three rather than about the first.
		filterErrs := make(map[string]string)

		if raw, problem := tickethttp.FilterParam(query, "status", tickethttp.ValidStatuses); problem != "" {
			filterErrs["status"] = problem
		} else {
			params.Status = (*domain.Status)(raw)
		}
		if raw, problem := tickethttp.FilterParam(query, "priority", tickethttp.ValidPriorities); problem != "" {
			filterErrs["priority"] = problem
		} else {
			params.Priority = (*domain.Priority)(raw)
		}

		// An unknown assignee value is a 400 rather than an ignored filter. A
		// queue that says it is filtered while showing everything is worse than
		// an error — same rule T14a set for status and priority.
		// "me" is resolved here rather than by the client. The caller already
		// knows who they are, and letting the browser send its own id back
		// would make it a second source of truth for something the server
		// already holds.
		switch raw := query.Get("assignee"); raw {
		case "", "any":
			params.Assignee = Any
		case "unassigned":
			params.Assignee = Unassigned
		case "me":
			params.Assignee = One
			id := caller.ID
			params.AssigneeID = &id
		default:
			id, err := uuid.Parse(raw)
			if err != nil {
				filterErrs["assignee"] = "must be one of any, unassigned, me, or a user id"
				break
			}
			params.Assignee = One
			params.AssigneeID = &id
		}

		if len(filterErrs) > 0 {
			httperr.WriteValidation(w, filterErrs)
			return
		}

		if raw := query.Get("cursor"); raw != "" {
			dueAt, id, err := tickethttp.DecodeCursor(raw)
			if err != nil {
				httperr.Write(w, http.StatusBadRequest, "the cursor is not one this API issued")
				return
			}
			params.AfterDueAt = &dueAt
			params.AfterID = &id
		}

		// One more row than asked for, so "is there another page" is answered
		// without counting the table.
		limit := tickethttp.PageSize(query.Get("limit"))
		params.PageSize = limit + 1

		rows, err := h.Handle(r.Context(), params)
		if err != nil {
			slog.ErrorContext(r.Context(), "reading the agent queue failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		var next *string
		if int32(len(rows)) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1].Ticket
			// The cursor carries the position in the sort order, which for a
			// paused ticket is the sentinel and not its absent deadline. A
			// cursor built from NULL would make the next page's row comparison
			// NULL, and every paused ticket would silently disappear from it.
			cursor := tickethttp.EncodeCursor(position(last.SLADueAt), last.ID)
			next = &cursor
		}

		out := make([]EntryResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, NewEntryResponse(row))
		}

		httpx.WriteJSON(w, r, http.StatusOK, Response{Tickets: out, NextCursor: next})
	})
}

// position is where a ticket sorts: its deadline, or the paused position when
// it has none.
func position(dueAt *time.Time) time.Time {
	if dueAt == nil {
		return PausedPosition
	}
	return *dueAt
}
