// Every endpoint that takes a body refuses a value after it, and refuses one
// over the cap.
//
// At the module root because it is an assertion about the *set* of endpoints,
// not about any one of them: a rule that holds where somebody happened to look
// is not a rule. Two features are exercised here, and a feature may not import
// its neighbour — a test of both together is not a feature.
package ticket_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/assign"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/features/transition"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
	tickethttp "github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/transport/http"
)

// json.Decoder reads one value and stops, so a body with something after it
// decoded happily and the trailing value was never seen. Nothing downstream was
// wrong about it — it simply was not read — but a client shipping garbage after
// a valid body should be told, because the next thing it ships may be the half
// the caller meant.
//
// Reported by CodeRabbit on PR #13 against the transition endpoint. Asserted
// here for every endpoint that takes a body, because a rule that holds where
// somebody happened to look is not a rule.
func TestNoEndpointAcceptsAValueAfterTheBody(t *testing.T) {
	caller := ports.Caller{ID: uuid.New(), Role: domain.RoleAgent}
	resolve := func(context.Context) (ports.Caller, bool) { return caller, true }

	id := uuid.New().String()

	cases := []struct {
		name    string
		method  string
		pattern string
		path    string
		handler http.Handler
		body    string
	}{
		{
			name:    "transitions",
			method:  http.MethodPost,
			pattern: "/tickets/{id}" + tickethttp.TransitionsSuffix,
			path:    "/tickets/" + id + tickethttp.TransitionsSuffix,
			handler: transition.HTTP(&transitionSpy{}, resolve),
			body:    `{"to":"pending"}{}`,
		},
		{
			name:    "assignee",
			method:  http.MethodPatch,
			pattern: "/tickets/{id}" + tickethttp.AssigneeSuffix,
			path:    "/tickets/" + id + tickethttp.AssigneeSuffix,
			handler: assign.HTTP(&assignSpy{}, resolve),
			body:    `{"assignee_id":null}{}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := chi.NewRouter()
			r.Method(tc.method, tc.pattern, tc.handler)

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 — a value after the body was accepted\nbody: %s",
					rec.Code, rec.Body.String())
			}
		})
	}
}

// A body over the cap is a size failure, not a syntax one. io.LimitReader
// truncated silently and the result read as invalid JSON, which told the caller
// their JSON was malformed when it was merely too big.
func TestAnOverlongBodyIsRefused(t *testing.T) {
	caller := ports.Caller{ID: uuid.New(), Role: domain.RoleAgent}
	resolve := func(context.Context) (ports.Caller, bool) { return caller, true }

	r := chi.NewRouter()
	r.Method(http.MethodPost, "/tickets/{id}"+tickethttp.TransitionsSuffix,
		transition.HTTP(&transitionSpy{}, resolve))

	huge := `{"to":"pending","reason":"` + strings.Repeat("x", 70<<10) + `"}`
	req := httptest.NewRequest(http.MethodPost,
		"/tickets/"+uuid.New().String()+tickethttp.TransitionsSuffix, strings.NewReader(huge))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Spies that do nothing but satisfy the two use cases. What these tests assert
// happens before either is reached: a body this endpoint cannot read is refused
// without the ticket ever being touched.
type transitionSpy struct{}

func (transitionSpy) Handle(context.Context, transition.Command) (domain.Ticket, error) {
	return domain.Ticket{}, nil
}

type assignSpy struct{}

func (assignSpy) Handle(context.Context, uuid.UUID, *uuid.UUID) (domain.Ticket, error) {
	return domain.Ticket{}, nil
}
