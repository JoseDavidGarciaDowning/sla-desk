package http_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"

	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

func validCreateTicket() tickethttp.CreateTicketRequest {
	return tickethttp.CreateTicketRequest{
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    domain.CategoryBilling,
		Priority:    domain.PriorityNormal,
	}
}

func TestValidRequestHasNoFieldErrors(t *testing.T) {
	if errs := validCreateTicket().Validate(); len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

func TestValidationReportsEveryBadFieldAtOnce(t *testing.T) {
	req := tickethttp.CreateTicketRequest{
		Title:       "",
		Description: "",
		Category:    "sales",
		Priority:    "critical",
	}

	errs := req.Validate()

	for _, field := range []string{"title", "description", "category", "priority"} {
		if _, ok := errs[field]; !ok {
			t.Errorf("no error reported for %q — a client should see every problem in one round trip", field)
		}
	}
}

func TestValidationRejectsWhitespaceOnlyText(t *testing.T) {
	req := validCreateTicket()
	req.Title = "   \t\n "

	if _, ok := req.Validate()["title"]; !ok {
		t.Error("a title of nothing but whitespace was accepted")
	}
}

// The bounds match the CHECK constraints in migration 003. Validating in Go as
// well is not duplication for its own sake: it turns a 500 from a constraint
// violation into a 400 that names the field.
func TestValidationRejectsOversizedText(t *testing.T) {
	req := validCreateTicket()
	req.Title = strings.Repeat("a", 201)

	if _, ok := req.Validate()["title"]; !ok {
		t.Error("a 201 character title was accepted; the database allows 200")
	}

	req = validCreateTicket()
	req.Description = strings.Repeat("a", 10001)
	if _, ok := req.Validate()["description"]; !ok {
		t.Error("a 10001 character description was accepted; the database allows 10000")
	}
}

// docs/spec.md §4.3: never trust a user_id coming from the client. The struct
// has no field for it, so the value has nowhere to land.
func TestRequesterIDInTheBodyHasNowhereToLand(t *testing.T) {
	body := []byte(`{
		"title": "Hello",
		"description": "World",
		"category": "billing",
		"priority": "normal",
		"requester_id": "11111111-1111-1111-1111-111111111111",
		"status": "closed",
		"sla_due_at": "2000-01-01T00:00:00Z"
	}`)

	var req tickethttp.CreateTicketRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if errs := req.Validate(); len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if req.Title != "Hello" {
		t.Errorf("title = %q", req.Title)
	}
}

// Trimming happens once, here, so the handler and the database see the same
// text and no caller has to remember to do it.
func TestNormalisedTrimsSurroundingWhitespace(t *testing.T) {
	req := validCreateTicket()
	req.Title = "  Cannot download my invoice  "
	req.Description = "\nThe download button returns a 500.\n"

	got := req.Normalised()

	if got.Title != "Cannot download my invoice" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Description != "The download button returns a 500." {
		t.Errorf("description = %q", got.Description)
	}
}

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
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return string(raw)
}
