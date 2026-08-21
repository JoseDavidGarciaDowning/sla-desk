package http

import (
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// TicketResponse is a ticket as the API returns it.
//
// Not domain.Ticket: that would put every future column on the wire and make a
// schema change a breaking API change.
//
// It lives here rather than in the feature that first returns one because four
// of them do — create, get, list and, by embedding, both agent views. The
// embedding is the reason this cannot be feature-local: AgentTicketResponse and
// QueueEntryResponse embed it precisely so a field added here appears there
// without anyone remembering, and the two views of a ticket must not be allowed
// to disagree about what a ticket is.
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
// Not domain.HistoryEntry. That row carries ActorID — another user's primary
// key — and a customer has no use for it: it hands out an identifier to
// enumerate and links their view of a ticket to the agent roster. The role is
// what a timeline is actually for. "An agent resolved this" is the information;
// which agent is not.
//
// The row's own id is left out for the same kind of reason. It is a sequence
// number a client could count with, and nothing in the UI addresses an entry.
//
// Shared here because the customer's timeline and the agent's return the same
// shape from two features.
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
