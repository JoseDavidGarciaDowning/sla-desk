package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/application"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

type transitionSpy struct {
	called bool
	got    application.StatusChange
	err    error
}

func (s *transitionSpy) Transition(_ context.Context, in application.StatusChange) (domain.Ticket, error) {
	s.called = true
	s.got = in
	if s.err != nil {
		return domain.Ticket{}, s.err
	}
	return domain.Ticket{ID: in.TicketID, Status: in.Target}, nil
}

func postTransition(t *testing.T, spy *transitionSpy, role domain.Role, body string) *httptest.ResponseRecorder {
	t.Helper()

	caller := tickethttp.Caller{ID: uuid.New(), Role: role}
	resolve := func(context.Context) (tickethttp.Caller, bool) { return caller, true }

	r := chi.NewRouter()
	r.Method(http.MethodPost, "/tickets/{id}"+tickethttp.TransitionsSuffix,
		tickethttp.TransitionTicketHandler(spy, resolve))

	req := httptest.NewRequest(http.MethodPost,
		"/tickets/"+uuid.New().String()+tickethttp.TransitionsSuffix, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The actor's role comes from the authenticated caller, never from the body.
// A client that could name its own role could take any edge in the table.
func TestTheActorRoleComesFromTheCallerAndNotTheBody(t *testing.T) {
	spy := &transitionSpy{}

	rec := postTransition(t, spy, domain.RoleAgent,
		`{"to":"pending","actor_role":"admin","reason":"waiting on the customer"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	if spy.got.ActorRole != domain.RoleAgent {
		t.Errorf("ActorRole = %q, want the caller's %q", spy.got.ActorRole, domain.RoleAgent)
	}
	if spy.got.Target != domain.StatusPending {
		t.Errorf("Target = %q, want pending", spy.got.Target)
	}
	if spy.got.Reason == nil || *spy.got.Reason != "waiting on the customer" {
		t.Errorf("Reason = %v", spy.got.Reason)
	}
}

// 403 and 400 are different answers, and the domain already tells them apart.
// Flattening them tells an agent their client is broken when the truth is that
// somebody else has to make the move.
func TestARoleThatMayNotMakeTheMoveIs403(t *testing.T) {
	spy := &transitionSpy{err: domain.ErrForbidden}

	rec := postTransition(t, spy, domain.RoleAgent, `{"to":"resolved"}`)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403\nbody: %s", rec.Code, rec.Body.String())
	}
}

// An edge that does not exist is a field error on "to". A closed ticket lands
// here too — it is terminal, so no edge leaves it for any role, and it needs no
// special case in the handler.
func TestAnImpossibleTransitionIsAFieldError(t *testing.T) {
	spy := &transitionSpy{err: domain.ErrInvalidTransition}

	rec := postTransition(t, spy, domain.RoleAgent, `{"to":"open"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}

	var problem struct {
		Errors map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if problem.Errors["to"] == "" {
		t.Errorf("no field error for 'to': %s", rec.Body.String())
	}
}

// An unknown word must not reach the state machine. It would be refused there
// too, but as "you cannot go from open to blorp" — which reads like an edge
// that might exist somewhere else.
func TestAnUnknownStatusNeverReachesTheStateMachine(t *testing.T) {
	for _, body := range []string{`{"to":"blorp"}`, `{"to":""}`, `{}`} {
		spy := &transitionSpy{}

		rec := postTransition(t, spy, domain.RoleAgent, body)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
		if spy.called {
			t.Errorf("body %s: reached the use case", body)
		}
	}
}

// A reason of whitespace is not a reason, and storing it would put a blank line
// in a timeline an agent reads.
func TestAWhitespaceReasonIsStoredAsAbsent(t *testing.T) {
	spy := &transitionSpy{}

	rec := postTransition(t, spy, domain.RoleAgent, `{"to":"pending","reason":"   "}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if spy.got.Reason != nil {
		t.Errorf("Reason = %q, want nil", *spy.got.Reason)
	}
}

func TestAnOverlongReasonIsRejected(t *testing.T) {
	spy := &transitionSpy{}

	rec := postTransition(t, spy, domain.RoleAgent,
		`{"to":"pending","reason":"`+strings.Repeat("x", 501)+`"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if spy.called {
		t.Error("an overlong reason reached the use case")
	}
}

func TestTransitioningAnUnknownTicketIs404(t *testing.T) {
	spy := &transitionSpy{err: application.ErrTicketNotFound}

	rec := postTransition(t, spy, domain.RoleAgent, `{"to":"pending"}`)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
