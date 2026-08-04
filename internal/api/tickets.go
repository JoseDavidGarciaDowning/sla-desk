package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/httperr"
	ticketapp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	ticketdomain "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// TicketsPath is the collection endpoint.
const TicketsPath = "/api/tickets"

// maxTicketBody caps the request body. The description is bounded at 10000
// characters, so anything approaching this is not a ticket.
const maxTicketBody = 64 << 10 // 64 KiB

// TicketCreator is the slice of the store this handler needs. Declared by the
// consumer, per docs/spec.md §8; the ticket module's Service satisfies it.
type TicketCreator interface {
	Create(ctx context.Context, in ticketapp.NewTicket) (ticketdomain.Ticket, error)
}

// CreateTicketHandler serves POST /api/tickets.
//
// It must be mounted behind RequireAuth. The requester is read from the request
// context and never from the body — docs/spec.md §4.3 — and CreateTicketRequest
// has no field one could arrive in anyway.
func CreateTicketHandler(tickets TicketCreator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := callerFromContext(r.Context())
		if !ok {
			// Only reachable if this route is mounted without RequireAuth in
			// front of it, which is a wiring mistake rather than a bad request.
			slog.ErrorContext(r.Context(), "ticket creation reached without an authenticated caller",
				"path", r.URL.Path)
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req CreateTicketRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, maxTicketBody)).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "the request body is not valid JSON")
			return
		}

		if errs := req.Validate(); len(errs) > 0 {
			httperr.WriteValidation(w, errs)
			return
		}
		req = req.Normalised()

		created, err := tickets.Create(r.Context(), ticketapp.NewTicket{
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
			if errors.Is(err, ticketapp.ErrNoSLAPolicy) {
				slog.ErrorContext(r.Context(), "no active SLA policy serves a supported priority",
					"priority", req.Priority, "error", err)
			} else {
				slog.ErrorContext(r.Context(), "creating a ticket failed", "error", err)
			}
			httperr.WriteInternal(w)
			return
		}

		w.Header().Set("Location", TicketsPath+"/"+created.ID.String())
		writeJSON(w, r, http.StatusCreated, NewTicketResponse(created))
	})
}

// Page sizes. The default keeps a first render small; the maximum stops a
// caller asking for the whole table in one request.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// TicketListResponse is one page of tickets.
//
// NextCursor is null on the last page. It is opaque on purpose: a client that
// parses it becomes coupled to the sort key, and changing the ordering would
// then be a breaking change.
type TicketListResponse struct {
	Tickets    []TicketResponse `json:"tickets"`
	NextCursor *string          `json:"next_cursor"`
}

// TicketReader is the slice of the store the read endpoints need.
type TicketReader interface {
	List(ctx context.Context, f ticketapp.ListFilter) ([]ticketdomain.Ticket, error)
	Get(ctx context.Context, id, requesterID uuid.UUID) (ticketdomain.Ticket, error)
	History(ctx context.Context, ticketID, requesterID uuid.UUID) ([]ticketdomain.HistoryEntry, error)
}

// encodeCursor packs the sort key of the last row on a page.
func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

var errBadCursor = errors.New("api: cursor is not readable")

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}

	createdAt, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}

	at, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}

	parsed, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	return at, parsed, nil
}

// pageSize reads ?limit=, clamping anything absent, unreadable or out of range
// to something serviceable rather than rejecting it. A caller asking for 1000
// wants "as many as I can have".
func pageSize(raw string) int32 {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultPageSize
	}
	return int32(min(n, MaxPageSize))
}

// filterParam reads an optional ?name= filter, checking it against the
// vocabulary the API actually supports.
//
// Absent and empty mean the same thing — no filter. Empty matters because that
// is what a form submits for "any", and treating it as a value would ask the
// database for tickets whose status is the empty string and return none.
//
// A value outside the vocabulary is refused rather than ignored. Ignoring it
// always returns something, but it returns the wrong thing silently: a
// bookmarked ?status=opne would show every ticket while the page said the list
// was filtered. The message names what is allowed, so the client does not have
// to hold a copy of the list to explain the failure.
func filterParam[T ~string](q url.Values, name string, valid []T) (*string, string) {
	raw := q.Get(name)
	if raw == "" {
		return nil, ""
	}

	if !slices.Contains(valid, T(raw)) {
		return nil, "must be one of " + join(valid)
	}
	return &raw, ""
}

