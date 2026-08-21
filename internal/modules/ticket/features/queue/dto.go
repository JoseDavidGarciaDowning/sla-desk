package queue

import tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"

// EntryResponse is a ticket as the agent queue shows it: everything a customer
// sees about their own, plus who asked.
//
// Embedded rather than duplicated, so a field added to TicketResponse appears
// here without anyone remembering to add it. The two views of a ticket must not
// be allowed to disagree about what a ticket is.
//
// It carries the requester's display name and not their id. An agent has no use
// for another user's primary key, and putting one on the wire hands out an
// identifier to enumerate — the same reasoning that kept actor_id out of the
// history DTO in T14b.
type EntryResponse struct {
	tickethttp.TicketResponse
	RequesterName string `json:"requester_name"`
}

// NewEntryResponse maps a queue row onto the wire shape.
//
// The email stands in when there is no name, because Clerk holds none for
// someone who signed up with an email and a password — and a row an agent
// cannot attribute to anyone is not a usable queue entry. Deciding it here
// rather than in the repository keeps it what it is: a display choice.
func NewEntryResponse(row Entry) EntryResponse {
	name := row.RequesterName
	if name == "" {
		name = row.RequesterEmail
	}
	return EntryResponse{
		TicketResponse: tickethttp.NewTicketResponse(row.Ticket),
		RequesterName:  name,
	}
}

// Response is one page of the agent queue.
//
// A distinct type from the customer list's page rather than one generic over
// both: they are different endpoints with different sort orders, and the cursor
// in each means a position in its own ordering. Sharing the wrapper would
// suggest a cursor from one works on the other, and it does not.
type Response struct {
	Tickets    []EntryResponse `json:"tickets"`
	NextCursor *string         `json:"next_cursor"`
}
