package domain

import "errors"

// ErrTicketNotFound means no ticket has that id, or it is not the caller's.
//
// The two are deliberately the same error. A 403 would confirm that an id names
// a real ticket, which is exactly what a caller must not be able to learn
// (docs/spec.md §11), and the scoped queries return nothing for both cases so
// nothing above them could tell them apart even if it wanted to.
//
// It lives in the domain rather than with a feature because six reads raise or
// answer it — a customer's ticket, a customer's timeline, an agent's ticket, an
// agent's timeline, an assignment and a transition — and because "this ticket
// is not available to you" is a business answer rather than a detail of any one
// contract. Under the vertical slice rules a type shared by that many features
// has to sit below all of them, and the domain is where it belongs on merit
// rather than only by elimination.
var ErrTicketNotFound = errors.New("ticket: no ticket with that id")

// ErrNoSLAPolicy means nothing serves that priority.
//
// The ticket module does not know what resolves an SLA policy — that is a
// contract in ports, implemented in the composition root — so this sentinel is
// declared here rather than re-exported from whatever provides one. The adapter
// translates the provider's own error into this, which is how a handler can
// tell "our seed data is wrong" apart from "the database is down" without
// importing another module to do it.
var ErrNoSLAPolicy = errors.New("ticket: no SLA policy serves that priority")
