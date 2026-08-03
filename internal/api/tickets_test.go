package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/JoseDavidGarciaDowning/sla-desk/internal/api"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/auth"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/store"
	"github.com/JoseDavidGarciaDowning/sla-desk/internal/ticket"
)

type fakeCreator struct {
	got store.NewTicket
	out store.Ticket
	err error

	calls int
}

func (f *fakeCreator) Create(_ context.Context, in store.NewTicket) (store.Ticket, error) {
	f.calls++
	f.got = in
	if f.err != nil {
		return store.Ticket{}, f.err
	}
	return f.out, nil
}

func uuid(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("parsing uuid: %v", err)
	}
	return id
}

// stubProvisioner satisfies auth.Provisioner so the request can go through the
// real RequireAuth rather than a test-only way of putting a user in the
// context. That matters: it proves the handler reads the caller from where the
// middleware actually puts it, and that there is no back door to forge one.
type stubProvisioner struct{ user store.User }

func (s stubProvisioner) GetUserByClerkID(context.Context, string) (store.User, error) {
	return s.user, nil
}

func (s stubProvisioner) UpsertUserFromClerk(context.Context, store.UpsertUserFromClerkParams) (store.User, error) {
	return s.user, nil
}

// authenticated wraps a handler in RequireAuth and returns a request already
// carrying verified Clerk claims, as clerkhttp.WithHeaderAuthorization would
// have left it.
func authenticated(t *testing.T, caller store.User, h http.Handler, body string) (http.Handler, *http.Request) {
	t.Helper()

	wrapped := auth.RequireAuth(stubProvisioner{user: caller}, nil)(h)

	r := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	claims := &clerk.SessionClaims{
		RegisteredClaims: clerk.RegisteredClaims{Subject: caller.ClerkUserID},
	}
	return wrapped, r.WithContext(clerk.ContextWithSessionClaims(r.Context(), claims))
}

const validTicketBody = `{
	"title": "Cannot download my invoice",
	"description": "The download button returns a 500.",
	"category": "billing",
	"priority": "normal"
}`

func customer(t *testing.T) store.User {
	t.Helper()
	return store.User{
		ID:          uuid(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		ClerkUserID: "user_customer",
		Email:       "customer@example.test",
		Role:        ticket.RoleCustomer,
	}
}

func TestCreateTicketReturns201WithTheTicket(t *testing.T) {
	caller := customer(t)
	due := time.Now().Add(24 * time.Hour).UTC()
	creator := &fakeCreator{out: store.Ticket{
		ID:          uuid(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		Title:       "Cannot download my invoice",
		Description: "The download button returns a 500.",
		Category:    ticket.CategoryBilling,
		Priority:    ticket.PriorityNormal,
		Status:      ticket.StatusOpen,
		SlaDueAt:    &due,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}}

	handler, r := authenticated(t, caller, api.CreateTicketHandler(creator), validTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/tickets/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" {
		t.Errorf("Location = %q", got)
	}

	var body api.TicketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Status != ticket.StatusOpen {
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
	caller := customer(t)
	creator := &fakeCreator{}

	body := `{
		"title": "Hello",
		"description": "World",
		"category": "billing",
		"priority": "normal",
		"requester_id": "cccccccc-cccc-cccc-cccc-cccccccccccc"
	}`

	handler, r := authenticated(t, caller, api.CreateTicketHandler(creator), body)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if creator.calls != 1 {
		t.Fatalf("Create calls = %d, want 1", creator.calls)
	}
	if creator.got.RequesterID != caller.ID {
		t.Errorf("requester = %v, want the authenticated caller %v", creator.got.RequesterID, caller.ID)
	}
	if creator.got.ActorRole != ticket.RoleCustomer {
		t.Errorf("actor role = %q, want the caller's role from our table", creator.got.ActorRole)
	}
}

func TestCreateTicketRejectsAnUnauthenticatedRequest(t *testing.T) {
	creator := &fakeCreator{}

	handler := auth.RequireAuth(stubProvisioner{}, nil)(api.CreateTicketHandler(creator))
	r := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(validTicketBody))

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

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), body)
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

	body_ := decodeProblem(t, rec)
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

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), `{"title":`)
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
	creator := &fakeCreator{err: store.ErrNoPolicyForPriority}

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), validTicketBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestStoreFailuresDoNotLeakTheirCause(t *testing.T) {
	creator := &fakeCreator{err: errors.New(`pq: relation "tickets" does not exist on host db.internal`)}

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), validTicketBody)
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

	handler, r := authenticated(t, customer(t), api.CreateTicketHandler(creator), body)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if creator.got.Title != "Padded" {
		t.Errorf("title = %q, want Padded", creator.got.Title)
	}
	if creator.got.Description != "Body" {
		t.Errorf("description = %q, want Body", creator.got.Description)
	}
}
