package get_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/get"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/tickettest"
)

// fakeGetter records what the handler asked for and answers with a ticket.
type fakeGetter struct {
	one domain.Ticket
	err error

	getID        uuid.UUID
	getRequester uuid.UUID
	getCalls     int
}

func (f *fakeGetter) Handle(_ context.Context, id, requesterID uuid.UUID) (domain.Ticket, error) {
	f.getCalls++
	f.getID, f.getRequester = id, requesterID
	return f.one, f.err
}

// docs/spec.md §11: another customer's ticket is 404, never 403. A 403 confirms
// the ticket exists, which is exactly what the caller must not learn.
func TestGetAnswers404ForATicketThatIsNotYours(t *testing.T) {
	reader := &fakeGetter{err: domain.ErrTicketNotFound}

	handler, r := tickettest.GetRequest(t, get.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
		"/api/tickets/{id}", "/api/tickets/44444444-4444-4444-4444-444444444444")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — 403 would confirm the ticket exists", rec.Code)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "forbidden") {
		t.Errorf("the body hints at existence: %s", rec.Body.String())
	}
}

func TestGetScopesToTheCallerInTheQuery(t *testing.T) {
	caller := tickettest.Customer(t)
	created := time.Now().UTC()
	reader := &fakeGetter{one: tickettest.SampleTicket(t, "55555555-5555-5555-5555-555555555555", created)}

	handler, r := tickettest.GetRequest(t, get.HTTP(reader, tickettest.ResolverFor(caller)),
		"/api/tickets/{id}", "/api/tickets/55555555-5555-5555-5555-555555555555")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if reader.getRequester != tickettest.UUIDOf(t, caller.ID.String()) {
		t.Errorf("requester = %v, want the authenticated caller", reader.getRequester)
	}
}

func TestGetRejectsAnIDThatIsNotAUUID(t *testing.T) {
	reader := &fakeGetter{}
	handler, r := tickettest.GetRequest(t, get.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))), "/api/tickets/{id}", "/api/tickets/not-a-uuid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if reader.getCalls != 0 {
		t.Error("a malformed id reached the store")
	}
}

// ── Filters (T14) ────────────────────────────────────────────────────────────
