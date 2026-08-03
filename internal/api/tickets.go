package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/auth"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/httperr"
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
			httperr.WriteInternal(w)
			return
		}

		w.Header().Set("Location", TicketsPath+"/"+uuidString(created.ID))
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
	ListTicketsByRequester(ctx context.Context, arg store.ListTicketsByRequesterParams) ([]store.Ticket, error)
	GetTicketForRequester(ctx context.Context, arg store.GetTicketForRequesterParams) (store.Ticket, error)
}

// encodeCursor packs the sort key of the last row on a page.
func encodeCursor(createdAt time.Time, id pgtype.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + uuidString(id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

var errBadCursor = errors.New("api: cursor is not readable")

func decodeCursor(s string) (time.Time, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, errBadCursor
	}
	at, rest, found := strings.Cut(string(raw), "|")
	if !found {
		return time.Time{}, pgtype.UUID{}, errBadCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, errBadCursor
	}
	var id pgtype.UUID
	if err := id.Scan(rest); err != nil {
		return time.Time{}, pgtype.UUID{}, errBadCursor
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

// ListTicketsHandler serves GET /api/tickets.
//
// Mount behind RequireAuth. The scope is the query's WHERE clause, so this
// handler has no ownership check to forget: another customer's row never
// arrives to be filtered out.
func ListTicketsHandler(tickets TicketReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.UserFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		params := store.ListTicketsByRequesterParams{RequesterID: caller.ID}

		if raw := r.URL.Query().Get("cursor"); raw != "" {
			createdAt, id, err := decodeCursor(raw)
			if err != nil {
				httperr.Write(w, http.StatusBadRequest, "the cursor is not one this API issued")
				return
			}
			params.AfterCreatedAt = &createdAt
			params.AfterID = id
		}

		// One more row than asked for. If it comes back, there is another page,
		// and that is cheaper to learn than by counting the table.
		limit := pageSize(r.URL.Query().Get("limit"))
		params.PageSize = limit + 1

		rows, err := tickets.ListTicketsByRequester(r.Context(), params)
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
		caller, ok := auth.UserFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var id pgtype.UUID
		if err := id.Scan(chi.URLParam(r, "id")); err != nil {
			httperr.Write(w, http.StatusBadRequest, "the ticket id is not a UUID")
			return
		}

		row, err := tickets.GetTicketForRequester(r.Context(), store.GetTicketForRequesterParams{
			ID:          id,
			RequesterID: caller.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
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
