package http

import (
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
)

// What is left here is the agent's wire shapes, and they are here only until
// the slice that moves the agent features. The customer shapes that used to
// share this file are now either in the feature that owns them — the create
// body, the list page — or in wire.go, where the ones more than one feature
// returns belong.

// QueueEntryResponse is a ticket as the agent queue shows it: everything a
// customer sees about their own, plus who asked.
//
// Embedded rather than duplicated, so a field added to TicketResponse appears
// here without anyone remembering to add it. The two views of a ticket must not
// be allowed to disagree about what a ticket is.
//
// It carries the requester's display name and not their id. An agent has no use
// for another user's primary key, and putting one on the wire hands out an
// identifier to enumerate — the same reasoning that kept actor_id out of the
// history DTO in T14b.
type QueueEntryResponse struct {
	TicketResponse
	RequesterName string `json:"requester_name"`
}

// NewQueueEntryResponse maps a queue row onto the wire shape.
//
// The email stands in when there is no name, because Clerk holds none for
// someone who signed up with an email and a password — and a row an agent
// cannot attribute to anyone is not a usable queue entry. Deciding it here
// rather than in the repository keeps it what it is: a display choice.
func NewQueueEntryResponse(row application.QueueEntry) QueueEntryResponse {
	name := row.RequesterName
	if name == "" {
		name = row.RequesterEmail
	}
	return QueueEntryResponse{
		TicketResponse: NewTicketResponse(row.Ticket),
		RequesterName:  name,
	}
}

// QueueResponse is one page of the agent queue.
//
// A distinct type from TicketListResponse rather than a generic one over both:
// they are different endpoints with different sort orders, and the cursor in
// each means a position in its own ordering. Sharing the wrapper would suggest
// a cursor from one works on the other, and it does not.
type QueueResponse struct {
	Tickets    []QueueEntryResponse `json:"tickets"`
	NextCursor *string              `json:"next_cursor"`
}

// AgentTicketResponse is a ticket as an agent sees it: everything a customer
// sees about their own, plus who is on it.
//
// A separate type rather than a field added to TicketResponse, and the reason
// is who reads each one. TicketResponse is what a customer gets back for their
// own ticket, and putting an assignee id in it would hand every customer the
// primary key of the agent working their case — the leak T14b closed by
// dropping actor_id from the history and T19 avoided by sending requester_name
// instead of an id.
//
// Embedded rather than restated, so a field added to TicketResponse appears
// here without anyone remembering. The two views of a ticket must not disagree
// about what a ticket is.
type AgentTicketResponse struct {
	TicketResponse
	AssigneeID *string `json:"assignee_id"`
}

// NewAgentTicketResponse maps a ticket onto the agent's wire shape.
func NewAgentTicketResponse(row domain.Ticket) AgentTicketResponse {
	var assignee *string
	if row.AssigneeID != nil {
		id := row.AssigneeID.String()
		assignee = &id
	}

	return AgentTicketResponse{
		TicketResponse: NewTicketResponse(row),
		AssigneeID:     assignee,
	}
}
