package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// QueuePath is the agent queue, mounted under the agent prefix by the
// composition root. It is not reachable from anywhere else, and that placement
// is the authorization: the query behind it has no requester predicate.
const QueuePath = "/tickets"

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

// QueueReader is the slice of the module this handler needs.
type QueueReader interface {
	Queue(ctx context.Context, f application.QueueFilter) ([]application.QueueEntry, error)
}

// QueueTicketsHandler serves GET /api/agent/tickets.
//
// It reads every ticket. There is no requester predicate in the query and none
// here either, which is the decision recorded in tasks/slice-2/plan.md §B: the
// alternative was passing the caller's role into the customer's query and
// skipping its predicate for agents, and a boolean in charge of a security
// predicate is a boolean that can be wrong.
//
// The caller is still resolved, for two reasons that are not authorization:
// assignee=me needs their id, and a request that reached here without one is a
// wiring mistake that must fail closed rather than serve the unscoped set.
func QueueTicketsHandler(queue QueueReader, resolve CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := resolve(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		query := r.URL.Query()
		params := application.QueueFilter{Assignee: application.AssigneeAny}

		// Every filter is validated before any is rejected, so a request with
		// three bad ones is told about three rather than about the first.
		filterErrs := make(map[string]string)

		if raw, problem := filterParam(query, "status", validStatuses); problem != "" {
			filterErrs["status"] = problem
		} else {
			params.Status = (*domain.Status)(raw)
		}
		if raw, problem := filterParam(query, "priority", validPriorities); problem != "" {
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
			params.Assignee = application.AssigneeAny
		case "unassigned":
			params.Assignee = application.AssigneeUnassigned
		case "me":
			params.Assignee = application.AssigneeOne
			id := caller.ID
			params.AssigneeID = &id
		default:
			id, err := uuid.Parse(raw)
			if err != nil {
				filterErrs["assignee"] = "must be one of any, unassigned, me, or a user id"
				break
			}
			params.Assignee = application.AssigneeOne
			params.AssigneeID = &id
		}

		if len(filterErrs) > 0 {
			httperr.WriteValidation(w, filterErrs)
			return
		}

		if raw := query.Get("cursor"); raw != "" {
			dueAt, id, err := decodeCursor(raw)
			if err != nil {
				httperr.Write(w, http.StatusBadRequest, "the cursor is not one this API issued")
				return
			}
			params.AfterDueAt = &dueAt
			params.AfterID = &id
		}

		// One more row than asked for, so "is there another page" is answered
		// without counting the table.
		limit := pageSize(query.Get("limit"))
		params.PageSize = limit + 1

		rows, err := queue.Queue(r.Context(), params)
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
			cursor := encodeCursor(queuePosition(last.SLADueAt), last.ID)
			next = &cursor
		}

		out := make([]QueueEntryResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, NewQueueEntryResponse(row))
		}

		httpx.WriteJSON(w, r, http.StatusOK, QueueResponse{Tickets: out, NextCursor: next})
	})
}

// queuePosition is where a ticket sorts: its deadline, or the paused position
// when it has none.
func queuePosition(dueAt *time.Time) time.Time {
	if dueAt == nil {
		return PausedPosition
	}
	return *dueAt
}

// AgentTicketReader is the slice of the module the agent detail endpoints need.
//
// Neither method takes a caller, which is the shape of the guarantee: these
// reads do not depend on who is asking, so the route they hang off has to be
// the one that decides who may ask.
type AgentTicketReader interface {
	Detail(ctx context.Context, id uuid.UUID) (domain.Ticket, error)
	Timeline(ctx context.Context, ticketID uuid.UUID) ([]domain.HistoryEntry, error)
}

// AgentTicketHandler serves GET /api/agent/tickets/{id}.
//
// 404 for an id that names no ticket, unchanged from the customer's endpoint.
// The agent group answers 403 to a customer because its path carries no id to
// confirm; an id that names nothing is a different question, and docs/spec.md
// §11's rule applies to it exactly as before.
func AgentTicketHandler(tickets AgentTicketReader, resolve CallerResolver) http.Handler {
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

		row, err := tickets.Detail(r.Context(), id)
		if err != nil {
			if errors.Is(err, application.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket for an agent failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, NewAgentTicketResponse(row))
	})
}

// AgentTicketHistoryHandler serves GET /api/agent/tickets/{id}/history.
//
// It reuses the DTO the customer's timeline uses, so both views of a ticket's
// history say the same thing about it — including the omission that matters:
// the actor's id never travels. It is another user's primary key, and putting
// it on the wire hands out an identifier to enumerate (T14b). An agent gains no
// reason to see one in slice 2.
func AgentTicketHistoryHandler(tickets AgentTicketReader, resolve CallerResolver) http.Handler {
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

		rows, err := tickets.Timeline(r.Context(), id)
		if err != nil {
			if errors.Is(err, application.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket history for an agent failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, NewTicketHistoryResponse(rows))
	})
}
