package http_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// The customer's ticket must not carry an assignee id. It is the primary key of
// the agent working their case, and handing it out is the leak T14b closed by
// dropping actor_id from the history and T19 avoided by sending requester_name.
//
// Asserted by encoding rather than by reading the struct: a field added with
// the wrong tag, or embedded from somewhere else, would still appear on the
// wire while the type looked untouched.
func TestTheCustomerTicketNeverCarriesAnAssignee(t *testing.T) {
	assignee := uuid.New()
	encoded := mustEncode(t, tickethttp.NewTicketResponse(domain.Ticket{
		ID:         uuid.New(),
		AssigneeID: &assignee,
		Title:      "a ticket",
	}))

	if strings.Contains(encoded, assignee.String()) {
		t.Errorf("the customer's ticket carries the assignee id: %s", encoded)
	}
	if strings.Contains(encoded, "assignee") {
		t.Errorf("the customer's ticket has an assignee field: %s", encoded)
	}
}
