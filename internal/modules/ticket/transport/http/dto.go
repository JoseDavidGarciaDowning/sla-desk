package http

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// Bounds mirror the CHECK constraints in migration 003. Validating here as well
// is not duplication for its own sake: without it an oversized title comes back
// as a constraint violation, which is a 500 that names nothing. With it the
// client gets a 400 that names the field.
const (
	maxTitleLength       = 200
	maxDescriptionLength = 10000
)

// CreateTicketRequest is the body of POST /api/tickets.
//
// There is deliberately no requester_id, status or sla_* field. docs/spec.md
// §4.3 forbids trusting a user id from a client, and a struct with nowhere to
// put one cannot be talked into it — the value is dropped when the body is
// decoded, before any code has a chance to read it.
type CreateTicketRequest struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Category    domain.Category `json:"category"`
	Priority    domain.Priority `json:"priority"`
}

var (
	validCategories = []domain.Category{
		domain.CategoryBilling, domain.CategoryTechnical, domain.CategoryAccount, domain.CategoryOther,
	}
	validPriorities = []domain.Priority{
		domain.PriorityUrgent, domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow,
	}

	// Statuses are never accepted in a request body — a client does not choose
	// what state a ticket is in — but they are accepted as a list filter, and
	// the frontend needs them to render both the filter control and a status
	// label. In the contract for that reason, and validated for the same one.
	validStatuses = []domain.Status{
		domain.StatusOpen, domain.StatusPending, domain.StatusResolved, domain.StatusClosed,
	}
)

// Normalised returns the request with surrounding whitespace removed, so the
// handler and the database see the same text and no caller has to remember to
// trim.
func (r CreateTicketRequest) Normalised() CreateTicketRequest {
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)
	return r
}

// Validate returns one message per rejected field, keyed by its JSON name.
//
// Every field is checked, not just the first to fail: a client fixing one
// problem per round trip is a worse experience than a form that shows all of
// them at once.
func (r CreateTicketRequest) Validate() map[string]string {
	r = r.Normalised()
	errs := make(map[string]string)

	// utf8.RuneCountInString, not len: the database counts characters, and a
	// title of 200 accented ones is 200 characters and rather more bytes.
	switch n := utf8.RuneCountInString(r.Title); {
	case n == 0:
		errs["title"] = "must not be empty"
	case n > maxTitleLength:
		errs["title"] = fmt.Sprintf("must be at most %d characters", maxTitleLength)
	}

	switch n := utf8.RuneCountInString(r.Description); {
	case n == 0:
		errs["description"] = "must not be empty"
	case n > maxDescriptionLength:
		errs["description"] = fmt.Sprintf("must be at most %d characters", maxDescriptionLength)
	}

	if !slices.Contains(validCategories, r.Category) {
		errs["category"] = "must be one of " + join(validCategories)
	}
	if !slices.Contains(validPriorities, r.Priority) {
		errs["priority"] = "must be one of " + join(validPriorities)
	}

	return errs
}

// join renders a vocabulary for the "must be one of ..." message. Unlike the
// membership test above there is no version of this in the standard library —
// strings.Join takes []string, and these are named types over string.
func join[T ~string](values []T) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return strings.Join(out, ", ")
}

// TicketResponse is a ticket as the API returns it.
//
// Not domain.Ticket: that would put pgtype values and every future column on the
// wire, and make a schema change a breaking API change.
type TicketResponse struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Category    domain.Category `json:"category"`
	Priority    domain.Priority `json:"priority"`
	Status      domain.Status   `json:"status"`

	// SLADueAt is null while the clock is paused, which is what makes a paused
	// ticket unable to breach. The frontend renders the absence, not a zero.
	SLADueAt    *time.Time `json:"sla_due_at"`
	SLABreached bool       `json:"sla_breached"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewTicketResponse(row domain.Ticket) TicketResponse {
	return TicketResponse{
		ID:          row.ID.String(),
		Title:       row.Title,
		Description: row.Description,
		Category:    row.Category,
		Priority:    row.Priority,
		Status:      row.Status,
		SLADueAt:    row.SLADueAt,
		SLABreached: row.Breached(),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

// TicketHistoryEntry is one status change, as a requester is allowed to see it.
//
// Not domain.TicketStatusHistory. That row carries ActorID — another user's
// primary key — and a customer has no use for it: it hands out an identifier to
// enumerate and links their view of a ticket to the agent roster. The role is
// what a timeline is actually for. "An agent resolved this" is the information;
// which agent is not.
//
// The row's own id is left out for the same kind of reason. It is a sequence
// number a client could count with, and nothing in the UI addresses an entry.
type TicketHistoryEntry struct {
	// FromStatus is null on the entry that records the ticket's creation, which
	// is the only entry that moved from nowhere.
	FromStatus *domain.Status `json:"from_status"`
	ToStatus   domain.Status  `json:"to_status"`
	ActorRole  domain.Role    `json:"actor_role"`
	Reason     *string        `json:"reason"`
	CreatedAt  time.Time      `json:"created_at"`
}

// TicketHistoryResponse is a ticket's timeline, oldest first.
//
// An object rather than a bare array: a top-level JSON array cannot grow a
// field later without breaking every client, and this one will want paging or a
// count eventually.
type TicketHistoryResponse struct {
	Entries []TicketHistoryEntry `json:"entries"`
}

func NewTicketHistoryResponse(rows []domain.HistoryEntry) TicketHistoryResponse {
	// make, so an empty timeline encodes as [] rather than null — though the
	// handler answers 404 before it can be empty.
	entries := make([]TicketHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, TicketHistoryEntry{
			FromStatus: row.FromStatus,
			ToStatus:   row.ToStatus,
			ActorRole:  row.ActorRole,
			Reason:     row.Reason,
			CreatedAt:  row.CreatedAt,
		})
	}
	return TicketHistoryResponse{Entries: entries}
}
