package http

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

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httperr"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/platform/httpx"
)

// Caller is who is making the request, as this module needs to know them.
//
// Deliberately not the identity module's User. This module does not know that
// module exists: it says what it needs — an id to scope by and a role to check
// transitions against — and internal/app is responsible for producing one. That
// is the contract that keeps the two independently evolvable (docs/adr/0005).
type Caller struct {
	ID   uuid.UUID
	Role domain.ActorRole
}

// CallerResolver reads the authenticated caller out of a request context.
//
// A function rather than an interface: it has one method, and a test can supply
// one inline without a stub type. It returns false when the request did not
// pass through authentication, which is a wiring mistake rather than a bad
// request.
type CallerResolver func(ctx context.Context) (Caller, bool)

// TicketsPath is the collection endpoint.
const TicketsPath = "/api/tickets"

// maxTicketBody caps the request body. The description is bounded at 10000
// characters, so anything approaching this is not a ticket.
const maxTicketBody = 64 << 10 // 64 KiB

// CreateTicketHandler serves POST /api/tickets.
//
// It must be mounted behind RequireAuth. The requester is read from the request
// context and never from the body — docs/spec.md §4.3 — and CreateTicketRequest
// has no field one could arrive in anyway.
func CreateTicketHandler(svc *application.Service, caller CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := caller(r.Context())
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

		created, err := svc.Create(r.Context(), application.NewTicket{
			RequesterID: who.ID,
			ActorRole:   who.Role,
			Title:       req.Title,
			Description: req.Description,
			Category:    req.Category,
			Priority:    req.Priority,
		})
		if err != nil {
			// A priority with no policy is our seed being wrong, not the
			// caller's request: validation has already established that the
			// priority is one of the four the system supports.
			if errors.Is(err, application.ErrNoSLAPolicy) {
				slog.ErrorContext(r.Context(), "no active SLA policy serves a supported priority",
					"priority", req.Priority, "error", err)
			} else {
				slog.ErrorContext(r.Context(), "creating a ticket failed", "error", err)
			}
			httperr.WriteInternal(w)
			return
		}

		w.Header().Set("Location", TicketsPath+"/"+created.ID.String())
		httpx.WriteJSON(w, r, http.StatusCreated, NewTicketResponse(created))
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

// encodeCursor packs the sort key of the last row on a page.
func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

var errBadCursor = errors.New("api: cursor is not readable")

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, errBadCursor
	}
	at, rest, found := strings.Cut(string(raw), "|")
	if !found {
		return time.Time{}, uuid.Nil, errBadCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, uuid.Nil, errBadCursor
	}
	id, err := uuid.Parse(rest)
	if err != nil {
		return time.Time{}, uuid.Nil, errBadCursor
	}
	return createdAt, id, nil
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
func filterParam[T ~string](q url.Values, name string, valid []T) (*T, string) {
	raw := q.Get(name)
	if raw == "" {
		return nil, ""
	}

	if !slices.Contains(valid, T(raw)) {
		return nil, "must be one of " + join(valid)
	}
	value := T(raw)
	return &value, ""
}

// ListTicketsHandler serves GET /api/tickets.
//
// Mount behind RequireAuth. The scope is the query's WHERE clause, so this
// handler has no ownership check to forget: another customer's row never
// arrives to be filtered out — and that stays true with filters applied,
// because they are further predicates on the same query rather than a
// replacement for it.
func ListTicketsHandler(svc *application.Service, caller CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := caller(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		query := r.URL.Query()
		filter := application.ListFilter{RequesterID: who.ID}

		// Both filters are read before either is rejected, so a request with
		// two bad ones is told about two rather than about the first.
		filterErrs := make(map[string]string)
		var problem string
		if filter.Status, problem = filterParam(query, "status", validStatuses); problem != "" {
			filterErrs["status"] = problem
		}
		if filter.Priority, problem = filterParam(query, "priority", validPriorities); problem != "" {
			filterErrs["priority"] = problem
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
			filter.AfterCreatedAt = &createdAt
			filter.AfterID = &id
		}

		// One more row than asked for. If it comes back, there is another page,
		// and that is cheaper to learn than by counting the table.
		limit := pageSize(query.Get("limit"))
		filter.PageSize = limit + 1

		rows, err := svc.List(r.Context(), filter)
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

		httpx.WriteJSON(w, r, http.StatusOK, TicketListResponse{Tickets: out, NextCursor: next})
	})
}

// GetTicketHandler serves GET /api/tickets/{id}.
//
// A ticket belonging to someone else answers 404, never 403 (docs/spec.md §11).
// A 403 would confirm that the id names a real ticket, which is exactly what
// the caller must not be able to learn. The query returns no rows for both
// cases, so the handler cannot tell them apart either.
func GetTicketHandler(svc *application.Service, caller CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := caller(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		row, err := svc.Get(r.Context(), id, who.ID)
		if err != nil {
			if errors.Is(err, application.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, NewTicketResponse(row))
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
func GetTicketHistoryHandler(svc *application.Service, caller CallerResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := caller(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		entries, err := svc.History(r.Context(), id, who.ID)
		if err != nil {
			// An empty history and a ticket that is not the caller's are the
			// same answer, and the service cannot tell them apart either.
			if errors.Is(err, application.ErrTicketNotFound) {
				httperr.Write(w, http.StatusNotFound, "no such ticket")
				return
			}
			slog.ErrorContext(r.Context(), "reading a ticket history failed", "error", err)
			httperr.WriteInternal(w)
			return
		}

		httpx.WriteJSON(w, r, http.StatusOK, NewTicketHistoryResponse(entries))
	})
}
