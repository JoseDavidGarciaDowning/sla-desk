package create_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/create"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/tickettest"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// fakeCreator stands in for the use case, so these tests are about the HTTP
// behaviour and not about resolving an SLA policy.
type fakeCreator struct {
	got create.Command
	out domain.Ticket
	err error

	calls int
}

func (f *fakeCreator) Handle(_ context.Context, in create.Command) (domain.Ticket, error) {
	f.calls++
	f.got = in
	if f.err != nil {
		return domain.Ticket{}, f.err
	}
	return f.out, nil
}

func TestCreateTicketReturns201WithTheTicket(t *testing.T) {
	caller := tickettest.Customer(t)
	due := time.Now().Add(24 * time.Hour).UTC()
	creator := &fakeCreator{out: domain.Ticket{
		ID:          tickettest.UUIDOf(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    domain.CategoryBilling,
		Priority:    domain.PriorityNormal,
		Status:      domain.StatusOpen,
		SLADueAt:    &due,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}}

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(caller)), tickettest.ValidTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/tickets/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" {
		t.Errorf("Location = %q", got)
	}

	var body tickethttp.TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Status != domain.StatusOpen {
		t.Errorf("status = %q, want open", body.Status)
	}
	if body.SLADueAt == nil {
		t.Error("sla_due_at is null on a ticket whose clock is running")
	}
	if body.SLABreached {
		t.Error("a brand new ticket is reported as breached")
	}
}

// docs/spec.md §4.3: never trust a user id from the client. The requester is
// the authenticated caller, whatever the body says.
func TestRequesterComesFromTheSessionNotTheBody(t *testing.T) {
	caller := tickettest.Customer(t)
	creator := &fakeCreator{}

	body := `{
		"title": "Hello",
		"description": "World",
		"category": "billing",
		"priority": "normal",
		"requester_id": "cccccccc-cccc-cccc-cccc-cccccccccccc"
	}`

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(caller)), body)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if creator.calls != 1 {
		t.Fatalf("Create calls = %d, want 1", creator.calls)
	}
	if creator.got.RequesterID != tickettest.UUIDOf(t, caller.ID.String()) {
		t.Errorf("requester = %v, want the authenticated caller %v", creator.got.RequesterID, caller.ID)
	}
	// domain.RoleCustomer, not the identity role the caller carries: the
	// handler is expected to have translated one vocabulary into the other.
	if creator.got.ActorRole != domain.RoleCustomer {
		t.Errorf("actor role = %q, want the caller's role from our table", creator.got.ActorRole)
	}
}

func TestCreateTicketRejectsAnUnauthenticatedRequest(t *testing.T) {
	creator := &fakeCreator{}

	// A resolver that finds nobody, which is what a route mounted outside the
	// authenticated group would produce.
	handler := create.HTTP(creator, tickettest.NoCaller)
	r := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(tickettest.ValidTicketBody))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if creator.calls != 0 {
		t.Error("an unauthenticated request reached the store")
	}
}

func TestCreateTicketReportsEveryInvalidField(t *testing.T) {
	creator := &fakeCreator{}
	body := `{"title":"","description":"","category":"sales","priority":"critical"}`

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(tickettest.Customer(t))), body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if creator.calls != 0 {
		t.Error("an invalid request reached the store")
	}

	body_ := tickettest.DecodeProblem(t, rec)
	errs, ok := body_["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors = %v, want an object", body_["errors"])
	}
	for _, field := range []string{"title", "description", "category", "priority"} {
		if errs[field] == nil {
			t.Errorf("no error for %q", field)
		}
	}
}

func TestCreateTicketRejectsAMalformedBody(t *testing.T) {
	creator := &fakeCreator{}

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(tickettest.Customer(t))), `{"title":`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if creator.calls != 0 {
		t.Error("a malformed body reached the store")
	}
}

// A priority with no policy is our seed being wrong, not the caller's request
// being wrong — validation has already established the priority is one of the
// four. It must not be reported as a client error.
func TestMissingPolicyIsReportedAsAServerFault(t *testing.T) {
	creator := &fakeCreator{err: domain.ErrNoSLAPolicy}

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(tickettest.Customer(t))), tickettest.ValidTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestStoreFailuresDoNotLeakTheirCause(t *testing.T) {
	creator := &fakeCreator{err: errors.New(`pq: relation "tickets" does not exist on host db.internal`)}

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(tickettest.Customer(t))), tickettest.ValidTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	for _, leak := range []string{"relation", "db.internal", "tickets"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the response echoed %q: %s", leak, rec.Body.String())
		}
	}
}

// Whitespace is trimmed once, before the write, so the stored text and the
// validated text are the same.
func TestCreateTicketTrimsTheText(t *testing.T) {
	creator := &fakeCreator{}
	body := `{"title":"  Padded  ","description":"\n  Body  \n","category":"other","priority":"low"}`

	handler, r := tickettest.PostRequest(t, create.HTTP(creator, tickettest.ResolverFor(tickettest.Customer(t))), body)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if creator.got.Title != "Padded" {
		t.Errorf("title = %q, want Padded", creator.got.Title)
	}
	if creator.got.Description != "Body" {
		t.Errorf("description = %q, want Body", creator.got.Description)
	}
}

// ── Reading tickets ─────────────────────────────────────────────────────────
