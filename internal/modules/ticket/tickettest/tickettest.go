// Package tickettest holds the fixtures the ticket module's tests share.
//
// It exists because four features and the module's remaining transport tests
// all need the same caller, the same sample ticket and the same way to mount a
// handler on a chi router. Before the features were split these lived in one
// test file that every test in the package could see; afterwards that is four
// packages, and the choice was between one shared fixture package and four
// copies that drift.
//
// It is not a feature and no feature imports another one through it: it names
// only the module's domain and its ports, which everything here already
// depends on. Nothing in production imports it.
package tickettest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/domain"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/modules/ticket/ports"
)

// UUIDOf parses a fixed uuid, failing the test rather than the assertion.
func UUIDOf(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parsing uuid: %v", err)
	}
	return id
}

// Customer is the caller the identity middleware would have attached for an
// ordinary requester.
func Customer(t *testing.T) ports.Caller {
	t.Helper()
	return ports.Caller{
		ID:   UUIDOf(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		Role: domain.RoleCustomer,
	}
}

// ResolverFor is the CallerResolver a test supplies in place of the composition
// root's. The contract is a function, so there is no stub type to declare — and
// this module's tests never mention the identity module, which is the boundary
// the contract exists to hold.
func ResolverFor(c ports.Caller) ports.CallerResolver {
	return func(context.Context) (ports.Caller, bool) { return c, true }
}

// NoCaller is a request that never passed through authentication, which is a
// wiring mistake rather than a bad request. Every handler must answer 401.
func NoCaller(context.Context) (ports.Caller, bool) {
	return ports.Caller{}, false
}

// ValidTicketBody is a create request that passes every field rule, so a test
// about one failure does not have to restate the other three.
const ValidTicketBody = `{
	"title": "Cannot download my invoice",
	"description": "The download button returns a 500.",
	"category": "billing",
	"priority": "normal"
}`

// PostRequest builds a request carrying a JSON body.
func PostRequest(t *testing.T, h http.Handler, body string) (http.Handler, *http.Request) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return h, r
}

// GetRequest mounts the handler on a chi router at the pattern it will really
// be served from, because the read handlers pull {id} out of chi's route
// context. Calling a handler directly leaves that context empty and every id
// looks malformed — which is how the first version of these tests failed.
func GetRequest(t *testing.T, h http.Handler, pattern, target string) (http.Handler, *http.Request) {
	t.Helper()

	router := chi.NewRouter()
	router.Method(http.MethodGet, pattern, h)

	return router, httptest.NewRequest(http.MethodGet, target, nil)
}

// SampleTicket is a ticket with every field a response reads.
func SampleTicket(t *testing.T, id string, created time.Time) domain.Ticket {
	t.Helper()
	due := created.Add(24 * time.Hour)
	return domain.Ticket{
		ID:        UUIDOf(t, id),
		Title:     "Ticket " + id[:8],
		Category:  domain.CategoryOther,
		Priority:  domain.PriorityNormal,
		Status:    domain.StatusOpen,
		SLADueAt:  &due,
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// HistoryRow is one timeline entry.
//
// The row's own id is not in the domain entry: it is a storage detail, and
// nothing above the repository ever needed it. The ordering it used to tiebreak
// is settled by the query.
func HistoryRow(from *domain.Status, to domain.Status, role domain.Role, at time.Time) domain.HistoryEntry {
	return domain.HistoryEntry{
		FromStatus: from,
		ToStatus:   to,
		ActorID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ActorRole:  role,
		CreatedAt:  at,
	}
}

// DecodeProblem reads an RFC 9457 document out of a recorded response.
//
// The documents themselves are tested in internal/platform/httperr, which owns
// them. What the tests here assert is that each handler reaches for one — with
// the right status, and without leaking a cause — not that the encoding works.
func DecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the problem document: %v\nbody: %s", err, rec.Body.String())
	}
	return body
}

// StrPtr, SamePtr and Deref keep the pointer-field assertions readable.
func StrPtr(s string) *string { return &s }

func SamePtr(got, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func Deref(p *string) string {
	if p == nil {
		return "<none>"
	}
	return *p
}
