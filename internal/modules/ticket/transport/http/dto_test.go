package http_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// The agent's does, because the assignment control cannot render who is on a
// ticket without it.
func TestTheAgentTicketCarriesTheAssignee(t *testing.T) {
	assignee := uuid.New()
	encoded := mustEncode(t, tickethttp.NewAgentTicketResponse(domain.Ticket{
		ID:         uuid.New(),
		AssigneeID: &assignee,
		Title:      "a ticket",
	}))

	if !strings.Contains(encoded, assignee.String()) {
		t.Errorf("the agent's ticket is missing the assignee id: %s", encoded)
	}
}

// An unassigned ticket sends null rather than omitting the field, so a client
// can tell "nobody is on it" from "this endpoint does not say".
func TestAnUnassignedAgentTicketSendsNull(t *testing.T) {
	encoded := mustEncode(t, tickethttp.NewAgentTicketResponse(domain.Ticket{
		ID:    uuid.New(),
		Title: "a ticket",
	}))

	if !strings.Contains(encoded, `"assignee_id":null`) {
		t.Errorf("want an explicit null assignee: %s", encoded)
	}
}

func mustEncode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return string(b)
}
