package history_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/history"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/tickettest"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// fakeHistorian stands in for the use case: one method, because history.Tickets
// has one. The reader this replaced answered three endpoints at once, so a test
// about a timeline carried stubs for listing and for reading a ticket.
type fakeHistorian struct {
	history          []domain.HistoryEntry
	err              error
	historyTicketID  uuid.UUID
	historyRequester uuid.UUID
	historyCalls     int
}

func (f *fakeHistorian) Handle(_ context.Context, ticketID, requesterID uuid.UUID) ([]domain.HistoryEntry, error) {
	f.historyCalls++
	f.historyTicketID, f.historyRequester = ticketID, requesterID
	return f.history, f.err
}

// An empty history is a 404, not an empty timeline.
//
// Every ticket has at least the row recording its creation, so nothing is
// returned only when the ticket does not exist or is not the caller's — and the
// query cannot tell those apart, which is what stops this handler from
// confirming that an id names a real ticket (docs/spec.md §11).
func TestHistoryAnswers404WhenThereIsNone(t *testing.T) {
	reader := &fakeHistorian{}
	handler, r := tickettest.GetRequest(t, history.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\nbody: %s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusForbidden {
		t.Error("403 confirms the ticket exists")
	}
}

func TestHistoryReturnsTheEntriesInOrder(t *testing.T) {
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	open := domain.StatusOpen

	reader := &fakeHistorian{history: []domain.HistoryEntry{
		tickettest.HistoryRow(nil, domain.StatusOpen, domain.RoleCustomer, base),
		tickettest.HistoryRow(&open, domain.StatusPending, domain.RoleAgent, base.Add(time.Hour)),
	}}

	handler, r := tickettest.GetRequest(t, history.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	var body tickethttp.TicketHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v\nbody: %s", err, rec.Body.String())
	}

	if len(body.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(body.Entries))
	}
	if body.Entries[0].FromStatus != nil {
		t.Errorf("the creation entry has from_status %v, want null", *body.Entries[0].FromStatus)
	}
	if body.Entries[1].ToStatus != domain.StatusPending {
		t.Errorf("entry 1 to_status = %q", body.Entries[1].ToStatus)
	}
	if body.Entries[1].ActorRole != domain.RoleAgent {
		t.Errorf("entry 1 actor_role = %q — who moved it is the point of a timeline", body.Entries[1].ActorRole)
	}
}

// The timeline says which kind of person moved the ticket, never which one.
// actor_id is another user's primary key, and a customer has no use for it —
// putting it on the wire hands out an identifier for enumeration and links a
// customer's view to the agent roster.
func TestHistoryNeverExposesTheActorsIdentity(t *testing.T) {
	reader := &fakeHistorian{history: []domain.HistoryEntry{
		tickettest.HistoryRow(nil, domain.StatusOpen, domain.RoleAgent, time.Now()),
	}}

	handler, r := tickettest.GetRequest(t, history.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	for _, leak := range []string{"actor_id", "11111111", "ActorID"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("the response mentions %q: %s", leak, rec.Body.String())
		}
	}
}

func TestHistoryScopesToTheCallerInTheQuery(t *testing.T) {
	caller := tickettest.Customer(t)
	reader := &fakeHistorian{history: []domain.HistoryEntry{
		tickettest.HistoryRow(nil, domain.StatusOpen, domain.RoleCustomer, time.Now()),
	}}

	handler, r := tickettest.GetRequest(t, history.HTTP(reader, tickettest.ResolverFor(caller)),
		"/api/tickets/{id}/history",
		"/api/tickets/6f1b5f2a-0000-4000-8000-000000000001/history")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if reader.historyRequester != tickettest.UUIDOf(t, caller.ID.String()) {
		t.Errorf("requester = %v, want the authenticated caller", reader.historyRequester)
	}
}

func TestHistoryRejectsAnIDThatIsNotAUUID(t *testing.T) {
	reader := &fakeHistorian{}
	handler, r := tickettest.GetRequest(t, history.HTTP(reader, tickettest.ResolverFor(tickettest.Customer(t))),
		"/api/tickets/{id}/history", "/api/tickets/not-a-uuid/history")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if reader.historyCalls != 0 {
		t.Error("the query ran with an unparsed id")
	}
}
