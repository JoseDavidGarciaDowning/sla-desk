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

type assignSpy struct {
	called bool
	whom   *uuid.UUID
	err    error
}

func (s *assignSpy) Assign(_ context.Context, ticketID uuid.UUID, assignee *uuid.UUID) (domain.Ticket, error) {
	s.called = true
	s.whom = assignee
	if s.err != nil {
		return domain.Ticket{}, s.err
	}
	return domain.Ticket{ID: ticketID, AssigneeID: assignee}, nil
}

// assignPatch runs the handler behind a chi router, so {id} is populated the
// way production populates it.
func assignPatch(t *testing.T, spy *assignSpy, body string) *httptest.ResponseRecorder {
	t.Helper()

	caller := tickethttp.Caller{ID: uuid.New(), Role: domain.RoleAgent}
	resolve := func(context.Context) (tickethttp.Caller, bool) { return caller, true }

	r := chi.NewRouter()
	r.Method(http.MethodPatch, "/tickets/{id}"+tickethttp.AssigneeSuffix,
		tickethttp.AssignTicketHandler(spy, resolve))

	req := httptest.NewRequest(http.MethodPatch,
		"/tickets/"+uuid.New().String()+tickethttp.AssigneeSuffix, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAssigningAUserPassesTheIDThrough(t *testing.T) {
	spy := &assignSpy{}
	agent := uuid.New()

	rec := assignPatch(t, spy, `{"assignee_id":"`+agent.String()+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	if spy.whom == nil || *spy.whom != agent {
		t.Errorf("Assign received %v, want %s", spy.whom, agent)
	}
}

// null is the documented way to take somebody off a ticket.
func TestAnExplicitNullUnassigns(t *testing.T) {
	spy := &assignSpy{}

	rec := assignPatch(t, spy, `{"assignee_id":null}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	if !spy.called {
		t.Fatal("the unassignment never reached the use case")
	}
	if spy.whom != nil {
		t.Errorf("Assign received %v, want nil", spy.whom)
	}
}

// The distinction this endpoint's body shape exists for. An absent field is a
// client mistake; treating it as null would let an empty body silently take
// somebody off a ticket they are working on.
func TestAnAbsentFieldIsRejectedRatherThanTreatedAsNull(t *testing.T) {
	for _, body := range []string{`{}`, `{"something_else":1}`} {
		spy := &assignSpy{}

		rec := assignPatch(t, spy, body)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want a rejection", body, rec.Code)
		}
		if spy.called {
			t.Errorf("body %s: the ticket was unassigned by an empty body", body)
		}
	}
}

func TestAMalformedAssigneeIsRejected(t *testing.T) {
	for _, body := range []string{
		`{"assignee_id":"not-a-uuid"}`,
		`{"assignee_id":42}`,
		`not json at all`,
	} {
		spy := &assignSpy{}

		rec := assignPatch(t, spy, body)

		if rec.Code < 400 {
			t.Errorf("body %s: status = %d, want a rejection", body, rec.Code)
		}
		if spy.called {
			t.Errorf("body %s: reached the use case", body)
		}
	}
}

// A field error and not a 404: the request is well formed and the id is a real
// uuid, it just names somebody who cannot hold tickets. The same answer is
// given for an id that names nobody, so this endpoint cannot be used to learn
// which uuids are users.
//
// 400 rather than the 422 the planning card named — every other validation
// failure in this API is a 400, and the frontend already reads its field errors.
func TestAnUnassignableUserIsAFieldError(t *testing.T) {
	spy := &assignSpy{err: application.ErrNotAssignable}

	rec := assignPatch(t, spy, `{"assignee_id":"`+uuid.New().String()+`"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}

	var problem struct {
		Errors map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if problem.Errors["assignee_id"] == "" {
		t.Errorf("no field error for assignee_id: %s", rec.Body.String())
	}
}

func TestAssigningAnUnknownTicketIs404(t *testing.T) {
	spy := &assignSpy{err: domain.ErrTicketNotFound}

	rec := assignPatch(t, spy, `{"assignee_id":"`+uuid.New().String()+`"}`)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