// ListTicketsHandler serves GET /api/tickets.
//
// Mount behind RequireAuth. The scope is the query's WHERE clause, so this
// handler has no ownership check to forget: another customer's row never
// arrives to be filtered out — and that stays true with filters applied,
// because they are further predicates on the same query rather than a
// replacement for it.
func ListTicketsHandler(tickets TicketReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := callerFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		query := r.URL.Query()
		params := ticketapp.ListFilter{RequesterID: caller.ID}

		// Both filters are read before either is rejected, so a request with
		// two bad ones is told about two rather than about the first.
		filterErrs := make(map[string]string)
		var problem string
		var raw *string
		if raw, problem = filterParam(query, "status", validStatuses); problem != "" {
			filterErrs["status"] = problem
		} else {
			params.Status = (*ticketdomain.Status)(raw)
		}
		if raw, problem = filterParam(query, "priority", validPriorities); problem != "" {
			filterErrs["priority"] = problem
		} else {
			params.Priority = (*ticketdomain.Priority)(raw)
		}
		if len(filterErrs) > 0 {
			httperr.WriteValidation(w, filterErrs)
			return
		}

		if raw := query.Get("cursor"); raw != "" {
			createdAt, id, err := decodeCursor(raw)
			if err != nil {
				httperr.Write(w, http.StatusBadRequest, "the cursor is not one this API issued")
				return
			}
			params.AfterCreatedAt = &createdAt
			params.AfterID = &id
		}

		// One more row than asked for. If it comes back, there is another page,
		// and that is cheaper to learn than by counting the table.
		limit := pageSize(query.Get("limit"))
		params.PageSize = limit + 1

		rows, err := tickets.List(r.Context(), params)
		if err != nil {
			slog.ErrorContext(r.Context(), "listing tickets failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		var next *string
		if int32(len(rows)) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			cursor := encodeCursor(last.CreatedAt, last.ID)
			next = &cursor
		}

		// Built with make so an empty page encodes as [] rather than null, and
		// no client has to write a nil check for it.
		out := make([]TicketResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, NewTicketResponse(row))
		}

		writeJSON(w, r, http.StatusOK, TicketListResponse{Tickets: out, NextCursor: next})
	})
}

// GetTicketHandler serves GET /api/tickets/{id}.
//
// A ticket belonging to someone else answers 404, never 403 (docs/spec.md §11).
// A 403 would confirm that the id names a real ticket, which is exactly what
// the caller must not be able to learn. The query returns no rows for both
// cases, so the handler cannot tell them apart either.
func GetTicketHandler(tickets TicketReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := callerFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		row, err := tickets.Get(r.Context(), id, caller.ID)
		if err != nil {
			// The module reports "not yours" and "does not exist" as the same
			// error, on purpose: a 403 would confirm that an id names a real
			// ticket (docs/spec.md §11).
			if errors.Is(err, ticketapp.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		writeJSON(w, r, http.StatusOK, NewTicketResponse(row))
	})
}

// TicketHistorySuffix is appended to a ticket's path.
const TicketHistorySuffix = "/history"

// GetTicketHistoryHandler serves GET /api/tickets/{id}/history.
//
// Mount behind RequireAuth. The requester predicate lives in the query's JOIN,
// so this handler has no ownership check to forget.
//
// An empty result is a 404, not an empty timeline. Every ticket has at least
// the entry recording its creation, so nothing comes back only when the ticket
// does not exist or is not the caller's — and the query cannot tell those
// apart, which is exactly why this can answer 404 for both without confirming
// that the id names a real ticket (docs/spec.md §11).
func GetTicketHistoryHandler(tickets TicketReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := callerFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		rows, err := tickets.History(r.Context(), id, caller.ID)
		if err != nil {
			if errors.Is(err, ticketapp.ErrTicketNotFound) {
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

		writeJSON(w, r, http.StatusOK, NewTicketHistoryResponse(rows))
	})
}
