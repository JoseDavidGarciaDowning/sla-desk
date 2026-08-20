package list

import tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"

// Response is one page of tickets.
//
// NextCursor is null on the last page. It is opaque on purpose: a client that
// parses it becomes coupled to the sort key, and changing the ordering would
// then be a breaking change.
//
// A distinct type from the queue's page rather than one generic over both: they
// are different endpoints with different sort orders, and the cursor in each
// means a position in its own ordering. Sharing the wrapper would suggest a
// cursor from one works on the other, and it does not.
type Response struct {
	Tickets    []tickethttp.TicketResponse `json:"tickets"`
	NextCursor *string                     `json:"next_cursor"`
}
