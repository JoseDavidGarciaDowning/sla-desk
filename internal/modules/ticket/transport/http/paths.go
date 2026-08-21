package http

// The module's paths, in one file, because the route table is only as readable
// as the constants it is written in terms of.
//
// QueuePath is relative: it is mounted under internal/app's agent prefix, and
// the prefix is the composition root's to choose. The others are absolute
// because they are mounted at the root of the authenticated group.
const (
	// TicketsPath is the customer's collection endpoint.
	TicketsPath = "/api/tickets"

	// TicketHistorySuffix is appended to a ticket's path.
	TicketHistorySuffix = "/history"

	// QueuePath is the agent's collection endpoint, relative to the agent
	// prefix.
	QueuePath = "/tickets"

	// AssigneeSuffix and TransitionsSuffix are appended to a queue ticket's
	// path.
	AssigneeSuffix    = "/assignee"
	TransitionsSuffix = "/transitions"
)
